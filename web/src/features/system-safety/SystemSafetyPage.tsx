import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import { PageHeader } from '@/components/ui/page-header'
import { useAccount } from '@/shared/account/AccountProvider'
import {
  getAutomationStatus,
  getRiskBreakers,
  getRiskStatus,
  resetRiskBreaker,
  resumeMarketKillSwitch,
  setAutomationJobEnabled,
  stopMarketKillSwitch,
  toggleKillSwitch,
} from '@/shared/api/endpoints'
import { ConfirmationDialog } from '@/shared/components/ConfirmationDialog'
import { EmptyState, ErrorState, LoadingState } from '@/shared/components/QueryStates'
import { queryKeys } from '@/shared/query/keys'
import { marketTypes } from '@/shared/types/domain'

type SafetyAction =
  | { kind: 'kill-switch'; active: boolean }
  | { kind: 'market'; market: string; active: boolean }
  | { kind: 'breaker'; scope: string }
  | { kind: 'automation'; name: string; enabled: boolean }

function actionTitle(action: SafetyAction) {
  switch (action.kind) {
    case 'kill-switch':
      return `${action.active ? 'Activate' : 'Deactivate'} global kill switch`
    case 'market':
      return `${action.active ? 'Stop' : 'Resume'} ${action.market} market`
    case 'breaker':
      return `Reset ${action.scope} breaker`
    case 'automation':
      return `${action.enabled ? 'Enable' : 'Disable'} ${action.name}`
  }
}

function actionExplanation(action: SafetyAction) {
  switch (action.kind) {
    case 'kill-switch':
      return action.active
        ? 'This blocks execution globally. Confirm the operator reason before continuing.'
        : 'This removes the global execution block and requires the one-shot admin key.'
    case 'market':
      return action.active
        ? `This blocks new ${action.market} execution globally.`
        : `This removes the global ${action.market} execution block.`
    case 'breaker':
      return 'This clears a persisted global breaker and requires the one-shot admin key.'
    case 'automation':
      return 'This changes whether the global automation job may run. It does not dispatch the job.'
  }
}

export function SystemSafetyPage() {
  const { account } = useAccount()
  const queryClient = useQueryClient()
  const [adminKey, setAdminKey] = useState('')
  const [reason, setReason] = useState('')
  const [pendingAction, setPendingAction] = useState<SafetyAction | null>(null)
  const [mutationMessage, setMutationMessage] = useState<string | null>(null)

  const status = useQuery({
    queryKey: queryKeys.riskStatus(account.id),
    queryFn: ({ signal }) => getRiskStatus(account.id, signal),
  })
  const breakers = useQuery({
    queryKey: queryKeys.riskBreakers,
    queryFn: ({ signal }) => getRiskBreakers(signal),
  })
  const automation = useQuery({
    queryKey: queryKeys.automationStatus,
    queryFn: ({ signal }) => getAutomationStatus(signal),
  })

  async function refreshSafetyState() {
    await Promise.all([
      queryClient.refetchQueries({ queryKey: queryKeys.riskStatus(account.id) }),
      queryClient.refetchQueries({ queryKey: queryKeys.riskBreakers }),
      queryClient.refetchQueries({ queryKey: queryKeys.automationStatus }),
    ])
  }

  const mutation = useMutation({
    retry: false,
    mutationFn: async (action: SafetyAction) => {
      switch (action.kind) {
        case 'kill-switch':
          return toggleKillSwitch(
            { active: action.active, reason: reason.trim() },
            action.active ? undefined : adminKey.trim(),
          )
        case 'market':
          return action.active
            ? stopMarketKillSwitch(action.market, { reason: reason.trim() })
            : resumeMarketKillSwitch(action.market)
        case 'breaker':
          return resetRiskBreaker({ scope: action.scope }, adminKey.trim())
        case 'automation':
          return setAutomationJobEnabled(action.name, action.enabled)
      }
    },
    onSuccess: async () => {
      await refreshSafetyState()
      setMutationMessage('Request accepted. Refreshed server state is shown below.')
      setAdminKey('')
      setPendingAction(null)
    },
  })

  function openAction(action: SafetyAction) {
    mutation.reset()
    setMutationMessage(null)
    setPendingAction(action)
  }

  const error = status.error ?? breakers.error ?? automation.error

  return (
    <div className="detail-stack">
      <PageHeader
        eyebrow="Global controls"
        title="System Safety"
        description="Global kill switches, breaker recovery, market stops, and automation enable controls. Account risk evidence remains in the account Risk page."
      />

      {error ? <ErrorState error={error} onRetry={() => void refreshSafetyState()} /> : null}
      {mutationMessage ? <p role="status">{mutationMessage}</p> : null}

      <section className="panel" aria-labelledby="global-kill-switch-heading">
        <div className="panel-header">
          <div>
            <h2 id="global-kill-switch-heading">Global kill switch</h2>
            <p className="muted">
              Current state is read from the resolved account risk status; the control itself is
              global.
            </p>
          </div>
          {status.data ? (
            <span className={`status-pill ${status.data.kill_switch.active ? 'warning' : 'active'}`}>
              {status.data.kill_switch.active ? 'Active' : 'Inactive'}
            </span>
          ) : null}
        </div>
        {status.isLoading ? <LoadingState label="Loading global safety state…" /> : null}
        <div className="filter-bar">
          <label>
            Operator reason
            <input value={reason} onChange={(event) => setReason(event.target.value)} />
          </label>
          <label>
            One-shot admin key
            <input
              type="password"
              value={adminKey}
              onChange={(event) => setAdminKey(event.target.value)}
            />
          </label>
        </div>
        <div className="action-row">
          <button
            className="danger-button"
            type="button"
            disabled={!reason.trim() || mutation.isPending}
            onClick={() => openAction({ kind: 'kill-switch', active: true })}
          >
            Activate global kill switch
          </button>
          <button
            className="secondary-button"
            type="button"
            disabled={!reason.trim() || !adminKey.trim() || mutation.isPending}
            onClick={() => openAction({ kind: 'kill-switch', active: false })}
          >
            Deactivate global kill switch
          </button>
        </div>
      </section>

      <section className="panel" aria-labelledby="market-controls-heading">
        <h2 id="market-controls-heading">Per-market stop and resume</h2>
        <div className="table-wrap">
          <table aria-label="Global market safety controls">
            <thead>
              <tr>
                <th>Market</th>
                <th>State</th>
                <th>Reason</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {marketTypes.map((market) => {
                const marketState = status.data?.market_kill_switches?.[market]
                const stopped = Boolean(marketState?.active)
                return (
                  <tr key={market}>
                    <td>{market}</td>
                    <td>{stopped ? 'Stopped' : 'Open'}</td>
                    <td>{marketState?.reason ?? '—'}</td>
                    <td>
                      <div className="action-row compact-actions">
                        <button
                          type="button"
                          disabled={!status.data || stopped || !reason.trim() || mutation.isPending}
                          onClick={() => openAction({ kind: 'market', market, active: true })}
                        >
                          Stop {market} market
                        </button>
                        <button
                          type="button"
                          disabled={!status.data || !stopped || mutation.isPending}
                          onClick={() => openAction({ kind: 'market', market, active: false })}
                        >
                          Resume {market} market
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel" aria-labelledby="tripped-breakers-heading">
        <h2 id="tripped-breakers-heading">Tripped breakers</h2>
        {breakers.isLoading ? <LoadingState label="Loading tripped breakers…" /> : null}
        {breakers.data?.tripped.length === 0 ? (
          <EmptyState
            title="No tripped breakers"
            message="No persisted global risk breakers are currently tripped."
          />
        ) : null}
        <ul>
          {breakers.data?.tripped.map((breaker) => (
            <li key={`${breaker.scope}-${breaker.tripped_at}`}>
              <code>{breaker.scope}</code> — {breaker.reason}{' '}
              <button
                type="button"
                disabled={!adminKey.trim() || mutation.isPending}
                onClick={() => openAction({ kind: 'breaker', scope: breaker.scope })}
              >
                Reset {breaker.scope} breaker
              </button>
            </li>
          ))}
        </ul>
      </section>

      <section className="panel" aria-labelledby="automation-safety-heading">
        <h2 id="automation-safety-heading">Automation status</h2>
        {automation.isLoading ? <LoadingState label="Loading automation status…" /> : null}
        {automation.data?.length === 0 ? (
          <EmptyState
            title="No automation jobs"
            message="No global automation job controls were returned."
          />
        ) : null}
        <ul>
          {automation.data?.map((job) => (
            <li key={job.name}>
              <strong>{job.name}</strong> — {job.running ? 'running' : job.last_result || 'idle'}{' '}
              <button
                type="button"
                disabled={mutation.isPending}
                onClick={() =>
                  openAction({ kind: 'automation', name: job.name, enabled: !job.enabled })
                }
              >
                {job.enabled ? 'Disable' : 'Enable'} {job.name}
              </button>
            </li>
          ))}
        </ul>
      </section>

      <ConfirmationDialog
        open={Boolean(pendingAction)}
        title={pendingAction ? actionTitle(pendingAction) : 'Confirm safety change'}
        confirmLabel="Confirm safety change"
        tone="danger"
        busy={mutation.isPending}
        disableDismiss={mutation.isPending}
        error={
          mutation.error
            ? 'The request did not complete cleanly. Completion may be unknown; refresh and verify server state before retrying.'
            : undefined
        }
        onConfirm={() => {
          if (pendingAction) mutation.mutate(pendingAction)
        }}
        onCancel={() => {
          mutation.reset()
          setPendingAction(null)
        }}
      >
        <p>{pendingAction ? actionExplanation(pendingAction) : null}</p>
      </ConfirmationDialog>
    </div>
  )
}

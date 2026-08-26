import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { PageHeader } from '@/components/ui/page-header'
import { StatusBadge } from '@/components/ui/status-badge'
import { getAutomationHealth, getAutomationStatus, runAutomationJob, setAutomationJobEnabled } from '@/shared/api/endpoints'
import { EmptyState, ErrorState, LastUpdated, LoadingState } from '@/shared/components/QueryStates'
import { queryKeys } from '@/shared/query/keys'
import type { AutomationJobStatus } from '@/shared/types/domain'

import { automationCutover, automationDependencyReason, automationOperationalState, currentAutomationErrorCount } from './automationCutover'
import { UnavailableCapabilitiesPanel } from './UnavailableCapabilitiesPanel'

function formatRelativeTime(iso?: string): string {
  if (!iso) return 'Never'
  const diff = Date.now() - new Date(iso).getTime()
  const seconds = Math.max(0, Math.floor(diff / 1000))
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

function JobStatePill({ job }: { job: AutomationJobStatus }) {
  const state = automationOperationalState(job)
  if (state === 'disabled') return <StatusBadge status="unknown" label="disabled" />
  if (state === 'running') return <StatusBadge status="running" />
  if (state === 'unverified') return <StatusBadge status="unknown" label="unverified" />
  if (state === 'blocked') return <StatusBadge status="warning" label="blocked" />
  if (state === 'failing') return <StatusBadge status="danger" label="failing" />
  if (state === 'degraded') return <StatusBadge status="warning" label="degraded" />
  return <StatusBadge status="success" label="healthy" />
}

function AutomationActions({ job }: { job: AutomationJobStatus }) {
  const queryClient = useQueryClient()
  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.automationStatus }),
      queryClient.invalidateQueries({ queryKey: queryKeys.automationHealth }),
      queryClient.invalidateQueries({ queryKey: queryKeys.automationRuns({ limit: 50, offset: 0 }) }),
    ])
  }
  const runMutation = useMutation({ mutationFn: () => runAutomationJob(job.name), onSuccess: invalidate })
  const toggleMutation = useMutation({ mutationFn: () => setAutomationJobEnabled(job.name, !job.enabled), onSuccess: invalidate })
  const busy = runMutation.isPending || toggleMutation.isPending

  return (
    <div className="table-actions">
      <button type="button" className="compact-button" disabled={busy || job.running || !job.enabled} onClick={() => runMutation.mutate()}>
        {runMutation.isPending ? 'Running…' : 'Run now'}
      </button>
      <button type="button" className="compact-button secondary-button" disabled={busy || job.running} onClick={() => toggleMutation.mutate()}>
        {toggleMutation.isPending ? 'Saving…' : job.enabled ? 'Disable' : 'Enable'}
      </button>
    </div>
  )
}

export function AutomationPage() {
  const statusQuery = useQuery({ queryKey: queryKeys.automationStatus, queryFn: ({ signal }) => getAutomationStatus(signal), refetchInterval: 30_000 })
  const healthQuery = useQuery({ queryKey: queryKeys.automationHealth, queryFn: ({ signal }) => getAutomationHealth(signal), refetchInterval: 30_000 })
  const jobs = statusQuery.data ?? []
  const orderedJobs = [...jobs].sort((a, b) => {
    const priority = (job: AutomationJobStatus) => ({ running: 0, failing: 1, blocked: 2, degraded: 3, unverified: 4, healthy: 5, disabled: 6 })[automationOperationalState(job)]
    return priority(a) - priority(b) || a.name.localeCompare(b.name)
  })
  const attentionJobs = orderedJobs.filter((job) => ['running', 'failing', 'blocked', 'degraded'].includes(automationOperationalState(job)))
  const unverifiedJobs = orderedJobs.filter((job) => automationOperationalState(job) === 'unverified')
  const failingJobs = orderedJobs.filter((job) => automationOperationalState(job) === 'failing').length
  const degradedJobs = orderedJobs.filter((job) => automationOperationalState(job) === 'degraded').length
  const blockedJobs = orderedJobs.filter((job) => automationOperationalState(job) === 'blocked').length
  const healthState = healthQuery.error ? 'unavailable' : healthQuery.data ? 'known' : 'unknown'
  const unavailableJobs = healthState === 'known' ? healthQuery.data?.unavailable_jobs ?? [] : []
  const overallState = healthState === 'unknown'
    ? 'Unknown'
    : healthState === 'unavailable'
      ? 'Unavailable'
      : failingJobs > 0 || degradedJobs > 0 || blockedJobs > 0
        ? 'Degraded'
        : unverifiedJobs.length > 0
          ? 'Unverified'
          : unavailableJobs.length > 0
            ? 'Limited'
            : 'Healthy'
  const overallMessage = healthState === 'unknown'
    ? 'Capability diagnostics are loading. Overall automation health cannot be confirmed.'
    : healthState === 'unavailable'
      ? 'Capability diagnostics could not be loaded. Overall automation health cannot be confirmed.'
      : attentionJobs.length > 0
        ? `${attentionJobs.length} job${attentionJobs.length === 1 ? '' : 's'} running, blocked, or reporting post-deployment failures. They are listed first below.`
        : unverifiedJobs.length > 0
          ? `${unverifiedJobs.length} job${unverifiedJobs.length === 1 ? '' : 's'} await a post-deployment run. Pre-deployment failures are excluded.`
          : unavailableJobs.length > 0
            ? `${unavailableJobs.length} capabilit${unavailableJobs.length === 1 ? 'y is' : 'ies are'} unavailable and listed separately from runnable jobs.`
            : 'No running or failed jobs currently require operator attention.'

  return (
    <div className="detail-stack">
      <PageHeader eyebrow="Paper operations" title="Automations" description="What is running, what needs attention, and when each paper-trading job last completed." actions={<LastUpdated date={statusQuery.dataUpdatedAt || undefined} />} />

      <section className="panel operations-hero">
        <div><p className="eyebrow">Scheduler state</p><h2>{statusQuery.isLoading ? 'Loading automation status' : statusQuery.error ? 'Automation status unavailable' : overallState === 'Unknown' ? 'Automation health is unknown' : overallState === 'Unavailable' ? 'Automation health is unavailable' : overallState === 'Healthy' ? 'Automation is operating normally' : overallState === 'Unverified' ? 'Automation awaits verification' : overallState === 'Limited' ? 'Automation capabilities are limited' : 'Automation needs attention'}</h2><p className="muted">{statusQuery.isLoading || statusQuery.error ? 'Operational health is shown after the job status refresh completes.' : overallMessage}</p></div>
        {!statusQuery.isLoading && !statusQuery.error ? (
          <div className="operations-metrics" aria-label="Automation summary">
            <div><span>Registered</span><strong>{jobs.length}</strong></div><div><span>Failing</span><strong>{failingJobs}</strong></div><div><span>Blocked</span><strong>{blockedJobs}</strong></div><div><span>Degraded</span><strong>{degradedJobs}</strong></div><div><span>Unverified</span><strong>{unverifiedJobs.length}</strong></div><div><span>Overall</span><strong>{overallState}</strong></div>
          </div>
        ) : null}
      </section>

      {healthState !== 'known' ? (
        <section className="panel unavailable-capabilities" aria-labelledby="capability-diagnostics-title">
          <div className="panel-header unavailable-capabilities-header">
            <div>
              <p className="eyebrow">Capability boundary</p>
              <h2 id="capability-diagnostics-title">{healthState === 'unknown' ? 'Checking capability availability' : 'Capability diagnostics unavailable'}</h2>
              <p className="muted">Runnable job status remains visible, but overall health cannot be confirmed without the capability diagnostics.</p>
            </div>
          </div>
          {healthState === 'unknown'
            ? <LoadingState label="Loading capability diagnostics…" />
            : <ErrorState error={healthQuery.error} onRetry={() => void healthQuery.refetch()} />}
        </section>
      ) : unavailableJobs.length > 0 ? <UnavailableCapabilitiesPanel jobs={unavailableJobs} /> : null}

      <section className="panel">
        <div className="panel-header"><div><p className="eyebrow">Execution queue</p><h2>Scheduled jobs</h2><p className="muted">Running and unhealthy post-deployment jobs are pinned to the top. Cutover: {automationCutover.deployment}.</p></div></div>
        {statusQuery.isLoading ? <LoadingState label="Loading automation jobs…" /> : null}
        {statusQuery.error ? <ErrorState error={statusQuery.error} onRetry={() => void statusQuery.refetch()} /> : null}
        {!statusQuery.isLoading && !statusQuery.error && jobs.length === 0 ? <EmptyState title="No automations found" message="The automation orchestrator is not reporting any registered jobs." /> : null}

        {jobs.length > 0 ? (
          <div className="table-wrap" role="region" aria-label="Automation jobs table" tabIndex={0}>
            <table className="operations-table automation-table" aria-label="Automation jobs">
              <thead>
                <tr>
                  <th scope="col">Job</th>
                  <th scope="col">State</th>
                  <th scope="col">Schedule</th>
                  <th scope="col">Last run</th>
                  <th scope="col">Runs</th>
                  <th scope="col">Errors</th>
                  <th scope="col">Actions</th>
                </tr>
              </thead>
              <tbody>
                {orderedJobs.map((job) => (
                  <tr key={job.name}>
                    <th scope="row">
                      <Link to={`/automation/${encodeURIComponent(job.name)}`}>{job.name}</Link>
                      <p className="cell-detail" title={job.description}>{job.description}</p>
                    </th>
                    <td>
                      <JobStatePill job={job} />
                      {automationOperationalState(job) === 'blocked' && automationDependencyReason(job) ? <span className="cell-detail" title={automationDependencyReason(job)}>{automationDependencyReason(job)}</span> : null}
                      {automationOperationalState(job) === 'degraded' && job.last_detail ? <span className="cell-detail" title={job.last_detail}>{job.last_detail}</span> : null}
                      {job.settlement_gate ? <span className="cell-detail">{job.settlement_gate.eligible ? 'Gate ready' : `Gate ${job.settlement_gate.consecutive_dry_run_successes}/${job.settlement_gate.threshold}`} · {job.settlement_gate.would_settle_decisions} pending</span> : null}
                    </td>
                    <td>{job.schedule || 'Manual only'}</td>
                    <td>{formatRelativeTime(job.last_run)}</td>
                    <td>{job.run_count}</td>
                    <td>{automationOperationalState(job) === 'unverified' ? <><StatusBadge status="unknown" label="0 current" /><span className="cell-detail">Pre-deploy history excluded after {automationCutover.deployment}</span></> : currentAutomationErrorCount(job) > 0 ? <><StatusBadge status="danger" label={`${currentAutomationErrorCount(job)} current`} /><span className="cell-detail">{currentAutomationErrorCount(job)} consecutive</span>{job.last_error ? <span className="cell-detail" title={job.last_error}>{job.last_error}</span> : null}</> : '0'}</td>
                    <td><AutomationActions job={job} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </section>
    </div>
  )
}

import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { RefreshCw } from 'lucide-react'

import { Alert } from '@/components/ui/alert'
import { PageHeader } from '@/components/ui/page-header'
import {
  getAllocationDecisions,
  getAllocatorDiagnostics,
  getAllocatorOpportunities,
  getAllocatorSummary,
  getEconomicCapitalFlows,
  getEconomicCapitalSummary,
  getEconomicLedgerTransaction,
  getOpenPortfolioPositions,
  getPortfolioSummary,
} from '@/shared/api/endpoints'
import {
  Breadcrumbs,
  EntityId,
  EntityLink,
} from '@/shared/components/EntityLinks'
import { useAccount } from '@/shared/account/AccountProvider'
import {
  EmptyState,
  ErrorState,
  LastUpdated,
  LoadingState,
  StaleBanner,
} from '@/shared/components/QueryStates'
import { queryKeys } from '@/shared/query/keys'
import {
  marketTypes,
  type AllocationDecision,
  type AllocatorDiagnostics,
  type AllocatorOpportunity,
  type EconomicCapitalFlow,
  type Position,
} from '@/shared/types/domain'
import { useRealtime } from '@/shared/websocket/RealtimeProvider'

const pageSize = 20
const uuidPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

function money(value?: number | string | null) {
  if (value === undefined || value === null) return 'Unknown'
  return new Intl.NumberFormat(undefined, {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: 2,
  }).format(Number(value))
}

function numberValue(value?: number) {
  if (value === undefined) return 'Unknown'
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 4 }).format(
    value,
  )
}

function percent(value?: number) {
  if (value === undefined) return 'Unknown'
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(value)}%`
}

function exactMoney(value: string, currency = 'USD') {
  const [whole = '0', fraction = ''] = value.split('.')
  const negative = whole.startsWith('-')
  const digits = negative ? whole.slice(1) : whole
  const grouped = digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const decimals = fraction
    .padEnd(2, '0')
    .slice(0, Math.max(2, Math.min(fraction.length, 8)))
  return `${negative ? '-' : ''}${currency} ${grouped}.${decimals}`
}

function CapitalFlowRows({ flows }: { flows: EconomicCapitalFlow[] }) {
  if (flows.length === 0)
    return <p className="muted">No capital flows recorded.</p>
  return (
    <div className="table-wrap">
      <table aria-label="Capital flow records">
        <thead>
          <tr>
            <th>Type</th>
            <th>Amount</th>
            <th>Source</th>
            <th>Effective</th>
            <th>Observed</th>
          </tr>
        </thead>
        <tbody>
          {flows.map((flow) => (
            <tr key={flow.id}>
              <td>{flow.type}</td>
              <td>{exactMoney(flow.amount, flow.currency)}</td>
              <td>{flow.source}</td>
              <td>{new Date(flow.effective_at).toLocaleString()}</td>
              <td>{new Date(flow.observed_at).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function CapitalLedgerPanel({ accountId }: { accountId: string }) {
  const [ledgerInput, setLedgerInput] = useState('')
  const [ledgerId, setLedgerId] = useState('')
  const trimmedLedgerInput = ledgerInput.trim()
  const ledgerInputValid =
    trimmedLedgerInput === '' || uuidPattern.test(trimmedLedgerInput)
  const summary = useQuery({
    queryKey: queryKeys.economicCapitalSummary(accountId, accountId),
    queryFn: ({ signal }) =>
      getEconomicCapitalSummary(accountId, accountId, signal),
  })
  const flows = useQuery({
    queryKey: queryKeys.economicCapitalFlows(accountId, accountId),
    queryFn: ({ signal }) =>
      getEconomicCapitalFlows(accountId, accountId, signal),
  })
  const ledger = useQuery({
    queryKey: queryKeys.economicLedgerTransaction(
      accountId,
      ledgerId || 'none',
    ),
    queryFn: ({ signal }) =>
      getEconomicLedgerTransaction(accountId, ledgerId, signal),
    enabled: Boolean(ledgerId),
    retry: false,
  })

  return (
    <div className="detail-stack" role="tabpanel" aria-label="Capital ledger">
      <section className="panel" aria-labelledby="capital-ledger-heading">
        <div className="panel-header">
          <div>
            <p className="eyebrow">Canonical economic records</p>
            <h2 id="capital-ledger-heading">Capital ledger</h2>
            <p className="muted">
              Read-only capital contributions and withdrawals for the resolved
              execution account. These values are not cash, equity, P&amp;L, or
              spendable buying power.
            </p>
          </div>
          <span className="status-pill unknown">Read only</span>
        </div>
        {summary.isLoading ? (
          <LoadingState label="Loading capital summary…" />
        ) : null}
        {summary.error ? (
          <ErrorState
            error={summary.error}
            onRetry={() => void summary.refetch()}
          />
        ) : null}
        {summary.data ? (
          <div
            className="metrics-grid"
            aria-label="Capital contribution summary"
          >
            <div className="panel nested-panel">
              <span>Starting capital</span>
              <strong>
                {exactMoney(
                  summary.data.starting_capital,
                  summary.data.currency,
                )}
              </strong>
            </div>
            <div className="panel nested-panel">
              <span>Deposits</span>
              <strong>
                {exactMoney(summary.data.deposits, summary.data.currency)}
              </strong>
            </div>
            <div className="panel nested-panel">
              <span>Withdrawals</span>
              <strong>
                {exactMoney(summary.data.withdrawals, summary.data.currency)}
              </strong>
            </div>
            <div className="panel nested-panel">
              <span>Net contributed capital</span>
              <strong>
                {exactMoney(summary.data.net_capital, summary.data.currency)}
              </strong>
            </div>
          </div>
        ) : null}
      </section>

      <section className="panel" aria-labelledby="capital-flows-heading">
        <div className="panel-header">
          <div>
            <h2 id="capital-flows-heading">Capital flow records</h2>
            <p className="muted">
              Stored account records ordered by the server.
            </p>
          </div>
          {flows.data ? <LastUpdated date={flows.dataUpdatedAt} /> : null}
        </div>
        {flows.isLoading ? (
          <LoadingState label="Loading capital flows…" />
        ) : null}
        {flows.error ? (
          <ErrorState
            error={flows.error}
            onRetry={() => void flows.refetch()}
          />
        ) : null}
        {flows.data ? <CapitalFlowRows flows={flows.data.data} /> : null}
      </section>

      <section className="panel" aria-labelledby="ledger-trace-heading">
        <div className="panel-header">
          <div>
            <h2 id="ledger-trace-heading">Ledger transaction trace</h2>
            <p className="muted">
              Inspect one account-owned transaction and its balanced postings by
              UUID.
            </p>
          </div>
        </div>
        <form
          className="filter-bar"
          aria-label="Ledger transaction lookup"
          onSubmit={(event) => {
            event.preventDefault()
            if (uuidPattern.test(trimmedLedgerInput))
              setLedgerId(trimmedLedgerInput)
          }}
        >
          <label>
            Transaction UUID
            <input
              value={ledgerInput}
              onChange={(event) => setLedgerInput(event.target.value)}
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              aria-invalid={!ledgerInputValid}
            />
          </label>
          <button
            type="submit"
            disabled={!uuidPattern.test(trimmedLedgerInput)}
          >
            Inspect transaction
          </button>
        </form>
        {!ledgerInputValid ? (
          <p className="error-text">Enter a valid UUID.</p>
        ) : null}
        {!ledgerId ? (
          <p className="muted">
            Enter a stored ledger transaction UUID to inspect its postings.
          </p>
        ) : null}
        {ledger.isLoading ? (
          <LoadingState label="Loading ledger transaction…" />
        ) : null}
        {ledger.error ? (
          <ErrorState
            error={ledger.error}
            onRetry={() => void ledger.refetch()}
          />
        ) : null}
        {ledger.data ? (
          <div className="detail-stack">
            <dl className="kv-grid">
              <dt>Event type</dt>
              <dd>{ledger.data.event_type}</dd>
              <dt>Origin</dt>
              <dd>
                {ledger.data.origin_type} / {ledger.data.origin_id}
              </dd>
              <dt>Effective</dt>
              <dd>{new Date(ledger.data.effective_at).toLocaleString()}</dd>
              <dt>Observed</dt>
              <dd>{new Date(ledger.data.observed_at).toLocaleString()}</dd>
            </dl>
            <div className="table-wrap">
              <table aria-label="Ledger transaction postings">
                <thead>
                  <tr>
                    <th>Ledger account</th>
                    <th>Unit kind</th>
                    <th>Unit</th>
                    <th>Amount</th>
                  </tr>
                </thead>
                <tbody>
                  {ledger.data.postings.map((posting) => (
                    <tr key={posting.id}>
                      <td>{posting.ledger_account}</td>
                      <td>{posting.unit_kind}</td>
                      <td>{posting.unit}</td>
                      <td>{posting.amount}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        ) : null}
      </section>
    </div>
  )
}

function compactMap(
  entries: Record<string, number>,
  emptyLabel = 'No counts reported.',
) {
  const pairs = Object.entries(entries).sort(([left], [right]) =>
    left.localeCompare(right),
  )
  if (pairs.length === 0) return <p className="muted">{emptyLabel}</p>
  return (
    <dl className="compact-kv">
      {pairs.map(([key, value]) => (
        <div key={key}>
          <dt>{key || 'unknown'}</dt>
          <dd>
            <span className={`status-pill ${value > 0 ? 'active' : 'unknown'}`}>
              {value}
            </span>
          </dd>
        </div>
      ))}
    </dl>
  )
}

function StatusPill({ value }: { value: string }) {
  const normalized = value.replaceAll('_', ' ')
  const known = [
    'queued',
    'selected',
    'rejected',
    'expired',
    'executed',
    'shadow',
    'paper',
    'select',
    'reject',
    'hold',
    'buy',
    'sell',
  ].includes(value)
  return (
    <span className={`status-pill ${known ? 'active' : 'unknown'}`}>
      {known ? normalized : `Unknown: ${normalized}`}
    </span>
  )
}

function SidePill({ value }: { value: string }) {
  const known = ['long', 'short'].includes(value)
  return (
    <span className={`status-pill ${known ? 'active' : 'unknown'}`}>
      {known ? value : `Unknown: ${value}`}
    </span>
  )
}

function PositionInstrument({ position }: { position: Position }) {
  if (position.asset_class !== 'option') return <>{position.ticker}</>
  return (
    <>
      {position.ticker}
      <br />
      <span className="cell-detail">
        {position.underlying_ticker ?? 'Unknown underlying'} ·{' '}
        {position.option_type ?? 'unknown type'}{' '}
        {position.strike === undefined
          ? 'unknown strike'
          : money(position.strike)}{' '}
        ·{' '}
        {position.expiry
          ? new Date(position.expiry).toLocaleDateString()
          : 'unknown expiry'}{' '}
        · {position.contract_multiplier ?? 'unknown'}×
        {position.leg_group_id
          ? ` · leg ${position.leg_group_id.slice(0, 8)}`
          : ''}
      </span>
    </>
  )
}

function PositionRows({ positions }: { positions: Position[] }) {
  return (
    <>
      <div className="table-wrap">
        <table
          className="operations-table positions-table"
          aria-label="Open positions"
        >
          <thead>
            <tr>
              <th>Position</th>
              <th>Ticker</th>
              <th>Side</th>
              <th>Quantity</th>
              <th>Average entry</th>
              <th>Current</th>
              <th>Unrealized P/L</th>
              <th>Realized P/L</th>
              <th>Opened</th>
            </tr>
          </thead>
          <tbody>
            {positions.map((position) => (
              <tr key={position.id}>
                <td>
                  <EntityLink kind="position" id={position.id} />
                  {position.strategy_id ? (
                    <>
                      <br />
                      <EntityLink
                        kind="strategy"
                        id={position.strategy_id}
                        copy={false}
                      />
                    </>
                  ) : null}
                </td>
                <td>
                  <PositionInstrument position={position} />
                </td>
                <td>
                  <SidePill value={position.side} />
                </td>
                <td>{numberValue(position.quantity)}</td>
                <td>{money(position.avg_entry)}</td>
                <td>{money(position.current_price)}</td>
                <td>{money(position.unrealized_pnl)}</td>
                <td>{money(position.realized_pnl)}</td>
                <td title={new Date(position.opened_at).toLocaleString()}>
                  {new Date(position.opened_at).toLocaleDateString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="card-list" aria-label="Open position cards">
        {positions.map((position) => (
          <article className="strategy-card" key={position.id}>
            <h3>
              <PositionInstrument position={position} />
            </h3>
            <p>
              <SidePill value={position.side} /> ·{' '}
              {numberValue(position.quantity)} units
            </p>
            <p>
              Unrealized {money(position.unrealized_pnl)} · Realized{' '}
              {money(position.realized_pnl)}
            </p>
            {position.strategy_id ? (
              <EntityLink
                kind="strategy"
                id={position.strategy_id}
                label="Open strategy"
                copy={false}
              />
            ) : null}
          </article>
        ))}
      </div>
    </>
  )
}

function DiagnosticsGrid({
  diagnostics,
}: {
  diagnostics: AllocatorDiagnostics
}) {
  const healthyValue = (value: number | undefined, formatter = percent) => (
    <span
      className={`status-pill ${value !== undefined && value > 0 ? 'active' : 'unknown'}`}
    >
      {formatter(value)}
    </span>
  )

  return (
    <div className="detail-stack">
      <section
        className="metrics-grid"
        aria-label="Allocator diagnostic summary"
      >
        <article className="panel">
          <p className="eyebrow">Runtime paper mode</p>
          <strong>
            <span
              className={`status-pill ${diagnostics.paper_evaluation.promotion_eligible && diagnostics.paper_evaluation.results_isolated ? 'active' : 'warning'}`}
            >
              {diagnostics.paper_evaluation.mode.replaceAll('_', ' ')}
            </span>
          </strong>
        </article>
        <article className="panel">
          <p className="eyebrow">Buying power</p>
          <strong>
            {healthyValue(diagnostics.buying_power_utilization_pct)}
          </strong>
        </article>
        <article className="panel">
          <p className="eyebrow">Exposure</p>
          <strong>{healthyValue(diagnostics.gross_exposure_pct)}</strong>
        </article>
        <article className="panel">
          <p className="eyebrow">Utilization gap</p>
          <strong>{healthyValue(diagnostics.utilization_gap_pct)}</strong>
        </article>
      </section>
      {!diagnostics.paper_evaluation.results_isolated ? (
        <Alert variant="warning">
          The runtime profile is labelled, but existing aggregate records are
          legacy and not yet account-scoped. Do not use these diagnostics to
          promote or rank a strategy.
        </Alert>
      ) : !diagnostics.paper_evaluation.promotion_eligible ? (
        <Alert variant="warning">
          This is{' '}
          {diagnostics.paper_evaluation.evidence_class.replaceAll('_', ' ')}{' '}
          evidence and must not be used to promote or rank a strategy.
        </Alert>
      ) : null}
      <div className="detail-grid">
        <article className="panel nested-panel">
          <h3>Evidence boundary</h3>
          <dl className="detail-grid">
            <div>
              <dt>Mode</dt>
              <dd>{diagnostics.paper_evaluation.mode}</dd>
            </div>
            <div>
              <dt>Storage namespace</dt>
              <dd>{diagnostics.paper_evaluation.storage_namespace}</dd>
            </div>
            <div>
              <dt>Evidence class</dt>
              <dd>{diagnostics.paper_evaluation.evidence_class}</dd>
            </div>
            <div>
              <dt>Profile promotion eligible</dt>
              <dd>
                {diagnostics.paper_evaluation.promotion_eligible ? 'Yes' : 'No'}
              </dd>
            </div>
            <div>
              <dt>Stored results isolated</dt>
              <dd>
                {diagnostics.paper_evaluation.results_isolated
                  ? 'Yes'
                  : 'No — legacy aggregate'}
              </dd>
            </div>
          </dl>
        </article>
        <article className="panel nested-panel">
          <h3>Exposure</h3>
          <dl className="detail-grid">
            <div>
              <dt>Buying power utilization</dt>
              <dd>
                <span
                  className={`status-pill ${diagnostics.buying_power_utilization_pct !== undefined && diagnostics.buying_power_utilization_pct <= 80 ? 'active' : 'unknown'}`}
                >
                  {percent(diagnostics.buying_power_utilization_pct)}
                </span>
              </dd>
            </div>
            <div>
              <dt>Gross exposure</dt>
              <dd>
                <span
                  className={`status-pill ${diagnostics.gross_exposure_pct !== undefined && diagnostics.gross_exposure_pct <= 100 ? 'active' : 'unknown'}`}
                >
                  {percent(diagnostics.gross_exposure_pct)}
                </span>
              </dd>
            </div>
            <div>
              <dt>Target exposure</dt>
              <dd>
                <span className="status-pill active">
                  {percent(diagnostics.target_gross_exposure_pct)}
                </span>
              </dd>
            </div>
            <div>
              <dt>Utilization gap</dt>
              <dd>
                <span
                  className={`status-pill ${diagnostics.utilization_gap_pct !== undefined && diagnostics.utilization_gap_pct <= 10 ? 'active' : 'unknown'}`}
                >
                  {percent(diagnostics.utilization_gap_pct)}
                </span>
              </dd>
            </div>
          </dl>
        </article>
        <article className="panel nested-panel">
          <h3>Run signals</h3>
          {compactMap(diagnostics.run_counts_by_signal)}
        </article>
        <article className="panel nested-panel">
          <h3>All-time legacy pipeline statuses</h3>
          <p className="muted">
            Global, unscoped counts. They do not describe the selected account
            or the current day.
          </p>
          {compactMap(diagnostics.run_counts_by_status)}
        </article>
        <article className="panel nested-panel">
          <h3>Decision statuses</h3>
          {compactMap(diagnostics.decision_counts_by_status)}
        </article>
        <article className="panel nested-panel">
          <h3>No-action reasons</h3>
          {compactMap(diagnostics.no_action_reasons)}
        </article>
        <article className="panel nested-panel">
          <h3>Active strategies by market</h3>
          {compactMap(diagnostics.active_strategies_by_market)}
        </article>
        <article className="panel nested-panel">
          <h3>Open positions by market</h3>
          {compactMap(
            diagnostics.open_positions_by_market,
            'No open positions.',
          )}
        </article>
      </div>
    </div>
  )
}

function OpportunityRows({
  opportunities,
}: {
  opportunities: AllocatorOpportunity[]
}) {
  return (
    <div className="table-wrap">
      <table aria-label="Allocator opportunities">
        <thead>
          <tr>
            <th>Opportunity</th>
            <th>Ticker</th>
            <th>Market</th>
            <th>Status</th>
            <th>Signal</th>
            <th>Edge</th>
            <th>Selected notional</th>
            <th>Reason</th>
          </tr>
        </thead>
        <tbody>
          {opportunities.map((opportunity) => (
            <tr key={opportunity.id}>
              <td>
                <EntityId
                  kind="opportunity"
                  id={opportunity.id}
                  label="Opportunity"
                />
                <br />
                <EntityLink
                  kind="strategy"
                  id={opportunity.strategy_id}
                  copy={false}
                />
                {opportunity.pipeline_run_id ? (
                  <>
                    <br />
                    <EntityLink
                      kind="run"
                      id={opportunity.pipeline_run_id}
                      tradeDate={opportunity.pipeline_run_trade_date?.slice(
                        0,
                        10,
                      )}
                      copy={false}
                    />
                  </>
                ) : null}
              </td>
              <td>{opportunity.ticker}</td>
              <td>{opportunity.market_type}</td>
              <td>
                <StatusPill value={opportunity.status} />
              </td>
              <td>{opportunity.signal}</td>
              <td>{percent(opportunity.edge_pct)}</td>
              <td>{money(opportunity.selected_notional)}</td>
              <td>{opportunity.reason}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function DecisionRows({ decisions }: { decisions: AllocationDecision[] }) {
  return (
    <div className="table-wrap">
      <table aria-label="Allocator decisions">
        <thead>
          <tr>
            <th>Decision</th>
            <th>Mode</th>
            <th>Action</th>
            <th>Score</th>
            <th>Notional</th>
            <th>Quantity</th>
            <th>Reasons</th>
          </tr>
        </thead>
        <tbody>
          {decisions.map((decision) => (
            <tr key={decision.id}>
              <td>
                <EntityId kind="decision" id={decision.id} />
                {decision.strategy_id ? (
                  <>
                    <br />
                    <EntityLink
                      kind="strategy"
                      id={decision.strategy_id}
                      copy={false}
                    />
                  </>
                ) : null}
                {decision.created_order_id ? (
                  <>
                    <br />
                    <EntityLink
                      kind="order"
                      id={decision.created_order_id}
                      label="Created order"
                      copy={false}
                    />
                  </>
                ) : null}
              </td>
              <td>
                <StatusPill value={decision.mode} />
              </td>
              <td>
                <StatusPill value={decision.action} />
              </td>
              <td>{numberValue(decision.score)}</td>
              <td>{money(decision.notional_usd)}</td>
              <td>{numberValue(decision.quantity)}</td>
              <td>{decision.reasons.join(', ')}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function AllocatorPanel({
  searchParams,
  setSearchParams,
}: {
  searchParams: URLSearchParams
  setSearchParams: (next: URLSearchParams) => void
}) {
  const { account } = useAccount()
  const accountId = account.id
  const opportunityOffset = Number(
    searchParams.get('opportunity_offset') ?? '0',
  )
  const decisionOffset = Number(searchParams.get('decision_offset') ?? '0')
  const opportunityFilters = useMemo(
    () => ({
      status: searchParams.get('status') || undefined,
      market_type: searchParams.get('market_type') || undefined,
      ticker: searchParams.get('ticker') || undefined,
      strategy_id: searchParams.get('strategy_id') || undefined,
      limit: 10,
      offset:
        Number.isFinite(opportunityOffset) && opportunityOffset > 0
          ? opportunityOffset
          : 0,
    }),
    [opportunityOffset, searchParams],
  )
  const decisionFilters = useMemo(
    () => ({
      mode: searchParams.get('mode') || undefined,
      action: searchParams.get('action') || undefined,
      strategy_id: searchParams.get('strategy_id') || undefined,
      limit: 10,
      offset:
        Number.isFinite(decisionOffset) && decisionOffset > 0
          ? decisionOffset
          : 0,
    }),
    [decisionOffset, searchParams],
  )
  const diagnosticsQuery = useQuery({
    queryKey: queryKeys.allocatorDiagnostics(accountId),
    queryFn: ({ signal }) => getAllocatorDiagnostics(accountId, signal),
  })
  const summaryQuery = useQuery({
    queryKey: queryKeys.allocatorSummary(accountId),
    queryFn: ({ signal }) => getAllocatorSummary(accountId, signal),
  })
  const opportunitiesQuery = useQuery({
    queryKey: queryKeys.allocatorOpportunities(accountId, opportunityFilters),
    queryFn: ({ signal }) =>
      getAllocatorOpportunities(accountId, opportunityFilters, signal),
  })
  const decisionsQuery = useQuery({
    queryKey: queryKeys.allocatorDecisions(accountId, decisionFilters),
    queryFn: ({ signal }) =>
      getAllocationDecisions(accountId, decisionFilters, signal),
  })
  const opportunities = opportunitiesQuery.data?.data ?? []
  const decisions = decisionsQuery.data?.data ?? []
  const opportunityTotal = opportunitiesQuery.data?.total
  const decisionTotal = decisionsQuery.data?.total
  const currentOpportunityOffset = opportunityFilters.offset ?? 0
  const currentDecisionOffset = decisionFilters.offset ?? 0

  function updateAllocatorFilters(updates: Record<string, string>) {
    const next = new URLSearchParams(searchParams)
    next.set('tab', 'allocator')
    for (const [key, value] of Object.entries(updates)) {
      if (value) next.set(key, value)
      else next.delete(key)
    }
    next.delete('opportunity_offset')
    next.delete('decision_offset')
    setSearchParams(next)
  }

  function setAllocatorOffset(
    key: 'opportunity_offset' | 'decision_offset',
    value: number,
  ) {
    const next = new URLSearchParams(searchParams)
    next.set('tab', 'allocator')
    if (value > 0) next.set(key, String(value))
    else next.delete(key)
    setSearchParams(next)
  }

  return (
    <div className="detail-stack" aria-label="Allocator diagnostics">
      <section className="panel" aria-labelledby="allocator-heading">
        <div className="panel-header">
          <div>
            <h2 id="allocator-heading">Allocator diagnostics</h2>
            <p className="muted">
              Read-only allocator health, shadow opportunities, and allocation
              decisions. Rebalance actions are excluded.
            </p>
          </div>
          {diagnosticsQuery.data ? (
            <LastUpdated date={diagnosticsQuery.dataUpdatedAt} />
          ) : null}
        </div>
        {diagnosticsQuery.isLoading ? (
          <LoadingState label="Loading allocator diagnostics…" />
        ) : null}
        {diagnosticsQuery.error ? (
          <ErrorState
            error={diagnosticsQuery.error}
            onRetry={() => void diagnosticsQuery.refetch()}
          />
        ) : null}
        {diagnosticsQuery.data ? (
          <DiagnosticsGrid diagnostics={diagnosticsQuery.data} />
        ) : null}
        {diagnosticsQuery.data?.warnings.length ? (
          <Alert variant="warning">
            Warnings: {diagnosticsQuery.data.warnings.join(', ')}
          </Alert>
        ) : null}
      </section>

      <section className="metrics-grid" aria-label="Allocator summary">
        <article className="panel">
          <p className="eyebrow">Queued opportunities</p>
          <strong>
            {summaryQuery.data?.opportunity_counts_by_status.queued ?? '—'}
          </strong>
        </article>
        <article className="panel">
          <p className="eyebrow">Selected opportunities</p>
          <strong>
            {summaryQuery.data?.opportunity_counts_by_status.selected ?? '—'}
          </strong>
        </article>
        <article className="panel">
          <p className="eyebrow">Recent decisions</p>
          <strong>{summaryQuery.data?.recent_decisions.length ?? '—'}</strong>
        </article>
      </section>
      {summaryQuery.error ? (
        <ErrorState
          error={summaryQuery.error}
          onRetry={() => void summaryQuery.refetch()}
        />
      ) : null}

      <section className="panel" aria-labelledby="opportunities-heading">
        <div className="panel-header">
          <div>
            <h2 id="opportunities-heading">Allocator opportunities</h2>
            <p className="muted">
              Backend-supported filters: ticker, market type, status, strategy.
            </p>
          </div>
          {opportunitiesQuery.data ? (
            <LastUpdated date={opportunitiesQuery.dataUpdatedAt} />
          ) : null}
        </div>
        <form
          className="filter-bar"
          aria-label="Allocator opportunity filters"
          onSubmit={(event) => event.preventDefault()}
        >
          <label>
            Ticker
            <input
              value={searchParams.get('ticker') ?? ''}
              onChange={(event) =>
                updateAllocatorFilters({
                  ticker: event.target.value.toUpperCase(),
                })
              }
              placeholder="AUGR"
            />
          </label>
          <label>
            Status
            <select
              value={searchParams.get('status') ?? ''}
              onChange={(event) =>
                updateAllocatorFilters({ status: event.target.value })
              }
            >
              <option value="">All statuses</option>
              {['queued', 'selected', 'rejected', 'expired', 'executed'].map(
                (status) => (
                  <option key={status} value={status}>
                    {status}
                  </option>
                ),
              )}
            </select>
          </label>
          <label>
            Market type
            <select
              value={searchParams.get('market_type') ?? ''}
              onChange={(event) =>
                updateAllocatorFilters({ market_type: event.target.value })
              }
            >
              <option value="">All markets</option>
              {marketTypes.map((market) => (
                <option key={market} value={market}>
                  {market}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() =>
              updateAllocatorFilters({
                ticker: '',
                status: '',
                market_type: '',
              })
            }
          >
            Clear filters
          </button>
        </form>
        {opportunitiesQuery.isLoading ? (
          <LoadingState label="Loading allocator opportunities…" />
        ) : null}
        {opportunitiesQuery.error ? (
          <ErrorState
            error={opportunitiesQuery.error}
            onRetry={() => void opportunitiesQuery.refetch()}
          />
        ) : null}
        {opportunitiesQuery.data && opportunities.length === 0 ? (
          <EmptyState
            title="No allocator opportunities"
            message="No allocator opportunities match these filters or the backend returned none."
          />
        ) : null}
        {opportunities.length > 0 ? (
          <OpportunityRows opportunities={opportunities} />
        ) : null}
        {opportunities.length > 0 ? (
          <nav
            className="pagination-controls"
            aria-label="Allocator opportunity pagination"
          >
            <button
              type="button"
              className="secondary-button"
              disabled={currentOpportunityOffset === 0}
              onClick={() =>
                setAllocatorOffset(
                  'opportunity_offset',
                  Math.max(0, currentOpportunityOffset - 10),
                )
              }
            >
              Previous
            </button>
            <span className="muted">
              Showing {currentOpportunityOffset + 1}–
              {currentOpportunityOffset + opportunities.length}{' '}
              {opportunityTotal === undefined
                ? 'total unavailable'
                : `of ${opportunityTotal}`}
            </span>
            <button
              type="button"
              className="secondary-button"
              disabled={
                opportunityTotal === undefined
                  ? opportunities.length < 10
                  : currentOpportunityOffset + 10 >= opportunityTotal
              }
              onClick={() =>
                setAllocatorOffset(
                  'opportunity_offset',
                  currentOpportunityOffset + 10,
                )
              }
            >
              Next
            </button>
          </nav>
        ) : null}
      </section>

      <section className="panel" aria-labelledby="decisions-heading">
        <div className="panel-header">
          <div>
            <h2 id="decisions-heading">Allocation decisions</h2>
            <p className="muted">
              Recent allocator decisions are read-only diagnostics. Created
              orders are linked in later slices.
            </p>
          </div>
          {decisionsQuery.data ? (
            <LastUpdated date={decisionsQuery.dataUpdatedAt} />
          ) : null}
        </div>
        <form
          className="filter-bar"
          aria-label="Allocation decision filters"
          onSubmit={(event) => event.preventDefault()}
        >
          <label>
            Mode
            <select
              value={searchParams.get('mode') ?? ''}
              onChange={(event) =>
                updateAllocatorFilters({ mode: event.target.value })
              }
            >
              <option value="">All modes</option>
              <option value="shadow">shadow</option>
              <option value="paper">paper</option>
            </select>
          </label>
          <label>
            Action
            <select
              value={searchParams.get('action') ?? ''}
              onChange={(event) =>
                updateAllocatorFilters({ action: event.target.value })
              }
            >
              <option value="">All actions</option>
              {[
                'shadow_selected',
                'shadow_rejected',
                'paper_order_intent',
                'execution_rejected',
                'executed',
              ].map((action) => (
                <option key={action} value={action}>
                  {action}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => updateAllocatorFilters({ mode: '', action: '' })}
          >
            Clear filters
          </button>
        </form>
        {decisionsQuery.isLoading ? (
          <LoadingState label="Loading allocation decisions…" />
        ) : null}
        {decisionsQuery.error ? (
          <ErrorState
            error={decisionsQuery.error}
            onRetry={() => void decisionsQuery.refetch()}
          />
        ) : null}
        {decisionsQuery.data && decisions.length === 0 ? (
          <EmptyState
            title="No allocation decisions"
            message="No allocation decisions match these filters or the backend returned none."
          />
        ) : null}
        {decisions.length > 0 ? <DecisionRows decisions={decisions} /> : null}
        {decisions.length > 0 ? (
          <nav
            className="pagination-controls"
            aria-label="Allocation decision pagination"
          >
            <button
              type="button"
              className="secondary-button"
              disabled={currentDecisionOffset === 0}
              onClick={() =>
                setAllocatorOffset(
                  'decision_offset',
                  Math.max(0, currentDecisionOffset - 10),
                )
              }
            >
              Previous
            </button>
            <span className="muted">
              Showing {currentDecisionOffset + 1}–
              {currentDecisionOffset + decisions.length}{' '}
              {decisionTotal === undefined
                ? 'total unavailable'
                : `of ${decisionTotal}`}
            </span>
            <button
              type="button"
              className="secondary-button"
              disabled={
                decisionTotal === undefined
                  ? decisions.length < 10
                  : currentDecisionOffset + 10 >= decisionTotal
              }
              onClick={() =>
                setAllocatorOffset(
                  'decision_offset',
                  currentDecisionOffset + 10,
                )
              }
            >
              Next
            </button>
          </nav>
        ) : null}
      </section>
    </div>
  )
}

export function PortfolioPage() {
  const { account } = useAccount()
  const accountId = account.id
  const [searchParams, setSearchParams] = useSearchParams()
  const realtime = useRealtime()
  const [realtimeStale, setRealtimeStale] = useState(false)
  const offset = Number(searchParams.get('offset') ?? '0')
  const activeTab =
    searchParams.get('tab') === 'allocator'
      ? 'allocator'
      : searchParams.get('tab') === 'ledger'
        ? 'ledger'
        : 'positions'
  const filters = useMemo(
    () => ({
      ticker: searchParams.get('ticker') || undefined,
      side: searchParams.get('side') || undefined,
      limit: pageSize,
      offset: Number.isFinite(offset) && offset > 0 ? offset : 0,
    }),
    [offset, searchParams],
  )
  const positionsQuery = useQuery({
    queryKey: queryKeys.portfolioOpenPositions(accountId, filters),
    queryFn: ({ signal }) =>
      getOpenPortfolioPositions(accountId, filters, signal),
  })
  const portfolioSummaryQuery = useQuery({
    queryKey: queryKeys.portfolioSummary(accountId),
    queryFn: ({ signal }) => getPortfolioSummary(accountId, signal),
  })
  const positions = positionsQuery.data?.data ?? []
  const total = positionsQuery.data?.total
  const currentOffset = filters.offset ?? 0
  const hasNext =
    total === undefined
      ? positions.length === pageSize
      : currentOffset + pageSize < total

  useEffect(() => {
    const latest = realtime.events[0]
    if (!latest) return
    if (latest.type === 'position_update' || latest.type === 'order_filled') {
      setRealtimeStale(true)
      void positionsQuery.refetch()
    }
  }, [realtime.events, positionsQuery])

  function updateFilters(updates: Record<string, string>) {
    const next = new URLSearchParams(searchParams)
    for (const [key, value] of Object.entries(updates)) {
      if (value) next.set(key, value)
      else next.delete(key)
    }
    next.delete('offset')
    setSearchParams(next)
  }

  function setOffset(nextOffset: number) {
    const next = new URLSearchParams(searchParams)
    if (nextOffset > 0) next.set('offset', String(nextOffset))
    else next.delete('offset')
    setSearchParams(next)
  }

  function setTab(tab: 'positions' | 'allocator' | 'ledger') {
    const next = new URLSearchParams(searchParams)
    if (tab === 'allocator' || tab === 'ledger') next.set('tab', tab)
    else next.delete('tab')
    setSearchParams(next)
  }

  return (
    <div className="detail-stack">
      <PageHeader
        eyebrow="Account portfolio"
        title="Portfolio"
        description={`Positions, allocator evidence, and capital activity for ${account.name}.`}
        actions={<span className="status-pill active">Account scoped</span>}
      />
      <Breadcrumbs
        items={[
          { label: 'Cockpit', to: `/accounts/${accountId}/cockpit` },
          { label: 'Portfolio' },
        ]}
      />
      <section className="panel">
        <StaleBanner
          show={
            realtimeStale ||
            realtime.status === 'disconnected' ||
            realtime.status === 'degraded'
          }
          message="Portfolio data may be stale after realtime position/order activity. Values are display-only."
        />
      </section>

      <div role="tablist" aria-label="Portfolio sections" className="tabs">
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'positions'}
          onClick={() => setTab('positions')}
        >
          Positions
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'allocator'}
          onClick={() => setTab('allocator')}
        >
          Allocator diagnostics
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'ledger'}
          onClick={() => setTab('ledger')}
        >
          Capital ledger
        </button>
      </div>

      {activeTab === 'allocator' ? (
        <AllocatorPanel
          searchParams={searchParams}
          setSearchParams={setSearchParams}
        />
      ) : null}

      {activeTab === 'ledger' ? (
        <CapitalLedgerPanel accountId={accountId} />
      ) : null}

      {activeTab === 'positions' ? (
        <>
          <section
            className="panel"
            aria-labelledby="portfolio-valuation-heading"
          >
            <div className="panel-header">
              <div>
                <p className="eyebrow">Canonical valuation</p>
                <h2 id="portfolio-valuation-heading">
                  Account capital summary
                </h2>
                <p className="muted">
                  Account-scoped marks and reconciliation evidence. No legacy
                  unscoped rows are included.
                </p>
              </div>
              {portfolioSummaryQuery.data ? (
                <LastUpdated date={portfolioSummaryQuery.data.generated_at} />
              ) : null}
            </div>
            {portfolioSummaryQuery.isLoading ? (
              <LoadingState label="Loading account valuation…" />
            ) : null}
            {portfolioSummaryQuery.error ? (
              <ErrorState
                error={portfolioSummaryQuery.error}
                onRetry={() => void portfolioSummaryQuery.refetch()}
              />
            ) : null}
            {portfolioSummaryQuery.data ? (
              <div className="metrics-grid">
                <div className="panel nested-panel">
                  <span>Market value</span>
                  <strong>
                    {money(portfolioSummaryQuery.data.market_value)}
                  </strong>
                </div>
                <div className="panel nested-panel">
                  <span>Unrealized P/L</span>
                  <strong>
                    {money(portfolioSummaryQuery.data.unrealized_pnl)}
                  </strong>
                </div>
                <div className="panel nested-panel">
                  <span>Realized P/L</span>
                  <strong>
                    {money(portfolioSummaryQuery.data.realized_pnl)}
                  </strong>
                </div>
                <div className="panel nested-panel">
                  <span>Total P/L</span>
                  <strong>{money(portfolioSummaryQuery.data.total_pnl)}</strong>
                </div>
                <div className="panel nested-panel">
                  <span>Mark coverage</span>
                  <strong>
                    {portfolioSummaryQuery.data.mark_coverage_complete
                      ? 'Complete'
                      : `${portfolioSummaryQuery.data.unmarked_positions} unmarked`}
                  </strong>
                </div>
                <div className="panel nested-panel">
                  <span>Reconciliation</span>
                  <strong>
                    {portfolioSummaryQuery.data.reconciliation_passed
                      ? 'Passed'
                      : 'Not passed'}
                  </strong>
                </div>
              </div>
            ) : null}
          </section>
          <section className="panel" aria-labelledby="open-positions-heading">
            <div className="panel-header">
              <div>
                <p className="eyebrow">Exposure ledger</p>
                <h2 id="open-positions-heading">Open positions</h2>
                <p className="muted">
                  Inspect current quantity, entry value, and mark-to-market
                  exposure.
                </p>
                {realtimeStale ? (
                  <Alert variant="warning">Data may be stale</Alert>
                ) : null}
              </div>
              <div className="panel-actions">
                {positionsQuery.data ? (
                  <LastUpdated date={positionsQuery.dataUpdatedAt} />
                ) : null}
                <button
                  type="button"
                  className="secondary-button"
                  onClick={() => {
                    void positionsQuery.refetch()
                    setRealtimeStale(false)
                  }}
                  aria-label="Refresh portfolio data"
                >
                  <RefreshCw size={16} /> Refresh
                </button>
              </div>
            </div>
            <form
              className="filter-bar"
              aria-label="Position filters"
              onSubmit={(event) => event.preventDefault()}
              style={{ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }}
            >
              <label>
                Ticker
                <input
                  value={searchParams.get('ticker') ?? ''}
                  onChange={(event) =>
                    updateFilters({ ticker: event.target.value.toUpperCase() })
                  }
                  placeholder="AUGR"
                />
              </label>
              <label>
                Side
                <select
                  value={searchParams.get('side') ?? ''}
                  onChange={(event) =>
                    updateFilters({ side: event.target.value })
                  }
                >
                  <option value="">All</option>
                  <option value="long">Long</option>
                  <option value="short">Short</option>
                </select>
              </label>
              <button
                type="button"
                onClick={() => updateFilters({ ticker: '', side: '' })}
              >
                Clear filters
              </button>
            </form>
            {positionsQuery.isLoading ? (
              <LoadingState label="Loading open positions…" />
            ) : null}
            {positionsQuery.error ? (
              <ErrorState
                error={positionsQuery.error}
                onRetry={() => void positionsQuery.refetch()}
              />
            ) : null}
            {positionsQuery.data && positions.length === 0 ? (
              <EmptyState
                title="No open positions"
                message="No open positions match these filters."
              />
            ) : null}
            {positions.length > 0 ? (
              <PositionRows positions={positions} />
            ) : null}
            {positions.length > 0 ? (
              <nav
                className="pagination-controls"
                aria-label="Position pagination"
              >
                <button
                  type="button"
                  className="secondary-button"
                  disabled={currentOffset === 0}
                  onClick={() =>
                    setOffset(Math.max(0, currentOffset - pageSize))
                  }
                >
                  Previous
                </button>
                <span className="muted">
                  Showing {currentOffset + 1}–{currentOffset + positions.length}{' '}
                  {total === undefined ? 'total unavailable' : `of ${total}`}
                </span>
                <button
                  type="button"
                  className="secondary-button"
                  disabled={!hasNext}
                  onClick={() => setOffset(currentOffset + pageSize)}
                >
                  Next
                </button>
              </nav>
            ) : null}
          </section>
        </>
      ) : null}
    </div>
  )
}

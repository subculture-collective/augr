import { useQuery } from '@tanstack/react-query'

import { Alert } from '@/components/ui/alert'
import { PageHeader } from '@/components/ui/page-header'
import { useAccount } from '@/shared/account/AccountProvider'
import { getRiskCockpit, getRiskStatus } from '@/shared/api/endpoints'
import { Breadcrumbs } from '@/shared/components/EntityLinks'
import { EmptyState, ErrorState, LastUpdated, LoadingState, StaleBanner } from '@/shared/components/QueryStates'
import { queryKeys } from '@/shared/query/keys'
import { useRealtime } from '@/shared/websocket/RealtimeProvider'

function display(value: string) { return value.replaceAll('_', ' ') }

export function RiskPage() {
  const { account, cockpitPath } = useAccount()
  const realtime = useRealtime()
  const status = useQuery({ queryKey: queryKeys.riskStatus(account.id), queryFn: ({ signal }) => getRiskStatus(account.id, signal) })
  const cockpit = useQuery({ queryKey: queryKeys.riskCockpit(account.id), queryFn: ({ signal }) => getRiskCockpit(account.id, signal) })
  const stale = status.isStale || cockpit.isStale || realtime.status === 'disconnected' || realtime.status === 'degraded'

  return <div className="detail-stack">
    <Breadcrumbs items={[{ label: 'Cockpit', to: cockpitPath }, { label: 'Risk' }]} />
    <PageHeader eyebrow="Account evidence" title="Risk" description={`Risk status and decision evidence for ${account.name}. Global safety controls are intentionally separate.`} />
    <StaleBanner show={stale} message="Account risk evidence may be stale. Refresh before using it for an operational decision." />
    <section className="panel">
      <div className="panel-header"><h2>Risk status</h2>{status.data ? <LastUpdated date={status.data.updated_at} /> : null}</div>
      {status.isLoading ? <LoadingState label="Loading account risk status…" /> : null}
      {status.error ? <ErrorState error={status.error} onRetry={() => void status.refetch()} /> : null}
      {status.data ? <dl className="kv-grid"><dt>Status</dt><dd><span className={`status-pill ${status.data.risk_status}`}>{display(status.data.risk_status)}</span></dd><dt>Circuit breaker</dt><dd>{display(status.data.circuit_breaker.state)}</dd><dt>Kill switch evidence</dt><dd>{status.data.kill_switch.active ? 'Active' : 'Inactive'}</dd><dt>Open positions</dt><dd>{status.data.position_limits.current_open_positions ?? 'Unavailable'} / {status.data.position_limits.max_concurrent}</dd><dt>Total exposure</dt><dd>{status.data.position_limits.current_total_exposure_pct === undefined ? 'Unavailable' : `${(status.data.position_limits.current_total_exposure_pct * 100).toFixed(2)}%`}</dd></dl> : null}
    </section>
    <section className="panel">
      <div className="panel-header"><h2>Risk cockpit evidence</h2>{cockpit.data ? <LastUpdated date={cockpit.data.generated_at} /> : null}</div>
      {cockpit.isLoading ? <LoadingState label="Loading account risk evidence…" /> : null}
      {cockpit.error ? <ErrorState error={cockpit.error} onRetry={() => void cockpit.refetch()} /> : null}
      {cockpit.data?.warnings.length ? <Alert variant="warning">{cockpit.data.warnings.join(', ')}</Alert> : null}
      {cockpit.data && cockpit.data.exposures.length === 0 ? <EmptyState title="No risk exposures" message="No decisions are present in the selected account window." /> : null}
      {cockpit.data?.exposures.length ? <div className="table-wrap"><table aria-label="Risk exposures"><thead><tr><th>Market</th><th>Approved</th><th>Rejected</th><th>Net expected value</th></tr></thead><tbody>{cockpit.data.exposures.map((exposure) => <tr key={exposure.market_type}><td>{display(exposure.market_type)}</td><td>{exposure.approved_decisions}</td><td>{exposure.rejected_decisions}</td><td>{exposure.net_expected_value}</td></tr>)}</tbody></table></div> : null}
    </section>
  </div>
}

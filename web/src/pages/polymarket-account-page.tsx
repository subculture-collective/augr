import { useMemo } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { usePolymarketAccount, usePolymarketAccountTrades, usePolymarketJobsStatus, useSetPolymarketAccountTracked } from '@/hooks/use-polymarket'
import { ApiClientError } from '@/lib/api/client'
import type { JobStatus } from '@/lib/api/types'

function formatRelativeTime(iso?: string): string {
  if (!iso) return '--'
  const diff = Date.now() - new Date(iso).getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })
const marketUrl = (slug: string) => `https://polymarket.com/market/${encodeURIComponent(slug)}`
const eventUrl = (slug: string) => `https://polymarket.com/event/${encodeURIComponent(slug)}`
const profileUrl = (address: string) => `https://polymarket.com/profile/${encodeURIComponent(address)}`

function isApiClientError(error: unknown): error is ApiClientError {
  return error instanceof ApiClientError
}

function isNotImplementedError(error: unknown): boolean {
  return isApiClientError(error) && (error.status === 501 || error.status === 503) && error.code === 'ERR_NOT_IMPLEMENTED'
}

function isNotFoundError(error: unknown): boolean {
  return isApiClientError(error) && error.status === 404
}

function jobTone(job?: JobStatus) {
  if (!job) return 'secondary' as const
  if (!job.enabled) return 'warning' as const
  if (job.running) return 'success' as const
  if (job.last_error || job.last_result?.startsWith('error')) return 'destructive' as const
  if (!job.last_run) return 'secondary' as const
  return 'success' as const
}

function jobLabel(job?: JobStatus) {
  if (!job) return 'not configured'
  if (!job.enabled) return 'disabled'
  if (job.running) return 'running'
  if (job.last_error || job.last_result?.startsWith('error')) return 'failed'
  if (!job.last_run) return 'never run'
  return 'ok'
}

function PolymarketContextRail({ address }: { address?: string }) {
  return (
    <nav aria-label="Polymarket links" className="hud-panel rounded-none px-3 py-2.5">
      <div className="flex flex-wrap gap-2">
        <Link className="inline-flex items-center border border-border bg-panel px-3 py-1.5 text-[11px] font-medium uppercase tracking-[0.14em] text-ink-dim transition-colors hover:border-border-strong hover:bg-panel-raised hover:text-ink" to="/polymarket">
          Hub
        </Link>
        <Link className="inline-flex items-center border border-border bg-panel px-3 py-1.5 text-[11px] font-medium uppercase tracking-[0.14em] text-ink-dim transition-colors hover:border-border-strong hover:bg-panel-raised hover:text-ink" to="/surfers/ops">
          Surfers Ops
        </Link>
        {address ? (
          <a
            className="inline-flex items-center border border-border bg-panel px-3 py-1.5 text-[11px] font-medium uppercase tracking-[0.14em] text-ink-dim transition-colors hover:border-border-strong hover:bg-panel-raised hover:text-ink"
            href={profileUrl(address)}
            target="_blank"
            rel="noreferrer"
          >
            Polymarket profile
          </a>
        ) : null}
      </div>
    </nav>
  )
}

function JobStatusCard({ job }: { job?: JobStatus }) {
  return (
    <div className="rounded-md border border-border bg-card px-4 py-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="font-mono text-xs uppercase tracking-[0.16em] text-muted-foreground">{job?.name ?? 'job missing'}</div>
          <div className="mt-1 text-sm font-medium">{job?.description ?? 'This job is not exposed on this deployment.'}</div>
        </div>
        <Badge variant={jobTone(job)}>{jobLabel(job)}</Badge>
      </div>
      <div className="mt-3 grid gap-2 text-xs text-muted-foreground sm:grid-cols-3">
        <div>
          <div className="font-mono uppercase tracking-[0.16em]">schedule</div>
          <div>{job?.schedule ?? '--'}</div>
        </div>
        <div>
          <div className="font-mono uppercase tracking-[0.16em]">last run</div>
          <div>{job?.last_run ? formatRelativeTime(job.last_run) : '--'}</div>
        </div>
        <div>
          <div className="font-mono uppercase tracking-[0.16em]">result</div>
          <div className={job?.last_error || job?.last_result?.startsWith('error') ? 'text-destructive' : ''}>
            {job?.last_error ?? job?.last_result ?? '--'}
          </div>
        </div>
      </div>
    </div>
  )
}

function IngestionStateCard({
  title,
  body,
  jobs,
}: {
  title: string
  body: string
  jobs: Array<JobStatus | undefined>
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-sm text-muted-foreground">{body}</p>
        <div className="space-y-3">
          {jobs.map((job, index) => (
            <JobStatusCard key={job?.name ?? index} job={job} />
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

export function PolymarketAccountPage() {
  const { address } = useParams<{ address: string }>()
  const qc = useQueryClient()
  const tradeWindow = useMemo(() => {
    const to = new Date()
    return {
      from: new Date(to.getTime() - 30 * 24 * 60 * 60 * 1000).toISOString(),
      to: to.toISOString(),
      limit: 100,
    }
  }, [])
  const account = usePolymarketAccount(address)
  const trades = usePolymarketAccountTrades(address, tradeWindow)
  const jobs = usePolymarketJobsStatus()
  const track = useSetPolymarketAccountTracked()
  const data = account.data
  const tradesData = trades.data?.data ?? []

  const jobsByName = useMemo(() => new Map((jobs.data ?? []).map((job) => [job.name, job])), [jobs.data])
  const profileJob = jobsByName.get('polymarket_profiles')
  const resolutionJob = jobsByName.get('polymarket_resolutions')

  const stats = useMemo(() => [
    ['total_volume', money.format(data?.total_volume ?? 0)],
    ['total_trades', String(data?.total_trades ?? 0)],
    ['markets_entered', String(data?.markets_entered ?? 0)],
    ['W/L', `${data?.markets_won ?? 0}/${data?.markets_lost ?? 0}`],
    ['resolved', String(data?.resolved_markets ?? 0)],
    ['win_rate', `${((data?.win_rate ?? 0) * 100).toFixed(1)}%`],
    ['adj_win_rate', `${((data?.bayesian_win_rate ?? 0) * 100).toFixed(1)}%`],
    ['consistency', (data?.consistency_score ?? 0).toFixed(3)],
    ['max_position', money.format(data?.max_position ?? 0)],
    ['early_entry_rate', `${((data?.early_entry_rate ?? 0) * 100).toFixed(1)}%`],
    ['last_active', formatRelativeTime(data?.last_active)],
  ], [data])

  const accountNotConfigured = isNotImplementedError(account.error)
  const accountNotFound = isNotFoundError(account.error)
  const tradesNotConfigured = isNotImplementedError(trades.error)
  const jobsNotConfigured = isNotImplementedError(jobs.error)
  const noTradesYet = !trades.isLoading && !trades.isError && tradesData.length === 0
  const showEmptyTradesState = !!data && noTradesYet

  return (
    <div className="space-y-4" data-testid="polymarket-account-page">
      <PageHeader
        title={address ?? ''}
        description="Polymarket account detail"
        actions={
          <div className="flex gap-2">
            <Button size="sm" variant="outline" asChild>
              <Link to="/polymarket">Hub</Link>
            </Button>
            <Button size="sm" variant="outline" asChild>
              <Link to="/surfers/ops">Surfers Ops</Link>
            </Button>
            <Button size="sm" variant="outline" onClick={() => void navigator.clipboard.writeText(address ?? '')}>Copy</Button>
            {address ? (
              <Button size="sm" variant="outline" asChild>
                <a href={profileUrl(address)} target="_blank" rel="noreferrer">Polymarket profile</a>
              </Button>
            ) : null}
          </div>
        }
      />

      <PolymarketContextRail address={address} />

      {account.isLoading && (
        <Card>
          <CardContent className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
            Loading account profile...
          </CardContent>
        </Card>
      )}

      {accountNotConfigured && (
        <Card>
          <CardHeader>
            <CardTitle className="text-destructive">Polymarket profiles API unavailable</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm text-muted-foreground">
            <p>This deployment does not expose the Polymarket account/profile repository, so wallet stats and trades cannot load.</p>
            <p>Enable the Polymarket account repository before this page can show profile or trade data.</p>
          </CardContent>
        </Card>
      )}

      {accountNotFound && !accountNotConfigured && (
        <IngestionStateCard
          title="No local Polymarket profile yet"
          body="We do not have an ingested profile for this wallet. The profile and resolution jobs need to run before wallet trades can appear here."
          jobs={[profileJob, resolutionJob].filter(Boolean) as JobStatus[]}
        />
      )}

      {account.isError && !accountNotFound && !accountNotConfigured && (
        <Card>
          <CardHeader>
            <CardTitle className="text-destructive">Failed to load Polymarket account</CardTitle>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            <p>{isApiClientError(account.error) ? account.error.message : 'An unexpected error occurred while loading this wallet.'}</p>
          </CardContent>
        </Card>
      )}

      {data && !account.isError && (
        <>
          <div className="flex gap-2">
            <Badge variant={data.tracked ? 'success' : 'secondary'}>{data.tracked ? 'tracked' : 'untracked'}</Badge>
            <input
              type="checkbox"
              checked={data.tracked}
              onChange={(e) => address && track.mutate({ address, tracked: e.target.checked }, { onSuccess: () => qc.invalidateQueries({ queryKey: ['polymarket-account', address] }) })}
            />
          </div>

          <div className="grid gap-3 md:grid-cols-4">
            {stats.map(([k, v]) => (
              <Card key={k}>
                <CardContent>
                  <div className="text-xs uppercase text-muted-foreground">{k}</div>
                  <div>{v}</div>
                </CardContent>
              </Card>
            ))}
          </div>

          {trades.isLoading && (
            <Card>
              <CardContent className="py-6 text-sm text-muted-foreground">Loading wallet trades...</CardContent>
            </Card>
          )}

          {showEmptyTradesState && !trades.isLoading && (
            <IngestionStateCard
              title="No wallet trades ingested yet"
              body={data.total_trades === 0
                ? 'This wallet has no ingested trades yet. If you expected rows, confirm the Polymarket profile and resolution jobs are enabled and have completed at least once.'
                : 'This wallet has no trades in the last 30 days. If you expected rows, check the ingestion jobs and the current time window.'}
              jobs={jobsNotConfigured ? [] : ([profileJob, resolutionJob].filter(Boolean) as JobStatus[])}
            />
          )}

          {trades.isError && (
            <Card>
              <CardHeader>
                <CardTitle className="text-destructive">Failed to load wallet trades</CardTitle>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                <p>{tradesNotConfigured ? 'This deployment does not expose the Polymarket trade repository.' : isApiClientError(trades.error) ? trades.error.message : 'An unexpected error occurred while loading trade rows.'}</p>
              </CardContent>
            </Card>
          )}

          {!trades.isError && !showEmptyTradesState && (
            <Card>
              <CardHeader>
                <CardTitle>Trades</CardTitle>
              </CardHeader>
              <CardContent>
                <table className="w-full text-sm">
                  <tbody>
                    {tradesData.map((t) => (
                      <tr key={t.id}>
                        <td>{new Date(t.timestamp).toLocaleString()}</td>
                        <td>
                          <div className="flex flex-col">
                            <a href={marketUrl(t.market_slug)} target="_blank" rel="noreferrer">{t.market_slug}</a>
                            <a className="text-xs text-muted-foreground" href={eventUrl(t.market_slug)} target="_blank" rel="noreferrer">event</a>
                          </div>
                        </td>
                        <td><Badge>{t.side}</Badge></td>
                        <td>{t.action}</td>
                        <td>{t.price.toFixed(3)}</td>
                        <td>{money.format(t.size_usdc)}</td>
                        <td>{t.pnl == null ? '--' : <span className={t.pnl >= 0 ? 'text-emerald-500' : 'text-red-500'}>{money.format(t.pnl)}</span>}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </CardContent>
            </Card>
          )}
        </>
      )}

      <Link to="/polymarket">Back to Polymarket</Link>
    </div>
  )
}

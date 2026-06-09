import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Search } from 'lucide-react'
import { Link } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ConsolePanel, HudBadge, HudRow, HudSection, StatusLed } from '@/components/ui/hud'
import { Input } from '@/components/ui/input'
import { apiClient } from '@/lib/api/client'
import { useAddPolymarketWatched, usePolymarketAccounts, usePolymarketDiscoveryLast, usePolymarketJobsStatus, usePolymarketRecentSignals, usePolymarketRecentTrades, usePolymarketWatched, useRemovePolymarketWatched, useRunPolymarketDiscovery, useSetPolymarketAccountTracked, useSetPolymarketWatchedEnabled } from '@/hooks/use-polymarket'
import { useWebSocketClient } from '@/hooks/use-websocket-client'
import type { ResearchOpportunity, TradeDecisionRiskStatus, TradeDecisionStatus } from '@/lib/api/types'

function formatRelativeTime(iso?: string): string {
  if (!iso) return '--'
  const delta = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(delta)) return '--'
  const s = Math.floor(delta / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  return h < 24 ? `${h}h ago` : `${Math.floor(h / 24)}d ago`
}

function safeDateLabel(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

function safeMoneyLabel(value: unknown) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : Number.NaN
  if (!Number.isFinite(parsed)) return '—'
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 2 }).format(parsed)
}

function safeNumberLabel(value: unknown, maximumFractionDigits = 0) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : Number.NaN
  if (!Number.isFinite(parsed)) return '—'
  return new Intl.NumberFormat('en-US', { maximumFractionDigits }).format(parsed)
}

function splitReasons(reasons?: string[] | null) {
  if (!Array.isArray(reasons) || reasons.length === 0) return []
  return reasons.map((reason) => reason.trim()).filter(Boolean)
}

const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 0 })
const money2 = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 2 })
const shortAddress = (address: string) => `${address.slice(0, 6)}…${address.slice(-4)}`
const marketUrl = (slug: string) => `https://polymarket.com/market/${encodeURIComponent(slug)}`
const eventUrl = (slug: string) => `https://polymarket.com/event/${encodeURIComponent(slug)}`
const profileUrl = (address: string) => `https://polymarket.com/profile/${encodeURIComponent(address)}`
type AccountSort = 'consistency_score' | 'bayesian_win_rate' | 'resolved_markets' | 'win_rate' | 'volume' | 'last_active' | 'trades'

const decisionStatusVariants: Record<TradeDecisionStatus, 'secondary' | 'outline' | 'success' | 'warning' | 'destructive'> = {
  candidate: 'outline',
  rejected: 'destructive',
  paper_ordered: 'warning',
  live_ordered: 'success',
  closed: 'secondary',
}

const riskStatusVariants: Record<TradeDecisionRiskStatus, 'success' | 'destructive'> = {
  approved: 'success',
  rejected: 'destructive',
}

function parseOptionalNumber(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  return Number.isFinite(parsed) ? parsed : undefined
}

function ScannerOpportunityCard({ opportunity }: { opportunity: ResearchOpportunity }) {
  const decision = opportunity.decision
  const reasons = splitReasons(opportunity.reasons ?? decision.risk_reasons)

  return (
    <article className="border border-border bg-panel p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1">
          <div className="font-mono text-xs text-muted-foreground break-all">{decision.instrument_key || '—'}</div>
          <div className="flex flex-wrap gap-1.5">
            <Badge variant={riskStatusVariants[decision.risk_status] ?? 'secondary'}>{decision.risk_status}</Badge>
            <Badge variant={decisionStatusVariants[decision.status]}>{decision.status}</Badge>
            {opportunity.accepted == null ? null : <Badge variant={opportunity.accepted ? 'success' : 'destructive'}>{opportunity.accepted ? 'accepted' : 'rejected'}</Badge>}
          </div>
        </div>
        <div className={decision.net_ev > 0 ? 'text-right font-semibold text-emerald-500' : decision.net_ev < 0 ? 'text-right font-semibold text-destructive' : 'text-right font-semibold text-muted-foreground'}>
          {safeMoneyLabel(decision.net_ev)}
          <div className="mt-1 text-xs font-normal text-muted-foreground">Net EV</div>
        </div>
      </div>

      <div className="mt-4 grid gap-3 text-sm sm:grid-cols-2">
        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Approved size</div>
          <div className="mt-1 font-medium">{safeNumberLabel(decision.approved_size, 0)}</div>
        </div>
        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Risk status</div>
          <div className="mt-1 font-medium">{decision.risk_status}</div>
        </div>
        <div className="sm:col-span-2">
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Risk reasons</div>
          <div className="mt-1 text-muted-foreground">{reasons.length > 0 ? reasons.join(' · ') : 'No risk reasons recorded'}</div>
        </div>
      </div>

      <div className="mt-4 flex items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>Updated {safeDateLabel(decision.updated_at)}</span>
        <Link to="/journal" className="text-primary underline-offset-4 hover:underline">
          Journal
        </Link>
      </div>
    </article>
  )
}

export function PolymarketPage() {
  const qc = useQueryClient()
  const [slug, setSlug] = useState('')
  const [err, setErr] = useState('')
  const [trackedOnly, setTrackedOnly] = useState(true)
  const [minWinRate, setMinWinRate] = useState('')
  const [minResolved, setMinResolved] = useState('5')
  const [sort, setSort] = useState<AccountSort>('consistency_score')
  const [offset, setOffset] = useState(0)

  const [scannerSlugDraft, setScannerSlugDraft] = useState('')
  const [scannerTokenIdDraft, setScannerTokenIdDraft] = useState('')
  const [scannerOutcomeDraft, setScannerOutcomeDraft] = useState('')
  const [scannerProbabilityDraft, setScannerProbabilityDraft] = useState('')
  const [scannerBestBidDraft, setScannerBestBidDraft] = useState('')
  const [scannerBestAskDraft, setScannerBestAskDraft] = useState('')
  const [scannerAskDepthUsdDraft, setScannerAskDepthUsdDraft] = useState('')
  const [scannerAskSizeDraft, setScannerAskSizeDraft] = useState('')
  const [scannerStrategyIdDraft, setScannerStrategyIdDraft] = useState('')

  const [scannerSlug, setScannerSlug] = useState('')
  const [scannerTokenId, setScannerTokenId] = useState('')
  const [scannerOutcome, setScannerOutcome] = useState('')
  const [scannerProbability, setScannerProbability] = useState<number | undefined>()
  const [scannerBestBid, setScannerBestBid] = useState<number | undefined>()
  const [scannerBestAsk, setScannerBestAsk] = useState<number | undefined>()
  const [scannerAskDepthUsd, setScannerAskDepthUsd] = useState<number | undefined>()
  const [scannerAskSize, setScannerAskSize] = useState<number | undefined>()
  const [scannerStrategyId, setScannerStrategyId] = useState('')

  const accounts = usePolymarketAccounts({
    tracked: trackedOnly || undefined,
    min_win_rate: minWinRate ? Number(minWinRate) : undefined,
    min_resolved: minResolved ? Number(minResolved) : undefined,
    sort,
    limit: 25,
    offset,
  })
  const watched = usePolymarketWatched()
  const jobs = usePolymarketJobsStatus()
  const trades = usePolymarketRecentTrades(50)
  const signals = usePolymarketRecentSignals({ limit: 25 })
  const tracked = usePolymarketAccounts({ tracked: true, limit: 1000 })
  const strategies = useQuery({
    queryKey: ['strategies', { market_type: 'polymarket' }],
    queryFn: () => apiClient.listStrategies({ market_type: 'polymarket' }),
  })
  const add = useAddPolymarketWatched()
  const remove = useRemovePolymarketWatched()
  const enable = useSetPolymarketWatchedEnabled()
  const track = useSetPolymarketAccountTracked()
  const discoveryLast = usePolymarketDiscoveryLast()
  const runDiscovery = useRunPolymarketDiscovery()

  const commitScanner = () => {
    setScannerSlug(scannerSlugDraft.trim())
    setScannerTokenId(scannerTokenIdDraft.trim())
    setScannerOutcome(scannerOutcomeDraft.trim())
    setScannerProbability(parseOptionalNumber(scannerProbabilityDraft))
    setScannerBestBid(parseOptionalNumber(scannerBestBidDraft))
    setScannerBestAsk(parseOptionalNumber(scannerBestAskDraft))
    setScannerAskDepthUsd(parseOptionalNumber(scannerAskDepthUsdDraft))
    setScannerAskSize(parseOptionalNumber(scannerAskSizeDraft))
    setScannerStrategyId(scannerStrategyIdDraft.trim())
  }

  const hasScannerInputs = Boolean(
    scannerSlug ||
      scannerTokenId ||
      scannerOutcome ||
      scannerProbability != null ||
      scannerBestBid != null ||
      scannerBestAsk != null ||
      scannerAskDepthUsd != null ||
      scannerAskSize != null ||
      scannerStrategyId,
  )

  const scannerQuery = useQuery({
    queryKey: [
      'polymarket-opportunities',
      scannerSlug,
      scannerTokenId,
      scannerOutcome,
      scannerProbability,
      scannerBestBid,
      scannerBestAsk,
      scannerAskDepthUsd,
      scannerAskSize,
      scannerStrategyId,
    ],
    queryFn: () =>
      apiClient.listPolymarketOpportunities({
        slug: scannerSlug || undefined,
        token_id: scannerTokenId || undefined,
        outcome: scannerOutcome || undefined,
        probability: scannerProbability,
        best_bid: scannerBestBid,
        best_ask: scannerBestAsk,
        ask_depth_usd: scannerAskDepthUsd,
        ask_size: scannerAskSize,
        strategy_id: scannerStrategyId || undefined,
        limit: 12,
      }),
    enabled: hasScannerInputs,
  })

  const ws = useWebSocketClient({
    enabled: true,
    onMessage: (m) => {
      if ('type' in m && (m.type === 'polymarket_whale_trade' || m.type === 'polymarket_price_move' || m.type === 'polymarket_account_tracked')) {
        qc.invalidateQueries({ queryKey: ['polymarket-accounts'] })
        qc.invalidateQueries({ queryKey: ['polymarket-account'] })
        qc.invalidateQueries({ queryKey: ['polymarket-recent-trades'] })
        qc.invalidateQueries({ queryKey: ['polymarket-recent-signals'] })
        qc.invalidateQueries({ queryKey: ['polymarket-watched'] })
      }
    },
  })

  const { status: wsStatus, sendCommand } = ws
  useEffect(() => {
    if (wsStatus === 'open') sendCommand({ action: 'subscribe_polymarket' })
  }, [sendCommand, wsStatus])

  const totalVolume = useMemo(() => (tracked.data?.data ?? []).reduce((sum, account) => sum + account.total_volume, 0), [tracked.data])
  const displayedTrackedCount = accounts.data?.data?.length ?? 0
  const doAdd = async () => {
    setErr('')
    try {
      await add.mutateAsync({ slug: slug.trim() })
      setSlug('')
    } catch (error) {
      setErr(error instanceof Error ? error.message : 'Failed')
    }
  }
  const accountsData = accounts.data?.data ?? []
  const watchedData = watched.data?.data ?? []
  const signalsData = signals.data?.data ?? []
  const jobsData = jobs.data ?? []
  const strategiesData = (strategies.data?.data ?? []).filter((strategy) => strategy.market_type === 'polymarket')
  const scannerOpps = scannerQuery.data?.data ?? []

  const scannerStatus = !hasScannerInputs
    ? 'Scanner not configured'
    : scannerQuery.isLoading
      ? 'Loading opportunities…'
      : scannerQuery.isError
        ? (scannerQuery.error as { status?: number } | null | undefined)?.status === 501
          ? 'Scanner not configured'
          : 'Unable to load opportunities'
        : scannerOpps.length === 0
          ? 'No opportunities met the paper-first filters.'
          : `${scannerOpps.length} opportunities`

  return (
    <div className="space-y-5" data-testid="polymarket-page">
      <PageHeader
        title="Polymarket"
        description="Prediction market intelligence: tracked wallets, whale trades, watched markets"
        meta={<StatusLed state={ws.status === 'open' ? 'live' : ws.status === 'connecting' ? 'sync' : 'warn'} label={scannerStatus} />}
      />

      <ConsolePanel className="space-y-4 p-4">
        <HudSection label="Summary" note="Wallets, volume, strategies, and whale signals" />
        <div className="grid gap-3 md:grid-cols-4">
          <HudRow label="Displayed wallets" value={displayedTrackedCount} />
          <HudRow label="Total volume" value={money.format(totalVolume)} />
          <HudRow label="Open strategies" value={strategiesData.length} />
          <HudRow label="Whale signals" value={signalsData.filter((signal) => signal.source === 'polymarket-whale').length} />
        </div>
      </ConsolePanel>

      <ConsolePanel className="space-y-4 p-4">
        <HudSection label="Research scanner" note="Paper-first market scanning and opportunity review" />
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <HudBadge tone={hasScannerInputs ? 'confirm' : 'caution'}>{scannerStatus}</HudBadge>
          {scannerSlug ? <span>· {scannerSlug}</span> : null}
          {scannerTokenId ? <span>· token {scannerTokenId}</span> : null}
          {scannerOutcome ? <span>· {scannerOutcome}</span> : null}
        </div>
          <form
            className="grid gap-3 lg:grid-cols-2 xl:grid-cols-4"
            onSubmit={(event) => {
              event.preventDefault()
              commitScanner()
            }}
          >
            <Input value={scannerSlugDraft} onChange={(event) => setScannerSlugDraft(event.target.value)} placeholder="slug" aria-label="Market slug" />
            <Input value={scannerTokenIdDraft} onChange={(event) => setScannerTokenIdDraft(event.target.value)} placeholder="token id" aria-label="Token ID" />
            <Input value={scannerOutcomeDraft} onChange={(event) => setScannerOutcomeDraft(event.target.value)} placeholder="outcome" aria-label="Outcome" />
            <Input value={scannerStrategyIdDraft} onChange={(event) => setScannerStrategyIdDraft(event.target.value)} placeholder="strategy id" aria-label="Strategy ID" />
            <Input value={scannerProbabilityDraft} onChange={(event) => setScannerProbabilityDraft(event.target.value)} inputMode="decimal" placeholder="probability" aria-label="Probability" />
            <Input value={scannerBestBidDraft} onChange={(event) => setScannerBestBidDraft(event.target.value)} inputMode="decimal" placeholder="best bid" aria-label="Best bid" />
            <Input value={scannerBestAskDraft} onChange={(event) => setScannerBestAskDraft(event.target.value)} inputMode="decimal" placeholder="best ask" aria-label="Best ask" />
            <Input value={scannerAskDepthUsdDraft} onChange={(event) => setScannerAskDepthUsdDraft(event.target.value)} inputMode="decimal" placeholder="ask depth usd" aria-label="Ask depth USD" />
            <Input value={scannerAskSizeDraft} onChange={(event) => setScannerAskSizeDraft(event.target.value)} inputMode="decimal" placeholder="ask size" aria-label="Ask size" />
            <div className="flex items-end">
              <Button type="submit" className="w-full">Scan</Button>
            </div>
          </form>

        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          {scannerProbability != null ? <span>· p={scannerProbability}</span> : null}
          {scannerBestBid != null ? <span>· bid={scannerBestBid}</span> : null}
          {scannerBestAsk != null ? <span>· ask={scannerBestAsk}</span> : null}
          {scannerAskDepthUsd != null ? <span>· depth=${scannerAskDepthUsd}</span> : null}
          {scannerAskSize != null ? <span>· size={scannerAskSize}</span> : null}
        </div>

          {!hasScannerInputs ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center">
              <Search className="size-8 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">Scanner not configured.</p>
            </div>
          ) : scannerQuery.isLoading ? (
            <div className="space-y-3" data-testid="polymarket-opportunities-loading">
              {Array.from({ length: 3 }).map((_, index) => (
                <div key={index} className="h-20 animate-pulse rounded-none border border-border bg-muted/40" />
              ))}
            </div>
          ) : scannerQuery.isError ? (
            (scannerQuery.error as { status?: number } | null | undefined)?.status === 501 ? (
              <div className="flex flex-col items-center gap-2 py-8 text-center">
                <Search className="size-8 text-muted-foreground" />
                <p className="text-sm text-muted-foreground">Scanner not configured.</p>
              </div>
            ) : (
              <div className="space-y-3 text-sm" data-testid="polymarket-opportunities-error">
                <p className="text-muted-foreground">Unable to load opportunities.</p>
                <Button type="button" variant="outline" size="sm" onClick={() => void scannerQuery.refetch()}>
                  Retry
                </Button>
              </div>
            )
          ) : scannerOpps.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="polymarket-opportunities-empty">
              <Search className="size-8 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">No opportunities met the paper-first filters.</p>
            </div>
          ) : (
            <>
              <div className="hidden overflow-hidden border border-border md:block">
                <table className="w-full text-left text-sm">
                  <thead>
                    <tr className="border-b border-border bg-muted/40 font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                      <th className="px-3 py-2 font-medium">Instrument</th>
                      <th className="px-3 py-2 font-medium">Net EV</th>
                      <th className="px-3 py-2 font-medium">Approved size</th>
                      <th className="px-3 py-2 font-medium">Risk / status</th>
                      <th className="px-3 py-2 font-medium">Risk reasons</th>
                      <th className="px-3 py-2 font-medium">Journal</th>
                    </tr>
                  </thead>
                  <tbody>
                    {scannerOpps.map((opportunity) => (
                      <tr key={opportunity.decision.id} className="border-b border-border last:border-0">
                        <td className="px-3 py-3 align-top font-mono text-xs text-muted-foreground break-all">{opportunity.decision.instrument_key || '—'}</td>
                        <td className={opportunity.decision.net_ev > 0 ? 'px-3 py-3 align-top font-semibold text-emerald-500' : opportunity.decision.net_ev < 0 ? 'px-3 py-3 align-top font-semibold text-destructive' : 'px-3 py-3 align-top font-semibold text-muted-foreground'}>
                          {safeMoneyLabel(opportunity.decision.net_ev)}
                        </td>
                        <td className="px-3 py-3 align-top">{safeNumberLabel(opportunity.decision.approved_size, 0)}</td>
                        <td className="px-3 py-3 align-top">
                          <div className="flex flex-wrap gap-1.5">
                            <Badge variant={riskStatusVariants[opportunity.decision.risk_status] ?? 'secondary'}>{opportunity.decision.risk_status}</Badge>
                            <Badge variant={decisionStatusVariants[opportunity.decision.status]}>{opportunity.decision.status}</Badge>
                            {opportunity.accepted == null ? null : <Badge variant={opportunity.accepted ? 'success' : 'destructive'}>{opportunity.accepted ? 'accepted' : 'rejected'}</Badge>}
                          </div>
                        </td>
                        <td className="px-3 py-3 align-top text-muted-foreground">
                          {splitReasons(opportunity.reasons ?? opportunity.decision.risk_reasons).join(' · ') || 'No risk reasons recorded'}
                        </td>
                        <td className="px-3 py-3 align-top text-sm">
                          <Link to="/journal" className="text-primary underline-offset-4 hover:underline">
                            Journal
                          </Link>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div className="space-y-3 md:hidden">
                {scannerOpps.map((opportunity) => (
                  <ScannerOpportunityCard key={opportunity.decision.id} opportunity={opportunity} />
                ))}
              </div>
            </>
          )}
      </ConsolePanel>

      <Card>
        <CardHeader>
          <CardTitle>Polymarket Jobs</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-2">
            {jobsData.map((job) => (
              <div key={job.name} className="rounded-md border border-border p-3 text-xs">
                <div className="font-medium">{job.name}</div>
                <div>{job.schedule}</div>
                <div>Last: {formatRelativeTime(job.last_run)}</div>
                <Badge variant={job.running ? 'success' : 'secondary'}>{job.running ? 'running' : job.enabled ? 'enabled' : 'idle'}</Badge>
                <Button size="sm" variant="outline" onClick={() => { void apiClient.runAutomationJob(job.name).then(() => qc.invalidateQueries({ queryKey: ['polymarket-jobs'] })) }}>
                  Run now
                </Button>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Watched Markets</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="mb-3 flex gap-2">
            <Input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="market slug" />
            <Button onClick={doAdd}>Add</Button>
          </div>
          {err ? <p className="text-sm text-destructive">{err}</p> : null}
          <table className="w-full text-sm">
            <tbody>
              {watchedData.map((market) => (
                <tr key={market.slug}>
                  <td><a href={marketUrl(market.slug)} target="_blank" rel="noreferrer">{market.slug}</a></td>
                  <td><input type="checkbox" checked={market.enabled} onChange={(e) => enable.mutate({ slug: market.slug, enabled: e.target.checked })} /></td>
                  <td>{formatRelativeTime(market.added_at)}</td>
                  <td>{market.note ?? '--'}</td>
                  <td><Button size="sm" variant="outline" onClick={() => remove.mutate(market.slug)}>Remove</Button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Tracked Wallets</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="mb-3 flex flex-wrap gap-2">
            <label>
              <input type="checkbox" checked={trackedOnly} onChange={(e) => setTrackedOnly(e.target.checked)} /> tracked-only
            </label>
            <Input type="number" step="0.01" min="0" max="1" value={minWinRate} onChange={(e) => setMinWinRate(e.target.value)} placeholder="min raw win rate" />
            <Input type="number" step="1" min="0" value={minResolved} onChange={(e) => setMinResolved(e.target.value)} placeholder="min resolved" />
            <select value={sort} onChange={(e) => setSort(e.target.value as AccountSort)}>
              <option value="consistency_score">consistency</option>
              <option value="bayesian_win_rate">bayesian win rate</option>
              <option value="resolved_markets">resolved markets</option>
              <option value="win_rate">raw win rate</option>
              <option value="volume">volume</option>
              <option value="last_active">last active</option>
              <option value="trades">trades</option>
            </select>
            <span className="text-xs text-muted-foreground">score = adjusted win rate × sample size</span>
          </div>
          <table className="w-full text-sm">
            <thead>
              <tr>
                <th>address</th><th>score</th><th>adj win</th><th>raw win</th><th>won/lost</th><th>volume</th><th>max_position</th><th>last_active</th><th>tracked</th><th>tags</th>
              </tr>
            </thead>
            <tbody>
              {accountsData.map((account) => (
                <tr key={account.address}>
                  <td>
                    <div className="flex flex-col">
                      <Link to={`/polymarket/accounts/${account.address}`}>{shortAddress(account.address)}</Link>
                      <a className="text-xs text-muted-foreground" href={profileUrl(account.address)} target="_blank" rel="noreferrer">profile</a>
                    </div>
                  </td>
                  <td>{(account.consistency_score ?? 0).toFixed(3)}</td>
                  <td>{((account.bayesian_win_rate ?? 0) * 100).toFixed(1)}%</td>
                  <td>{(account.win_rate * 100).toFixed(1)}%</td>
                  <td>{`${account.markets_won}/${account.markets_lost} (${account.resolved_markets ?? account.markets_won + account.markets_lost})`}</td>
                  <td>{money.format(account.total_volume)}</td>
                  <td>{money.format(account.max_position)}</td>
                  <td>{formatRelativeTime(account.last_active)}</td>
                  <td>
                    <input
                      type="checkbox"
                      checked={account.tracked}
                      onChange={(e) =>
                        track.mutate(
                          { address: account.address, tracked: e.target.checked },
                          { onSuccess: () => qc.invalidateQueries({ queryKey: ['polymarket-account', account.address] }) },
                        )
                      }
                    />
                  </td>
                  <td>{account.tags?.join(', ') || '--'}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="mt-3 flex gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={offset === 0}
              onClick={() => setOffset((current) => Math.max(0, current - 25))}
            >
              Prev
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={accountsData.length < 25}
              onClick={() => setOffset((current) => current + 25)}
            >
              Next
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent Whale Trades</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="mb-2 flex items-center gap-2">
            <span className={ws.status === 'open' ? 'size-2 rounded-full bg-emerald-400' : 'size-2 rounded-full bg-zinc-500'} />
            {ws.status}
          </div>
          <table className="w-full text-sm">
            <tbody>
              {(trades.data?.data ?? []).map((trade) => (
                <tr key={trade.id}>
                  <td>{formatRelativeTime(trade.timestamp)}</td>
                  <td>
                    <div className="flex flex-col">
                      <Link to={`/polymarket/accounts/${trade.account_address}`}>{shortAddress(trade.account_address)}</Link>
                      <a className="text-xs text-muted-foreground" href={profileUrl(trade.account_address)} target="_blank" rel="noreferrer">profile</a>
                    </div>
                  </td>
                  <td>
                    <div className="flex flex-col">
                      <a href={marketUrl(trade.market_slug)} target="_blank" rel="noreferrer">{trade.market_slug}</a>
                      <a className="text-xs text-muted-foreground" href={eventUrl(trade.market_slug)} target="_blank" rel="noreferrer">event</a>
                    </div>
                  </td>
                  <td><Badge variant={String(trade.side).toUpperCase() === 'YES' ? 'success' : 'destructive'}>{trade.side}</Badge></td>
                  <td>{money2.format(trade.size_usdc)}</td>
                  <td>{trade.price.toFixed(3)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent Polymarket Signals</CardTitle>
        </CardHeader>
        <CardContent>
          {signalsData.map((signal) => (
            <div key={signal.id} className="flex justify-between border-b border-border py-2">
              <div>
                {formatRelativeTime(signal.received_at)} · {signal.title}
              </div>
              <Badge>{signal.source}</Badge>
              <div>{signal.urgency}</div>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Polymarket Strategies</CardTitle>
        </CardHeader>
        <CardContent>
          {strategiesData.map((strategy) => (
            <div key={strategy.id}>
              <Link to={`/strategies/${strategy.id}`}>{strategy.name}</Link>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card data-testid="polymarket-discovery">
        <CardHeader>
          <CardTitle>Auto-generated Strategies</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="mb-3 flex items-center gap-2">
            <Button size="sm" disabled={runDiscovery.isPending} onClick={() => runDiscovery.mutate()}>
              {runDiscovery.isPending ? 'Running…' : 'Run discovery now'}
            </Button>
            <span className="text-xs text-muted-foreground">Cron: every 6 hours. Generates paper strategies from open Polymarket markets.</span>
          </div>
          {runDiscovery.isError ? <p className="mb-2 text-sm text-destructive">{(runDiscovery.error as Error)?.message ?? 'Failed to start discovery'}</p> : null}
          {(() => {
            const last = discoveryLast.data?.last
            if (!last) return <p className="text-sm text-muted-foreground">No discovery run yet.</p>
            const deployed = last.deployed ?? []
            return (
              <div className="space-y-2">
                <div className="text-xs text-muted-foreground">
                  Last run {formatRelativeTime(last.started_at)} · fetched {last.fetched_all} · screened {last.screened} · proposed {last.proposed} · skipped {last.skipped} · deployed {deployed.length}{last.dry_run ? ' (dry run)' : ''}
                </div>
                {deployed.length === 0 ? (
                  <p className="text-sm">No strategies deployed in the last run.</p>
                ) : (
                  <table className="w-full text-sm">
                    <thead>
                      <tr>
                        <th className="text-left">name</th><th className="text-left">slug</th><th className="text-left">template</th><th className="text-left">side</th><th className="text-left">conviction</th><th />
                      </tr>
                    </thead>
                    <tbody>
                      {deployed.map((deployedStrategy) => (
                        <tr key={deployedStrategy.strategy_id}>
                          <td><Link to={`/strategies/${deployedStrategy.strategy_id}`}>{deployedStrategy.name}</Link></td>
                          <td><a href={marketUrl(deployedStrategy.slug)} target="_blank" rel="noreferrer">{deployedStrategy.slug}</a></td>
                          <td><Badge variant="secondary">{deployedStrategy.template}</Badge></td>
                          <td><Badge variant={deployedStrategy.direction === 'YES' ? 'success' : 'destructive'}>{deployedStrategy.direction}</Badge></td>
                          <td>{(deployedStrategy.conviction * 100).toFixed(0)}%</td>
                          <td>{deployedStrategy.reused ? <Badge variant="secondary">reused</Badge> : <Badge variant="success">new</Badge>}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                {last.errors && last.errors.length > 0 ? (
                  <details className="text-xs">
                    <summary>{last.errors.length} errors</summary>
                    <ul className="mt-1 list-disc pl-4">
                      {last.errors.map((error, index) => (
                        <li key={index}>{error}</li>
                      ))}
                    </ul>
                  </details>
                ) : null}
              </div>
            )
          })()}
        </CardContent>
      </Card>
    </div>
  )
}

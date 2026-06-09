import { useQuery } from '@tanstack/react-query'
import { ArrowRight, CircleAlert, FileText, Shield } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { apiClient } from '@/lib/api/client'
import type {
  EngineStatus,
  MarketType,
  TradeDecision,
  TradeDecisionStatus,
  TradeDecisionRiskStatus,
} from '@/lib/api/types'
import { cn } from '@/lib/utils'

const MARKET_TYPES: Array<{ value: '' | MarketType; label: string }> = [
  { value: '', label: 'All markets' },
  { value: 'stock', label: 'Stocks' },
  { value: 'crypto', label: 'Crypto' },
  { value: 'polymarket', label: 'Polymarket' },
  { value: 'options', label: 'Options' },
]

const DECISION_STATUSES: Array<{ value: '' | TradeDecisionStatus; label: string }> = [
  { value: '', label: 'All statuses' },
  { value: 'candidate', label: 'Candidate' },
  { value: 'rejected', label: 'Rejected' },
  { value: 'paper_ordered', label: 'Paper ordered' },
  { value: 'live_ordered', label: 'Live ordered' },
  { value: 'closed', label: 'Closed' },
]

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

function safeDateLabel(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

function safeMoneyLabel(value: unknown) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : Number.NaN
  if (!Number.isFinite(parsed)) return '—'
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(parsed)
}

function splitReasons(reasons?: string[] | null) {
  if (!Array.isArray(reasons) || reasons.length === 0) return []
  return reasons.map((reason) => reason.trim()).filter(Boolean)
}

function OrderReference({ id }: { id?: string | null }) {
  if (!id) {
    return <span className="font-mono text-xs text-muted-foreground">—</span>
  }

  return (
    <Link to={`/orders/${id}`} className="font-mono text-xs text-primary hover:underline">
      {id}
    </Link>
  )
}

function marketLabel(marketType: MarketType) {
  return MARKET_TYPES.find((option) => option.value === marketType)?.label ?? marketType
}

function statusLabel(status: TradeDecisionStatus) {
  return DECISION_STATUSES.find((option) => option.value === status)?.label ?? status
}

function riskBannerLabel(status?: EngineStatus) {
  if (!status) return 'Loading safety state…'
  const killSwitch = status.kill_switch.active ? 'kill switch active' : 'kill switch clear'
  return `${status.risk_status} · ${status.circuit_breaker.state} · ${killSwitch}`
}

function DecisionRow({ decision }: { decision: TradeDecision }) {
  const reasons = splitReasons(decision.risk_reasons)
  const riskReasonsLabel = reasons.length > 0 ? reasons.join(' · ') : 'No risk reasons recorded'
  const netEv = typeof decision.net_ev === 'number' ? decision.net_ev : Number(decision.net_ev)
  const netEvClass = Number.isFinite(netEv)
    ? netEv > 0
      ? 'text-emerald-400'
      : netEv < 0
        ? 'text-destructive'
        : 'text-muted-foreground'
    : 'text-muted-foreground'
  const decisionTone =
    decision.status === 'rejected'
      ? 'border-destructive/30 bg-destructive/5'
      : decision.status === 'live_ordered'
        ? 'border-emerald-500/25 bg-emerald-500/5'
        : decision.status === 'paper_ordered'
          ? 'border-amber-500/25 bg-amber-500/5'
          : 'border-border bg-card'

  return (
    <tr className={cn('border-b border-border last:border-0', decisionTone)}>
      <td className="px-3 py-3 align-top">
        <div className="space-y-1">
          <div className="text-sm font-medium text-foreground">{safeDateLabel(decision.created_at)}</div>
          <div className="text-xs text-muted-foreground">Updated {safeDateLabel(decision.updated_at)}</div>
        </div>
      </td>
      <td className="px-3 py-3 align-top">
        <Badge variant="outline">{marketLabel(decision.market_type)}</Badge>
      </td>
      <td className="px-3 py-3 align-top font-mono text-xs text-muted-foreground">
        <div className="max-w-[16rem] break-all">{decision.instrument_key || '—'}</div>
        {decision.outcome ? <div className="mt-1 text-[11px] uppercase tracking-[0.16em]">{decision.outcome}</div> : null}
      </td>
      <td className="px-3 py-3 align-top">
        <div className={cn('text-sm font-semibold', netEvClass)}>
          {safeMoneyLabel(decision.net_ev)}
        </div>
        <div className="mt-1 text-xs text-muted-foreground">
          Fair {safeMoneyLabel(decision.fair_value)} · Exec {safeMoneyLabel(decision.executable_price)}
        </div>
      </td>
      <td className="px-3 py-3 align-top">
        <div className="flex flex-wrap gap-1.5">
          <Badge variant={riskStatusVariants[decision.risk_status] ?? 'secondary'}>
            {decision.risk_status}
          </Badge>
          <Badge variant={decisionStatusVariants[decision.status]}>{statusLabel(decision.status)}</Badge>
        </div>
        <p className="mt-2 max-w-[24rem] text-xs leading-5 text-muted-foreground">{riskReasonsLabel}</p>
      </td>
      <td className="px-3 py-3 align-top text-sm text-muted-foreground">
        <div className="space-y-2">
          <div>
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Paper</div>
            <OrderReference id={decision.paper_order_id} />
          </div>
          <div>
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Live</div>
            <OrderReference id={decision.live_order_id} />
          </div>
          <div>
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Replay</div>
            <Link to={`/replay/decisions/${decision.id}`} className="text-xs text-primary hover:underline">
              View replay
            </Link>
          </div>
        </div>
      </td>
    </tr>
  )
}

function DecisionCard({ decision }: { decision: TradeDecision }) {
  const reasons = splitReasons(decision.risk_reasons)
  const netEv = typeof decision.net_ev === 'number' ? decision.net_ev : Number(decision.net_ev)
  const netEvClass = Number.isFinite(netEv)
    ? netEv > 0
      ? 'text-emerald-400'
      : netEv < 0
        ? 'text-destructive'
        : 'text-muted-foreground'
    : 'text-muted-foreground'

  return (
    <article className="rounded-lg border border-border bg-card p-4 shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1">
          <div className="text-sm font-medium text-foreground">{safeDateLabel(decision.created_at)}</div>
          <div className="flex flex-wrap gap-1.5">
            <Badge variant="outline">{marketLabel(decision.market_type)}</Badge>
            <Badge variant={decisionStatusVariants[decision.status]}>{statusLabel(decision.status)}</Badge>
            <Badge variant={riskStatusVariants[decision.risk_status] ?? 'secondary'}>{decision.risk_status}</Badge>
          </div>
        </div>
        <div className={cn('text-right text-sm font-semibold', netEvClass)}>
          {safeMoneyLabel(decision.net_ev)}
          <div className="mt-1 text-xs font-normal text-muted-foreground">Net EV</div>
        </div>
      </div>

      <div className="mt-4 space-y-3 text-sm">
        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Instrument</div>
          <div className="mt-1 break-all font-mono text-xs text-muted-foreground">{decision.instrument_key || '—'}</div>
        </div>

        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Risk reasons</div>
          <div className="mt-1 text-muted-foreground">{reasons.length > 0 ? reasons.join(' · ') : 'No risk reasons recorded'}</div>
        </div>

        <div className="grid grid-cols-2 gap-3 text-xs text-muted-foreground">
          <div>
            <div className="uppercase tracking-[0.18em]">Paper order</div>
            <div className="mt-1">
              <OrderReference id={decision.paper_order_id} />
            </div>
          </div>
          <div>
            <div className="uppercase tracking-[0.18em]">Live order</div>
            <div className="mt-1">
              <OrderReference id={decision.live_order_id} />
            </div>
          </div>
        </div>

        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Replay</div>
          <Link to={`/replay/decisions/${decision.id}`} className="mt-1 inline-flex text-primary hover:underline">
            View replay workbench
          </Link>
        </div>
      </div>
    </article>
  )
}

export function DecisionJournalPage() {
  const [marketType, setMarketType] = useState<'' | MarketType>('')
  const [status, setStatus] = useState<'' | TradeDecisionStatus>('')

  const query = useMemo(
    () => ({
      market_type: marketType || undefined,
      status: status || undefined,
      limit: 100,
    }),
    [marketType, status],
  )

  const decisionsQuery = useQuery({
    queryKey: ['trade-decisions', query],
    queryFn: () => apiClient.listTradeDecisions(query),
  })

  const riskQuery = useQuery<EngineStatus>({
    queryKey: ['riskStatus', 'journal-banner'],
    queryFn: () => apiClient.getRiskStatus(),
    staleTime: 30_000,
  })

  const decisions = decisionsQuery.data?.data ?? []

  return (
    <div className="space-y-4" data-testid="decision-journal-page">
      <PageHeader
        eyebrow="Audit trail"
        title="Decision journal"
        description="Read-only record of trade decisions. Paper-first by default; live order IDs are audit references only."
      />

      <Card className="border-primary/20 bg-primary/5">
        <CardHeader>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <CardTitle className="flex items-center gap-2">
                <Shield className="size-4" />
                Operator safety
              </CardTitle>
              <CardDescription>Paper-first journaling with live safety state visible.</CardDescription>
            </div>
            <Badge variant={riskQuery.data?.kill_switch.active ? 'destructive' : 'success'}>
              {riskBannerLabel(riskQuery.data)}
            </Badge>
          </div>
        </CardHeader>
        <CardContent className="grid gap-3 md:grid-cols-3">
          <div className="rounded-lg border border-border bg-card p-3">
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Circuit breaker</div>
            <div className="mt-1 text-sm font-medium">{riskQuery.data?.circuit_breaker.state ?? '—'}</div>
            <div className="mt-1 text-xs text-muted-foreground">{riskQuery.data?.circuit_breaker.reason || 'No breaker reason recorded'}</div>
          </div>
          <div className="rounded-lg border border-border bg-card p-3">
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Kill switch</div>
            <div className="mt-1 text-sm font-medium">
              {riskQuery.data ? (riskQuery.data.kill_switch.active ? 'Active' : 'Inactive') : '—'}
            </div>
            <div className="mt-1 text-xs text-muted-foreground">
              {riskQuery.data?.kill_switch.reason || 'No kill-switch reason recorded'}
            </div>
          </div>
          <div className="rounded-lg border border-border bg-card p-3">
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Journal scope</div>
            <div className="mt-1 text-sm font-medium">{decisionsQuery.isLoading ? 'Loading…' : `${decisions.length} visible`}</div>
            <div className="mt-1 text-xs text-muted-foreground">Filters stay server-side and read-only.</div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <CardTitle className="flex items-center gap-2">
                <FileText className="size-4" />
                Filters
              </CardTitle>
              <CardDescription>Filter by market type and decision status.</CardDescription>
            </div>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <CircleAlert className="size-4" />
              <span>Live trading controls are intentionally absent here.</span>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
            <label className="space-y-1.5">
              <span className="text-xs font-medium uppercase tracking-[0.16em] text-muted-foreground">Market type</span>
              <select
                value={marketType}
                onChange={(event) => setMarketType(event.target.value as '' | MarketType)}
                className="flex h-9 w-full rounded-md border border-input bg-card px-3 py-1 text-sm text-foreground transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                aria-label="Market type filter"
              >
                {MARKET_TYPES.map((option) => (
                  <option key={option.value || 'all'} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>

            <label className="space-y-1.5">
              <span className="text-xs font-medium uppercase tracking-[0.16em] text-muted-foreground">Status</span>
              <select
                value={status}
                onChange={(event) => setStatus(event.target.value as '' | TradeDecisionStatus)}
                className="flex h-9 w-full rounded-md border border-input bg-card px-3 py-1 text-sm text-foreground transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                aria-label="Decision status filter"
              >
                {DECISION_STATUSES.map((option) => (
                  <option key={option.value || 'all'} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>

            <div className="flex items-end gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setMarketType('')
                  setStatus('')
                }}
                disabled={!marketType && !status}
              >
                Reset
              </Button>
              <Button type="button" variant="secondary" onClick={() => void decisionsQuery.refetch()}>
                <ArrowRight className="size-4" />
                Refresh
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Decision feed</CardTitle>
          <CardDescription>
            Created time, market, instrument, net EV, risk state, and order audit IDs.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {decisionsQuery.isLoading ? (
            <div className="space-y-3" data-testid="decision-journal-loading">
              {Array.from({ length: 4 }).map((_, index) => (
                <div key={index} className="h-24 animate-pulse rounded-lg border border-border bg-muted/50" />
              ))}
            </div>
          ) : decisionsQuery.isError ? (
            <div className="space-y-3 text-sm" data-testid="decision-journal-error">
              <p className="text-muted-foreground">Unable to load decision journal.</p>
              <Button type="button" variant="outline" size="sm" onClick={() => void decisionsQuery.refetch()}>
                Retry
              </Button>
            </div>
          ) : decisions.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-10 text-center" data-testid="decision-journal-empty">
              <FileText className="size-8 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">No decisions match the current filters.</p>
            </div>
          ) : (
            <>
              <div className="hidden overflow-hidden rounded-lg border border-border md:block">
                <table className="w-full text-left text-sm">
                  <thead>
                    <tr className="border-b border-border bg-muted/40 font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                      <th className="px-3 py-2 font-medium">Created</th>
                      <th className="px-3 py-2 font-medium">Market</th>
                      <th className="px-3 py-2 font-medium">Instrument</th>
                      <th className="px-3 py-2 font-medium">Net EV</th>
                      <th className="px-3 py-2 font-medium">Risk / status</th>
                      <th className="px-3 py-2 font-medium">Orders</th>
                    </tr>
                  </thead>
                  <tbody>
                    {decisions.map((decision) => (
                      <DecisionRow key={decision.id} decision={decision} />
                    ))}
                  </tbody>
                </table>
              </div>

              <div className="space-y-3 md:hidden">
                {decisions.map((decision) => (
                  <DecisionCard key={decision.id} decision={decision} />
                ))}
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, CircleAlert, Clock3, History } from 'lucide-react'
import { useMemo, type ReactNode } from 'react'
import { Link, useParams } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ConsolePanel, HudSection, StatusLed } from '@/components/ui/hud'
import { ApiClientError, apiClient } from '@/lib/api/client'
import type {
  ReplayDecisionResponse,
  ReplayEvent,
  ReplayEventType,
  TradeDecisionRiskStatus,
  TradeDecisionStatus,
} from '@/lib/api/types'

const DECISION_STATUS_LABELS: Record<TradeDecisionStatus, string> = {
  candidate: 'Candidate',
  rejected: 'Rejected',
  paper_ordered: 'Paper ordered',
  live_ordered: 'Live ordered',
  closed: 'Closed',
}

const DECISION_STATUS_VARIANTS: Record<TradeDecisionStatus, 'secondary' | 'outline' | 'success' | 'warning' | 'destructive'> = {
  candidate: 'outline',
  rejected: 'destructive',
  paper_ordered: 'warning',
  live_ordered: 'success',
  closed: 'secondary',
}

const RISK_STATUS_VARIANTS: Record<TradeDecisionRiskStatus, 'success' | 'destructive'> = {
  approved: 'success',
  rejected: 'destructive',
}

const REPLAY_EVENT_LABELS: Record<ReplayEventType, string> = {
  decision_created: 'Decision created',
  risk_reviewed: 'Risk reviewed',
  paper_ordered: 'Paper ordered',
  live_ordered: 'Live ordered',
  fill_observed: 'Fill observed',
  position_updated: 'Position updated',
  outcome_resolved: 'Outcome resolved',
}

const REPLAY_EVENT_VARIANTS: Record<ReplayEventType, 'secondary' | 'outline' | 'success' | 'warning' | 'destructive'> = {
  decision_created: 'outline',
  risk_reviewed: 'secondary',
  paper_ordered: 'warning',
  live_ordered: 'success',
  fill_observed: 'success',
  position_updated: 'secondary',
  outcome_resolved: 'secondary',
}

const moneyFormatter = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

const integerFormatter = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 0,
})

const decimalFormatter = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 2,
})

function safeDateLabel(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

function safeNumberLabel(value: unknown, formatter = integerFormatter) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : Number.NaN
  if (!Number.isFinite(parsed)) return '—'
  return formatter.format(parsed)
}

function safeMoneyLabel(value: unknown) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : Number.NaN
  if (!Number.isFinite(parsed)) return '—'
  return moneyFormatter.format(parsed)
}

function safePayloadPreview(payload: unknown) {
  if (payload == null) return 'null'
  if (typeof payload === 'string') return payload
  if (typeof payload === 'number' || typeof payload === 'boolean' || typeof payload === 'bigint') {
    return String(payload)
  }

  try {
    const serialized = JSON.stringify(payload, null, 2)
    return serialized ?? String(payload)
  } catch {
    return '[unserializable payload]'
  }
}

function sortReplayEvents(events: ReplayEvent[]) {
  return [...events].sort((left, right) => {
    const leftOccurred = Date.parse(left.occurred_at)
    const rightOccurred = Date.parse(right.occurred_at)

    if (Number.isFinite(leftOccurred) && Number.isFinite(rightOccurred) && leftOccurred !== rightOccurred) {
      return leftOccurred - rightOccurred
    }

    if (Number.isFinite(leftOccurred) !== Number.isFinite(rightOccurred)) {
      return Number.isFinite(leftOccurred) ? -1 : 1
    }

    const leftCreated = Date.parse(left.created_at)
    const rightCreated = Date.parse(right.created_at)

    if (Number.isFinite(leftCreated) && Number.isFinite(rightCreated) && leftCreated !== rightCreated) {
      return leftCreated - rightCreated
    }

    return left.id.localeCompare(right.id)
  })
}

function DecisionField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="border border-border bg-panel p-3">
      <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm font-medium text-foreground">{children}</dd>
    </div>
  )
}

function SummaryTile({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="border border-border bg-panel p-3">
      <div className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">{label}</div>
      <div className="mt-1 text-sm font-semibold text-foreground">{value}</div>
      {detail ? <div className="mt-1 text-xs text-muted-foreground">{detail}</div> : null}
    </div>
  )
}

function EventTimelineItem({ event }: { event: ReplayEvent }) {
  return (
    <li className="relative">
      <span className="absolute -left-[1.3rem] top-4 size-2 rounded-none border border-border bg-panel" />
      <Card className="border-border/80 bg-card/80">
        <CardContent className="space-y-3 p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="space-y-1">
              <div className="flex flex-wrap gap-1.5">
                <Badge variant={REPLAY_EVENT_VARIANTS[event.event_type] ?? 'secondary'}>
                  {REPLAY_EVENT_LABELS[event.event_type] ?? event.event_type}
                </Badge>
                <Badge variant="outline">{event.source || 'unknown source'}</Badge>
              </div>
              <div className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                Event {event.id}
              </div>
            </div>
            <div className="text-right text-xs text-muted-foreground">
              <div>{safeDateLabel(event.occurred_at)}</div>
              <div className="mt-1">Created {safeDateLabel(event.created_at)}</div>
            </div>
          </div>

          <dl className="grid gap-3 sm:grid-cols-2">
            <DecisionField label="Decision ID">{event.trade_decision_id || '—'}</DecisionField>
            <DecisionField label="Occurred at">{safeDateLabel(event.occurred_at)}</DecisionField>
          </dl>

          <div>
            <div className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Payload preview</div>
            <pre className="mt-2 max-h-64 overflow-x-auto rounded-none border border-border bg-panel p-3 font-mono text-[12px] leading-5 text-muted-foreground whitespace-pre-wrap break-words">
              {safePayloadPreview(event.payload)}
            </pre>
          </div>
        </CardContent>
      </Card>
    </li>
  )
}

function ReplayLoadingState() {
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <div className="h-4 w-40 animate-pulse rounded-none border border-border bg-muted/40" />
          <div className="h-3 w-72 animate-pulse rounded-none border border-border bg-muted/30" />
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 lg:grid-cols-2 xl:grid-cols-3">
            {Array.from({ length: 6 }).map((_, index) => (
              <div key={index} className="h-20 animate-pulse rounded-none border border-border bg-muted/40" />
            ))}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div className="h-4 w-36 animate-pulse rounded-none border border-border bg-muted/40" />
          <div className="h-3 w-64 animate-pulse rounded-none border border-border bg-muted/30" />
        </CardHeader>
        <CardContent>
          <div className="space-y-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <div key={index} className="h-36 animate-pulse rounded-none border border-border bg-muted/40" />
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

export function ReplayPage() {
  const { id } = useParams<{ id: string }>()

  const replayQuery = useQuery<ReplayDecisionResponse>({
    queryKey: ['decision-replay', id],
    queryFn: () => apiClient.getDecisionReplay(id ?? ''),
    enabled: Boolean(id),
  })

  const replay = replayQuery.data
  const source = replay?.source
  const summary = replay?.summary
  const events = useMemo(() => sortReplayEvents(replay?.events ?? []), [replay?.events])

  const errorStatus = replayQuery.error instanceof ApiClientError ? replayQuery.error.status : undefined
  const isConfiguredError = errorStatus === 501
  const isNotFoundError = errorStatus === 404
  const replayState = replayQuery.isError ? 'warn' : replayQuery.isLoading ? 'sync' : 'ok'

  return (
    <div className="space-y-4" data-testid="replay-page">
      <PageHeader
        eyebrow="Audit trail"
        title="Replay workbench"
        description="Read-only event timeline for a trade decision."
        meta={<StatusLed state={replayState} label={replay ? 'Replay ready' : 'Loading'} />}
        actions={(
          <Button asChild variant="outline" size="sm">
            <Link to="/journal">
              <ArrowLeft className="size-4" />
              Back to journal
            </Link>
          </Button>
        )}
      />

      {replayQuery.isLoading ? (
        <ReplayLoadingState />
      ) : replayQuery.isError ? (
        <Card data-testid="replay-error">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <CircleAlert className="size-4" />
              Replay workbench unavailable
            </CardTitle>
            <CardDescription>
              {isConfiguredError
                ? 'Replay workbench is not configured on this deployment.'
                : isNotFoundError
                  ? 'No replay data exists for this decision.'
                  : 'Unable to load the replay workbench.'}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-wrap items-center gap-2">
            <Button asChild variant="outline" size="sm">
              <Link to="/journal">Back to journal</Link>
            </Button>
            {!isConfiguredError && !isNotFoundError ? (
              <Button type="button" variant="secondary" size="sm" onClick={() => void replayQuery.refetch()}>
                Retry
              </Button>
            ) : null}
          </CardContent>
        </Card>
      ) : replay ? (
        <div className="space-y-4">
          <ConsolePanel className="space-y-4 p-4">
            <HudSection label="Decision summary" note="Source decision captured by the backend replay API" />
              <dl className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                <DecisionField label="Decision ID">{source?.id ?? '—'}</DecisionField>
                <DecisionField label="Market type">{source?.market_type ?? '—'}</DecisionField>
                <DecisionField label="Instrument">{source?.instrument_key || '—'}</DecisionField>
                <DecisionField label="Side">{source?.side ?? '—'}</DecisionField>
                <DecisionField label="Status">
                  {source ? (
                    <Badge variant={DECISION_STATUS_VARIANTS[source.status] ?? 'secondary'}>
                      {DECISION_STATUS_LABELS[source.status] ?? source.status}
                    </Badge>
                  ) : (
                    '—'
                  )}
                </DecisionField>
                <DecisionField label="Risk status">
                  {source ? (
                    <Badge variant={RISK_STATUS_VARIANTS[source.risk_status] ?? 'secondary'}>
                      {source.risk_status}
                    </Badge>
                  ) : (
                    '—'
                  )}
                </DecisionField>
                <DecisionField label="Net EV">{safeMoneyLabel(source?.net_ev)}</DecisionField>
                <DecisionField label="Approved size">{safeNumberLabel(source?.approved_size, decimalFormatter)}</DecisionField>
                <DecisionField label="Created at">{safeDateLabel(source?.created_at)}</DecisionField>
                <DecisionField label="Updated at">{safeDateLabel(source?.updated_at)}</DecisionField>
                <DecisionField label="Fair value">{safeMoneyLabel(source?.fair_value)}</DecisionField>
                <DecisionField label="Executable price">{safeMoneyLabel(source?.executable_price)}</DecisionField>
              </dl>

              <div className="mt-3 grid gap-3 lg:grid-cols-3">
                <div>
                  <div className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Risk reasons</div>
                  <div className="mt-1 text-sm text-muted-foreground">
                    {source?.risk_reasons?.length ? source.risk_reasons.join(' · ') : 'No risk reasons recorded.'}
                  </div>
                </div>
                <div>
                  <div className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Related</div>
                  <div className="mt-1 flex flex-wrap gap-3 text-sm">
                    {source?.strategy_id ? (
                      <Link to={`/strategies/${source.strategy_id}`} className="text-primary hover:underline">
                        Strategy
                      </Link>
                    ) : null}
                    {source?.pipeline_run_id ? (
                      <Link to={`/runs/${source.pipeline_run_id}`} className="text-primary hover:underline">
                        Pipeline run
                      </Link>
                    ) : null}
                    {source?.paper_order_id ? (
                      <Link to={`/orders/${source.paper_order_id}`} className="text-primary hover:underline">
                        Paper order
                      </Link>
                    ) : null}
                    {source?.live_order_id ? (
                      <Link to={`/orders/${source.live_order_id}`} className="text-primary hover:underline">
                        Live order
                      </Link>
                    ) : null}
                  </div>
                </div>
                <div>
                  <div className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Outcome</div>
                  <div className="mt-1 text-sm text-muted-foreground">{source?.outcome || '—'}</div>
                </div>
              </div>
          </ConsolePanel>

          <Card>
            <CardHeader>
              <CardTitle>Replay summary</CardTitle>
              <CardDescription>Backend-computed timeline totals and coverage indicators.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                <SummaryTile
                  label="Event count"
                  value={safeNumberLabel(summary?.event_count)}
                  detail={`First ${safeDateLabel(summary?.first_event_at)} · Last ${safeDateLabel(summary?.last_event_at)}`}
                />
                <SummaryTile label="Latest status" value={summary?.latest_status || '—'} />
                <SummaryTile label="Approved size" value={safeNumberLabel(summary?.total_approved_size, decimalFormatter)} />
                <SummaryTile label="Net EV" value={safeMoneyLabel(summary?.total_net_ev)} />
                <SummaryTile label="Rejections" value={safeNumberLabel(summary?.rejection_count)} />
                <SummaryTile
                  label="Coverage"
                  value={[
                    summary?.has_paper_order ? 'paper' : 'no paper',
                    summary?.has_live_order ? 'live' : 'no live',
                    summary?.has_fill ? 'fill' : 'no fill',
                    summary?.has_outcome ? 'outcome' : 'no outcome',
                  ].join(' · ')}
                />
              </div>

              <div className="flex flex-wrap gap-2">
                <Badge variant={summary?.has_paper_order ? 'success' : 'outline'}>
                  {summary?.has_paper_order ? 'paper order' : 'no paper order'}
                </Badge>
                <Badge variant={summary?.has_live_order ? 'success' : 'outline'}>
                  {summary?.has_live_order ? 'live order' : 'no live order'}
                </Badge>
                <Badge variant={summary?.has_fill ? 'success' : 'outline'}>
                  {summary?.has_fill ? 'fill observed' : 'no fill'}
                </Badge>
                <Badge variant={summary?.has_outcome ? 'success' : 'outline'}>
                  {summary?.has_outcome ? 'outcome resolved' : 'no outcome'}
                </Badge>
              </div>

              {summary?.rejection_reasons?.length ? (
                <div className="border border-border bg-panel p-3">
                  <div className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                    Rejection reasons
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2 text-sm text-muted-foreground">
                    {summary.rejection_reasons.map((reason) => (
                      <Badge key={reason} variant="secondary" className="max-w-full normal-case tracking-normal">
                        {reason}
                      </Badge>
                    ))}
                  </div>
                </div>
              ) : null}
            </CardContent>
          </Card>

          <ConsolePanel className="space-y-4 p-4">
            <HudSection label="Replay summary" note="Backend-computed timeline totals and coverage indicators" />
            <div className="flex items-center gap-2">
                <History className="size-4" />
                <span className="text-sm font-semibold uppercase tracking-[0.1em]">Event timeline</span>
            </div>
              {events.length === 0 ? (
                <div className="flex flex-col items-center gap-2 py-10 text-center" data-testid="replay-empty">
                  <Clock3 className="size-8 text-muted-foreground" />
                  <p className="text-sm text-muted-foreground">
                    No replay events were recorded for this decision yet.
                  </p>
                  <p className="text-xs text-muted-foreground">
                    The decision summary above is still available for audit review.
                  </p>
                </div>
              ) : (
                <ol className="relative space-y-4 border-l border-border/80 pl-4">
                  {events.map((event) => (
                    <EventTimelineItem key={event.id} event={event} />
                  ))}
                </ol>
              )}
          </ConsolePanel>
        </div>
      ) : null}
    </div>
  )
}

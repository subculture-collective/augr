import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Power } from 'lucide-react'
import { useState } from 'react'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiClientError, apiClient } from '@/lib/api/client'
import { formatCurrency } from '@/lib/format'
import type {
  AuditLogEntry,
  CircuitBreakerPhase,
  EngineStatus,
  RiskCockpitExposure,
  RiskCockpitSummary,
} from '@/lib/api/types'

const circuitBreakerBadge: Record<CircuitBreakerPhase, { label: string; variant: 'success' | 'destructive' | 'warning' }> = {
  open: { label: 'Open', variant: 'success' },
  tripped: { label: 'Tripped', variant: 'destructive' },
  cooldown: { label: 'Cooldown', variant: 'warning' },
}

const cockpitMarketOrder: RiskCockpitExposure['market_type'][] = ['stock', 'crypto', 'options', 'polymarket']

const cockpitMarketLabels: Record<RiskCockpitExposure['market_type'], string> = {
  stock: 'Stock',
  crypto: 'Crypto',
  options: 'Options',
  polymarket: 'Polymarket',
}

function formatDetails(details?: unknown) {
  if (details == null) {
    return '—'
  }

  return typeof details === 'string' ? details : JSON.stringify(details, null, 2)
}

function formatCount(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toLocaleString('en-US') : '—'
}

function formatSafeCurrency(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value) ? formatCurrency(value) : '—'
}

function formatDateTime(iso?: string) {
  if (!iso) return '—'

  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function formatUtilizationValue(value: number) {
  return Number.isInteger(value) ? String(value) : value.toFixed(1)
}

function percentDisplayValue(value: number, usesFraction: boolean) {
  return usesFraction ? value * 100 : value
}

function utilizationBarClass(ratio: number) {
  if (ratio > 0.9) return 'bg-red-500'
  if (ratio >= 0.7) return 'bg-amber-500'
  return 'bg-emerald-500'
}

function UtilizationRow({
  label,
  current,
  max,
  suffix = '',
  testId,
}: {
  label: string
  current: number
  max: number
  suffix?: string
  testId: string
}) {
  const ratio = max > 0 ? current / max : 0
  const width = Math.max(0, Math.min(ratio * 100, 100))

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-4 text-sm">
        <span className="font-medium">{label}</span>
        <span className="text-muted-foreground">
          {formatUtilizationValue(current)}{suffix} / {formatUtilizationValue(max)}{suffix}
        </span>
      </div>
      <div className="h-3 overflow-hidden rounded-full bg-muted">
        <div
          data-testid={testId}
          className={`h-full rounded-full transition-all ${utilizationBarClass(ratio)}`}
          style={{ width: `${width}%` }}
        />
      </div>
    </div>
  )
}

function CockpitMarketCard({ exposure, marketType }: { exposure?: RiskCockpitExposure; marketType: RiskCockpitExposure['market_type'] }) {
  return (
    <Card className="border-border/70 bg-background/60" data-testid={`risk-cockpit-market-${marketType}`}>
      <CardHeader className="space-y-2 pb-3">
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="text-base">{cockpitMarketLabels[marketType]}</CardTitle>
            <CardDescription className="text-xs uppercase tracking-[0.16em]">{marketType}</CardDescription>
          </div>
          <Badge variant={exposure ? 'outline' : 'secondary'}>{exposure ? 'Live' : 'Missing'}</Badge>
        </div>
      </CardHeader>
      <CardContent>
        {exposure ? (
          <dl className="grid gap-2 sm:grid-cols-2 xl:grid-cols-1">
            <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
              <dt className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Gross exposure</dt>
              <dd className="mt-1 font-mono text-base">{formatSafeCurrency(exposure.gross_exposure)}</dd>
            </div>
            <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
              <dt className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Net expected value</dt>
              <dd className="mt-1 font-mono text-base">{formatSafeCurrency(exposure.net_expected_value)}</dd>
            </div>
            <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
              <dt className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Open positions</dt>
              <dd className="mt-1 font-mono text-base">{formatCount(exposure.open_positions)}</dd>
            </div>
            <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
              <dt className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Approved decisions</dt>
              <dd className="mt-1 font-mono text-base">{formatCount(exposure.approved_decisions)}</dd>
            </div>
            <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2 sm:col-span-2 xl:col-span-1">
              <dt className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Rejected decisions</dt>
              <dd className="mt-1 font-mono text-base">{formatCount(exposure.rejected_decisions)}</dd>
            </div>
          </dl>
        ) : (
          <p className="text-sm text-muted-foreground">No exposure data returned for this market.</p>
        )}
      </CardContent>
    </Card>
  )
}

export function RiskPage() {
  const queryClient = useQueryClient()
  const [reason, setReason] = useState('')
  const [showReasonInput, setShowReasonInput] = useState(false)
  const [auditLimit, setAuditLimit] = useState(10)

  const { data, isLoading, isError, error } = useQuery<EngineStatus>({
    queryKey: ['riskStatus'],
    queryFn: () => apiClient.getRiskStatus(),
    refetchInterval: 30_000,
  })

  const {
    data: cockpitData,
    isLoading: cockpitLoading,
    isError: cockpitError,
    error: cockpitErrorValue,
  } = useQuery<RiskCockpitSummary>({
    queryKey: ['riskCockpit'],
    queryFn: () => apiClient.getRiskCockpit(),
    refetchInterval: 30_000,
  })

  const {
    data: auditData,
    isLoading: auditLoading,
    isError: auditError,
  } = useQuery({
    queryKey: ['auditLog', auditLimit],
    queryFn: () => apiClient.listAuditLog({ limit: auditLimit }),
  })

  const toggleMutation = useMutation({
    mutationFn: (params: { active: boolean; reason?: string }) =>
      apiClient.toggleKillSwitch(params),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['riskStatus'] })
      queryClient.invalidateQueries({ queryKey: ['riskCockpit'] })
      queryClient.invalidateQueries({ queryKey: ['auditLog'] })
      setReason('')
      setShowReasonInput(false)
    },
  })

  const killSwitch = data?.kill_switch
  const circuitBreaker = data?.circuit_breaker
  const auditEntries = auditData?.data ?? []
  const cockpit = cockpitData
  const cockpitExposures = Array.isArray(cockpit?.exposures) ? cockpit.exposures : []
  const cockpitWarnings = Array.isArray(cockpit?.warnings) ? cockpit.warnings : []
  const cockpitExposureByMarket = new Map(cockpitExposures.map((entry) => [entry.market_type, entry]))

  function handleToggle() {
    if (!killSwitch) return

    if (!killSwitch.active && !showReasonInput) {
      setShowReasonInput(true)
      return
    }

    toggleMutation.mutate({
      active: !killSwitch.active,
      reason: killSwitch.active ? undefined : reason || undefined,
    })
  }

  const cockpitIsUnavailable =
    cockpitError && cockpitErrorValue instanceof ApiClientError && cockpitErrorValue.status === 501

  return (
    <div className="space-y-4" data-testid="risk-page">
      <PageHeader
        eyebrow="Controls"
        title="Risk engine"
        description="Review circuit breaker state, kill switch controls, exposure utilization, and recent audit events."
      />

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <Card>
          <CardHeader>
            <CardTitle>Circuit Breaker</CardTitle>
            <CardDescription>Current state of the circuit breaker</CardDescription>
          </CardHeader>
          <CardContent>
            {isLoading && <div data-testid="circuit-breaker-loading" className="h-20 animate-pulse rounded bg-muted" />}
            {isError && <p className="text-sm text-destructive">Failed to load: {(error as Error).message}</p>}
            {circuitBreaker && (
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">State:</span>
                  <Badge variant={circuitBreakerBadge[circuitBreaker.state].variant}>
                    {circuitBreakerBadge[circuitBreaker.state].label}
                  </Badge>
                </div>
                {circuitBreaker.reason && (
                  <p className="text-sm text-muted-foreground">
                    <span className="font-medium">Reason:</span> {circuitBreaker.reason}
                  </p>
                )}
                <p className="text-sm text-muted-foreground">
                  <span className="font-medium">Tripped at:</span> {formatDateTime(circuitBreaker.tripped_at)}
                </p>
                <p className="text-sm text-muted-foreground">
                  <span className="font-medium">Cooldown ends:</span> {formatDateTime(circuitBreaker.cooldown_end)}
                </p>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Kill Switch</CardTitle>
            <CardDescription>Emergency trading halt</CardDescription>
          </CardHeader>
          <CardContent>
            {isLoading && <div data-testid="kill-switch-loading" className="h-20 animate-pulse rounded bg-muted" />}
            {isError && <p className="text-sm text-destructive">Failed to load: {(error as Error).message}</p>}
            {killSwitch && (
              <div className="space-y-4">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">State:</span>
                  <Badge variant={killSwitch.active ? 'destructive' : 'success'}>
                    {killSwitch.active ? 'Active' : 'Inactive'}
                  </Badge>
                </div>
                {killSwitch.active && killSwitch.reason && (
                  <p className="text-sm text-muted-foreground">
                    <span className="font-medium">Reason:</span> {killSwitch.reason}
                  </p>
                )}
                <p className="text-sm text-muted-foreground">
                  <span className="font-medium">Activated at:</span> {formatDateTime(killSwitch.activated_at)}
                </p>

                {showReasonInput && !killSwitch.active ? (
                  <div className="space-y-2">
                    <Label htmlFor="kill-reason">Reason for halt</Label>
                    <Input
                      id="kill-reason"
                      placeholder="Enter reason..."
                      value={reason}
                      onChange={(e) => setReason(e.target.value)}
                    />
                    <div className="flex gap-2">
                      <Button
                        variant="destructive"
                        size="sm"
                        disabled={!reason.trim() || toggleMutation.isPending}
                        onClick={handleToggle}
                      >
                        <Power className="size-3.5" />
                        {toggleMutation.isPending ? 'Stopping...' : 'Confirm Stop All'}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setShowReasonInput(false)
                          setReason('')
                        }}
                      >
                        Cancel
                      </Button>
                    </div>
                  </div>
                ) : null}

                {!showReasonInput ? (
                  <Button
                    variant={killSwitch.active ? 'outline' : 'destructive'}
                    disabled={toggleMutation.isPending}
                    onClick={handleToggle}
                    data-testid="kill-switch-toggle"
                  >
                    <Power className="size-4" />
                    {toggleMutation.isPending
                      ? 'Processing...'
                      : killSwitch.active
                        ? 'Resume All'
                        : 'Stop All'}
                  </Button>
                ) : null}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Cross-flow cockpit</CardTitle>
          <CardDescription>Read-only cross-asset exposure snapshot with cockpit-wide warnings.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {cockpitLoading ? (
            <div data-testid="risk-cockpit-loading" className="space-y-3">
              <div className="h-10 animate-pulse rounded bg-muted" />
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                {Array.from({ length: 4 }).map((_, index) => (
                  <div key={index} className="h-52 animate-pulse rounded-lg border border-border bg-muted/50" />
                ))}
              </div>
            </div>
          ) : cockpitError ? (
            cockpitIsUnavailable ? (
              <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="risk-cockpit-unavailable">
                <p className="text-sm text-muted-foreground">Cross-flow cockpit not configured.</p>
              </div>
            ) : (
              <div className="space-y-3 text-sm" data-testid="risk-cockpit-error">
                <p className="text-muted-foreground">Unable to load cross-flow cockpit.</p>
                <Button type="button" variant="outline" size="sm" onClick={() => void queryClient.invalidateQueries({ queryKey: ['riskCockpit'] })}>
                  Retry
                </Button>
              </div>
            )
          ) : cockpit ? (
            <>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge variant={cockpit.kill_switch_active ? 'destructive' : 'success'}>
                  Kill switch {cockpit.kill_switch_active ? 'active' : 'clear'}
                </Badge>
                <Badge variant={cockpit.circuit_breaker ? 'warning' : 'success'}>
                  Circuit breaker {cockpit.circuit_breaker ? 'tripped' : 'clear'}
                </Badge>
                <span className="text-muted-foreground">Generated {formatDateTime(cockpit.generated_at)}</span>
              </div>

              {cockpitExposures.length === 0 ? (
                <div className="flex flex-col items-center gap-2 rounded-lg border border-border/70 bg-muted/20 px-4 py-8 text-center" data-testid="risk-cockpit-empty">
                  <p className="text-sm text-muted-foreground">No cross-flow exposures returned.</p>
                </div>
              ) : null}

              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                {cockpitMarketOrder.map((marketType) => (
                  <CockpitMarketCard key={marketType} marketType={marketType} exposure={cockpitExposureByMarket.get(marketType)} />
                ))}
              </div>

              <div className="rounded-lg border border-border/70 bg-muted/20 p-3">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium">Warnings</p>
                  <Badge variant={cockpitWarnings.length ? 'warning' : 'secondary'}>
                    {cockpitWarnings.length ? `${cockpitWarnings.length} active` : 'Clear'}
                  </Badge>
                </div>
                {cockpitWarnings.length ? (
                  <ul className="mt-2 space-y-2">
                    {cockpitWarnings.map((warning, index) => (
                      <li key={`${warning}-${index}`} className="rounded-md border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-sm text-foreground">
                        {String(warning)}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="mt-2 text-sm text-muted-foreground">No active cockpit warnings.</p>
                )}
              </div>
            </>
          ) : null}
        </CardContent>
      </Card>

      {data ? (
        <Card>
          <CardHeader>
            <CardTitle>Position limit utilization</CardTitle>
            <CardDescription>Current usage against configured open position and exposure caps</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <UtilizationRow
              label="Open positions"
              current={data.position_limits.current_open_positions ?? 0}
              max={data.position_limits.max_concurrent}
              testId="risk-utilization-open-positions"
            />
            <UtilizationRow
              label="Total exposure"
              current={percentDisplayValue(
                data.position_limits.current_total_exposure_pct ?? 0,
                data.position_limits.max_total_pct <= 1,
              )}
              max={percentDisplayValue(data.position_limits.max_total_pct, data.position_limits.max_total_pct <= 1)}
              suffix="%"
              testId="risk-utilization-total-exposure"
            />
          </CardContent>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Audit Log</CardTitle>
          <CardDescription>Recent system events</CardDescription>
        </CardHeader>
        <CardContent>
          {auditLoading ? (
            <div data-testid="audit-log-loading" className="space-y-2">
              {Array.from({ length: 3 }).map((_, index) => (
                <div key={index} className="h-8 animate-pulse rounded bg-muted" />
              ))}
            </div>
          ) : auditError ? (
            <p className="text-sm text-muted-foreground">Unable to load audit log.</p>
          ) : !auditEntries.length ? (
            <p className="text-sm text-muted-foreground">No audit log entries.</p>
          ) : (
            <div className="space-y-2">
              <div className="max-h-96 overflow-y-auto">
                <table className="w-full text-sm" data-testid="audit-log-table">
                  <thead>
                    <tr className="border-b border-border text-left font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                      <th className="pb-2 font-medium">Event</th>
                      <th className="pb-2 font-medium">Entity</th>
                      <th className="pb-2 font-medium">Entity ID</th>
                      <th className="pb-2 font-medium">Actor</th>
                      <th className="pb-2 font-medium">Details</th>
                      <th className="pb-2 font-medium">Time</th>
                    </tr>
                  </thead>
                  <tbody>
                    {auditEntries.map((entry: AuditLogEntry) => {
                      const detailsText = formatDetails(entry.details)
                      return (
                        <tr key={entry.id} className="border-b border-border last:border-0">
                          <td className="py-2">
                            <Badge variant="outline">{entry.event_type}</Badge>
                          </td>
                          <td className="py-2 text-muted-foreground">{entry.entity_type ?? '—'}</td>
                          <td className="py-2 font-mono text-xs text-muted-foreground">{entry.entity_id ?? '—'}</td>
                          <td className="py-2 text-muted-foreground">{entry.actor ?? '—'}</td>
                          <td className="max-w-xs py-2 text-muted-foreground">
                            {detailsText.length > 80 ? (
                              <details className="cursor-pointer">
                                <summary className="truncate text-sm">{detailsText.slice(0, 77)}...</summary>
                                <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap rounded border border-border bg-background p-2 font-mono text-xs">
                                  {detailsText}
                                </pre>
                              </details>
                            ) : (
                              <span className="text-sm">{detailsText}</span>
                            )}
                          </td>
                          <td className="py-2 text-muted-foreground">{formatDateTime(entry.created_at)}</td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
              {auditEntries.length >= auditLimit ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setAuditLimit((prev) => prev + 10)}
                  data-testid="load-more-audit"
                >
                  Load more
                </Button>
              ) : null}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

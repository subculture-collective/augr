import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Power } from 'lucide-react'
import { useState } from 'react'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConsolePanel, HudBadge, HudRow, HudSection, StatusLed } from '@/components/ui/hud'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiClientError, apiClient } from '@/lib/api/client'
import { formatCurrency } from '@/lib/format'
import {
  RISK_COCKPIT_MARKET_ORDER,
  getCircuitBreakerSummaryDisplay,
  getCircuitBreakerDisplay,
  getCockpitMarketLabel,
  getKillSwitchDisplay,
} from '@/lib/risk/presentation'
import type {
  AuditLogEntry,
  EngineStatus,
  RiskCockpitExposure,
  RiskCockpitSummary,
} from '@/lib/api/types'

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
  if (ratio > 0.9) return 'bg-alert'
  if (ratio >= 0.7) return 'bg-caution'
  return 'bg-confirm'
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
      <div className="h-3 overflow-hidden rounded-none border border-border-faint bg-void-900">
        <div
          data-testid={testId}
          className={`h-full rounded-none transition-all ${utilizationBarClass(ratio)}`}
          style={{ width: `${width}%` }}
        />
      </div>
    </div>
  )
}

function CockpitMarketCard({ exposure, marketType }: { exposure?: RiskCockpitExposure; marketType: RiskCockpitExposure['market_type'] }) {
  return (
    <ConsolePanel className="space-y-3 p-3" data-testid={`risk-cockpit-market-${marketType}`}>
      <div className="flex items-start justify-between gap-3">
        <div>
          <HudSection label={getCockpitMarketLabel(marketType)} note={marketType} />
        </div>
        <HudBadge tone={exposure ? 'confirm' : 'caution'}>{exposure ? 'Live' : 'Missing'}</HudBadge>
      </div>
      <div>
        {exposure ? (
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-1">
            <HudRow label="Gross exposure" value={formatSafeCurrency(exposure.gross_exposure)} />
            <HudRow label="Net EV" value={formatSafeCurrency(exposure.net_expected_value)} />
            <HudRow label="Open positions" value={formatCount(exposure.open_positions)} />
            <HudRow label="Approved decisions" value={formatCount(exposure.approved_decisions)} />
            <HudRow label="Rejected decisions" value={formatCount(exposure.rejected_decisions)} className="sm:col-span-2 xl:col-span-1" />
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">No exposure data returned for this market.</p>
        )}
      </div>
    </ConsolePanel>
  )
}

export function RiskPage() {
  const queryClient = useQueryClient()
  const [reason, setReason] = useState('')
  const [showReasonInput, setShowReasonInput] = useState(false)
  const [auditEventType, setAuditEventType] = useState('')
  const [auditActor, setAuditActor] = useState('')
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
    queryKey: ['auditLog', auditEventType.trim(), auditActor.trim(), auditLimit],
    queryFn: () =>
      apiClient.listAuditLog({
        event_type: auditEventType.trim() || undefined,
        actor: auditActor.trim() || undefined,
        limit: auditLimit,
      }),
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
  const circuitBreakerDisplay = circuitBreaker ? getCircuitBreakerDisplay(circuitBreaker) : null
  const killSwitchDisplay = killSwitch ? getKillSwitchDisplay(killSwitch) : null
  const cockpitKillSwitchDisplay = cockpit ? getKillSwitchDisplay({ active: cockpit.kill_switch_active }) : null
  const cockpitCircuitBreakerDisplay = cockpit ? getCircuitBreakerSummaryDisplay(cockpit.circuit_breaker) : null

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

  const engineState = data ? (data.kill_switch.active || data.circuit_breaker.state === 'tripped' ? 'warn' : 'ok') : 'sync'

  return (
    <div className="space-y-5" data-testid="risk-page">
      <PageHeader
        eyebrow="Controls"
        title="Risk engine"
        description="Review circuit breaker state, kill switch controls, exposure utilization, and recent audit events."
        meta={data ? <StatusLed state={engineState} label={data.risk_status} /> : <StatusLed state="sync" label="Loading" />}
      />

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <ConsolePanel className="space-y-4 p-4">
          <HudSection label="Circuit breaker" note="Current state of the circuit breaker" />
            {isLoading && <div data-testid="circuit-breaker-loading" className="h-20 animate-pulse rounded-none border border-border bg-muted/40" />}
            {isError && <p className="text-sm text-destructive">Failed to load: {(error as Error).message}</p>}
            {circuitBreaker && circuitBreakerDisplay ? (
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">State:</span>
                  <Badge variant={circuitBreakerDisplay.variant}>
                    {circuitBreakerDisplay.label}
                  </Badge>
                </div>
                <HudRow label="Reason" value={circuitBreaker.reason || '—'} />
                <HudRow label="Tripped at" value={formatDateTime(circuitBreaker.tripped_at)} />
                <HudRow label="Cooldown ends" value={formatDateTime(circuitBreaker.cooldown_end)} />
              </div>
            ) : null}
        </ConsolePanel>

        <ConsolePanel className="space-y-4 p-4">
          <HudSection label="Kill switch" note="Emergency trading halt" />
            {isLoading && <div data-testid="kill-switch-loading" className="h-20 animate-pulse rounded-none border border-border bg-muted/40" />}
            {isError && <p className="text-sm text-destructive">Failed to load: {(error as Error).message}</p>}
            {killSwitch && killSwitchDisplay ? (
              <div className="space-y-4">
                <div className="space-y-1">
                  <Badge variant={killSwitchDisplay.badgeVariant}>
                    {killSwitchDisplay.badgeLabel}
                  </Badge>
                  <p className="text-sm text-muted-foreground">
                    {killSwitchDisplay.description}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    <span className="font-medium">Updated:</span> {formatDateTime(killSwitch.activated_at)}
                  </p>
                  {killSwitchDisplay.mechanismText ? (
                    <p className="text-xs text-muted-foreground">
                      <span className="font-medium">Mechanism:</span> {killSwitchDisplay.mechanismText}
                    </p>
                  ) : null}
                </div>

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
                        ? 'Resume Trading'
                        : 'Stop All'}
                  </Button>
                ) : null}
              </div>
            ) : null}
        </ConsolePanel>
      </div>

      <ConsolePanel className="space-y-4 p-4">
        <HudSection label="Cross-flow cockpit" note="Read-only cross-asset exposure snapshot with cockpit-wide warnings" />
          {cockpitLoading ? (
            <div data-testid="risk-cockpit-loading" className="space-y-3">
              <div className="h-10 animate-pulse rounded-none border border-border bg-muted/40" />
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                {Array.from({ length: 4 }).map((_, index) => (
                  <div key={index} className="h-52 animate-pulse rounded-none border border-border bg-muted/40" />
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
                <Badge variant={cockpitKillSwitchDisplay?.badgeVariant ?? 'secondary'}>
                  {cockpitKillSwitchDisplay?.badgeLabel ?? 'Unknown'}
                </Badge>
                <Badge variant={cockpitCircuitBreakerDisplay?.badgeVariant ?? 'secondary'}>
                  {cockpitCircuitBreakerDisplay?.badgeLabel ?? 'Unknown'}
                </Badge>
                <span className="text-muted-foreground">Generated {formatDateTime(cockpit.generated_at)}</span>
              </div>

              {cockpitExposures.length === 0 ? (
                <div className="flex flex-col items-center gap-2 border border-border px-4 py-8 text-center" data-testid="risk-cockpit-empty">
                  <p className="text-sm text-muted-foreground">No cross-flow exposures returned.</p>
                </div>
              ) : null}

              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                {RISK_COCKPIT_MARKET_ORDER.map((marketType) => (
                  <CockpitMarketCard key={marketType} marketType={marketType} exposure={cockpitExposureByMarket.get(marketType)} />
                ))}
              </div>

              <div className="border border-border p-3">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium">Warnings</p>
                  <Badge variant={cockpitWarnings.length ? 'warning' : 'secondary'}>
                    {cockpitWarnings.length ? `${cockpitWarnings.length} active` : 'Clear'}
                  </Badge>
                </div>
                {cockpitWarnings.length ? (
                  <ul className="mt-2 space-y-2">
                    {cockpitWarnings.map((warning, index) => (
                      <li key={`${warning}-${index}`} className="rounded-none border border-caution/25 bg-caution/8 px-3 py-2 text-sm text-ink">
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
      </ConsolePanel>

      {data ? (
        <ConsolePanel className="space-y-4 p-4">
          <HudSection label="Position limit utilization" note="Current usage against configured open position and exposure caps" />
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
        </ConsolePanel>
      ) : null}

      <ConsolePanel className="space-y-4 p-4">
        <HudSection label="Audit log" note="Recent system events" />
          <div className="grid gap-3 rounded-lg border border-border bg-background p-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_120px]">
            <div className="space-y-2">
              <Label htmlFor="audit-event-type">Event type</Label>
              <Input
                id="audit-event-type"
                value={auditEventType}
                placeholder="kill_switch_toggled"
                onChange={(event) => setAuditEventType(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="audit-actor">Actor</Label>
              <Input
                id="audit-actor"
                value={auditActor}
                placeholder="system"
                onChange={(event) => setAuditActor(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="audit-limit">Limit</Label>
              <Input
                id="audit-limit"
                type="number"
                min={1}
                max={100}
                value={auditLimit}
                onChange={(event) => {
                  const parsed = Number.parseInt(event.target.value || '1', 10)
                  setAuditLimit(Number.isFinite(parsed) ? Math.min(100, Math.max(1, parsed)) : 1)
                }}
              />
            </div>
          </div>
          {auditLoading ? (
            <div data-testid="audit-log-loading" className="space-y-2">
              {Array.from({ length: 3 }).map((_, index) => (
                <div key={index} className="h-8 animate-pulse rounded-none border border-border bg-muted/40" />
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
            </div>
          )}
      </ConsolePanel>
    </div>
  )
}

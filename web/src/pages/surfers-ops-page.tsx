import { useEffect, useState } from 'react'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { ApiClientError, apiClient } from '@/lib/api/client'
import type { DivergenceResponse, PolymarketStatus, RiskBreakerState } from '@/lib/api/types'

function getApiStatus(error: unknown) {
  return error instanceof ApiClientError ? error.status : (error as { status?: number } | null | undefined)?.status
}

function getApiCode(error: unknown) {
  return error instanceof ApiClientError ? error.code : (error as { code?: string } | null | undefined)?.code
}

function isNotConfigured(error: unknown) {
  const status = getApiStatus(error)
  const code = getApiCode(error)

  if (code === 'ERR_NOT_IMPLEMENTED') {
    return status === 501 || status === 503 || status === undefined
  }

  return status === 501
}

function formatDate(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function loadingBlock(testId: string) {
  return <div data-testid={testId} className="h-20 animate-pulse rounded-none border border-border bg-muted/40" />
}

export function SurfersOpsPage() {
  const [status, setStatus] = useState<PolymarketStatus | null>(null)
  const [statusError, setStatusError] = useState<unknown>(null)
  const [statusLoading, setStatusLoading] = useState(true)

  const [breakers, setBreakers] = useState<RiskBreakerState[]>([])
  const [breakersError, setBreakersError] = useState<unknown>(null)
  const [breakersLoading, setBreakersLoading] = useState(true)

  const [strategyDraft, setStrategyDraft] = useState('')
  const [strategyId, setStrategyId] = useState('')
  const [divergence, setDivergence] = useState<DivergenceResponse | null>(null)
  const [divergenceError, setDivergenceError] = useState<unknown>(null)
  const [divergenceLoading, setDivergenceLoading] = useState(false)

  useEffect(() => {
    let alive = true

    const loadCore = async () => {
      setStatusLoading(true)
      setBreakersLoading(true)
      setStatusError(null)
      setBreakersError(null)

      const [statusResult, breakersResult] = await Promise.allSettled([
        apiClient.getPolymarketMarketDataStatus(),
        apiClient.listRiskBreakers(),
      ])

      if (!alive) return

      if (statusResult.status === 'fulfilled') {
        setStatus(statusResult.value)
      } else {
        setStatus(null)
        setStatusError(statusResult.reason)
      }

      if (breakersResult.status === 'fulfilled') {
        setBreakers(breakersResult.value.tripped)
      } else {
        setBreakers([])
        setBreakersError(breakersResult.reason)
      }

      setStatusLoading(false)
      setBreakersLoading(false)
    }

    void loadCore()
    const id = window.setInterval(loadCore, 5000)

    return () => {
      alive = false
      window.clearInterval(id)
    }
  }, [])

  useEffect(() => {
    let alive = true

    if (!strategyId) {
      setDivergence(null)
      setDivergenceError(null)
      setDivergenceLoading(false)
      return () => {
        alive = false
      }
    }

    const loadDivergence = async () => {
      setDivergenceLoading(true)
      setDivergenceError(null)

      try {
        const result = await apiClient.getBacktestDivergence(strategyId)
        if (!alive) return
        setDivergence(result)
      } catch (error) {
        if (!alive) return
        setDivergence(null)
        setDivergenceError(error)
      } finally {
        if (alive) setDivergenceLoading(false)
      }
    }

    void loadDivergence()
    const id = window.setInterval(loadDivergence, 5000)

    return () => {
      alive = false
      window.clearInterval(id)
    }
  }, [strategyId])

  const statusUnavailable = isNotConfigured(statusError)
  const breakersUnavailable = isNotConfigured(breakersError)
  const divergenceUnavailable = isNotConfigured(divergenceError)
  const divergenceMissing = getApiStatus(divergenceError) === 404
  const statusFailed = Boolean(statusError) && !statusUnavailable
  const breakersFailed = Boolean(breakersError) && !breakersUnavailable
  const divergenceFailed = Boolean(divergenceError) && !divergenceUnavailable && !divergenceMissing

  const feedLabel = statusLoading
    ? 'Loading market feed…'
    : statusUnavailable
      ? 'Feed not configured'
      : statusFailed
        ? 'Feed status unavailable'
        : status?.enabled
          ? 'Feed enabled'
          : 'Feed disabled'

  return (
    <div className="space-y-4" data-testid="surfers-ops-page">
      <PageHeader title="Surfers Ops" description="Polymarket feed, recorder lag, breakers, and divergence" />

      <Card>
        <CardHeader>
          <CardTitle>WS Pool Health</CardTitle>
          <CardDescription>{feedLabel}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          {statusLoading ? (
            loadingBlock('surfers-status-loading')
          ) : statusUnavailable ? (
            <div className="space-y-2 text-muted-foreground" data-testid="surfers-status-unavailable">
              <p>Polymarket feed is not configured on this deployment.</p>
            </div>
          ) : statusFailed ? (
            <div className="space-y-2 text-muted-foreground" data-testid="surfers-status-error">
              <p>Unable to load Polymarket feed status. Retry after checking the API service.</p>
            </div>
          ) : status ? (
            <>
              <div className="flex flex-wrap gap-2">
                <Badge variant={status.enabled ? 'success' : 'warning'}>{status.enabled ? 'Enabled' : 'Disabled'}</Badge>
                <Badge variant="outline">{status.ws_connections} connections</Badge>
                <Badge variant={status.ready_slugs.length ? 'success' : 'secondary'}>
                  {status.ready_slugs.length ? `${status.ready_slugs.length} ready slugs` : 'No ready slugs'}
                </Badge>
              </div>
              <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Jitter</div>
                  <div className="mt-1 font-medium">{status.avg_jitter_ms.toFixed(2)} ms</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Dropped</div>
                  <div className="mt-1 font-medium">{status.dropped}</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Recorder lag</div>
                  <div className="mt-1 font-medium">{status.recorder_lag_seconds.toFixed(2)} s</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Updated</div>
                  <div className="mt-1 font-medium">{formatDate(status.updated_at)}</div>
                </div>
              </div>
              {!status.enabled ? (
                <p className="text-muted-foreground" data-testid="surfers-status-disabled">
                  Feed disabled or not configured; no live market data will arrive until the dependency is enabled.
                </p>
              ) : status.ws_connections === 0 && status.ready_slugs.length === 0 ? (
                <p className="text-muted-foreground">Feed is enabled, but no markets are connected yet.</p>
              ) : null}
            </>
          ) : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Tripped Breakers</CardTitle>
          <CardDescription>
            {breakersLoading
              ? 'Loading breaker state…'
              : breakersUnavailable
                ? 'Risk breaker service not configured'
                : breakersFailed
                  ? 'Unable to load breaker state'
                  : breakers.length
                    ? `${breakers.length} active`
                    : 'No current incidents'}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          {breakersLoading ? (
            loadingBlock('surfers-breakers-loading')
          ) : breakersUnavailable ? (
            <div className="space-y-2 text-muted-foreground" data-testid="surfers-breakers-unavailable">
              <p>Risk breaker service is not configured on this deployment.</p>
            </div>
          ) : breakersFailed ? (
            <div className="space-y-2 text-muted-foreground" data-testid="surfers-breakers-error">
              <p>Unable to load risk breaker state. Retry after checking the API service.</p>
            </div>
          ) : breakers.length ? (
            breakers.map((breaker) => (
              <div key={breaker.scope} className="rounded-none border border-border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="destructive">tripped</Badge>
                  <div className="font-medium">{breaker.scope}</div>
                </div>
                <div className="mt-2 text-muted-foreground">{breaker.reason}</div>
                <div className="mt-1 text-xs text-muted-foreground">Tripped {formatDate(breaker.tripped_at)}</div>
                <div className="text-xs text-muted-foreground">Reset {formatDate(breaker.reset_at)}</div>
              </div>
            ))
          ) : (
            <p className="text-muted-foreground">No tripped breakers.</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Divergence Status</CardTitle>
          <CardDescription>
            {strategyId
              ? divergenceLoading
                ? 'Loading divergence…'
                : divergenceUnavailable
                  ? 'Divergence source not configured'
                  : divergenceFailed
                    ? 'Unable to load divergence'
                    : divergenceMissing
                      ? 'No divergence data found for this strategy'
                      : divergence
                        ? 'Divergence loaded'
                        : 'Ready to load'
              : 'Enter a strategy_id to compare live vs backtest divergence'}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <form
            className="flex flex-wrap gap-2"
            onSubmit={(event) => {
              event.preventDefault()
              setStrategyId(strategyDraft.trim())
            }}
          >
            <Input
              value={strategyDraft}
              onChange={(event) => setStrategyDraft(event.target.value)}
              placeholder="strategy_id"
              aria-label="Strategy ID"
            />
            <Button type="submit">Load</Button>
          </form>

          {strategyId ? (
            divergenceLoading ? (
              loadingBlock('surfers-divergence-loading')
            ) : divergenceUnavailable ? (
              <div className="space-y-2 text-muted-foreground" data-testid="surfers-divergence-unavailable">
                <p>Divergence source is not configured on this deployment.</p>
              </div>
            ) : divergenceFailed ? (
              <div className="space-y-2 text-muted-foreground" data-testid="surfers-divergence-error">
                <p>Unable to load divergence for this strategy. Retry after checking the API service.</p>
              </div>
            ) : divergenceMissing ? (
              <div className="space-y-2 text-muted-foreground" data-testid="surfers-divergence-empty">
                <p>No divergence data exists for this strategy.</p>
              </div>
            ) : divergence ? (
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4 text-sm">
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Status</div>
                  <div className="mt-1 font-medium">{divergence.status}</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Tolerance</div>
                  <div className="mt-1 font-medium">{divergence.tolerance.toFixed(2)}</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Backtest samples</div>
                  <div className="mt-1 font-medium">{divergence.backtest.samples}</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Live samples</div>
                  <div className="mt-1 font-medium">{divergence.live.samples}</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Backtest fill / win</div>
                  <div className="mt-1 font-medium">
                    {divergence.backtest.fill_rate.toFixed(2)} / {divergence.backtest.win_rate.toFixed(2)}
                  </div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Live fill / win</div>
                  <div className="mt-1 font-medium">
                    {divergence.live.fill_rate.toFixed(2)} / {divergence.live.win_rate.toFixed(2)}
                  </div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Max abs delta</div>
                  <div className="mt-1 font-medium">{divergence.max_abs_delta.toFixed(2)}</div>
                </div>
                <div className="rounded-none border border-border bg-background p-3">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Strategy</div>
                  <div className="mt-1 font-mono text-xs">{divergence.strategy_id}</div>
                </div>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">Load a strategy to inspect divergence.</p>
            )
          ) : (
            <p className="text-sm text-muted-foreground">No divergence loaded.</p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

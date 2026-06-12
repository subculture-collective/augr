import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowLeft, Pause, Play, SkipForward, Trash2, Zap } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { StrategyConfigEditor } from '@/components/strategies/strategy-config-editor'
import { StrategyRunHistory } from '@/components/strategies/strategy-run-history'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useWebSocketClient } from '@/hooks/use-websocket-client'
import { ApiClientError, apiClient } from '@/lib/api/client'
import type { PipelineErrorData, StrategyStatus, StrategyUpdateRequest } from '@/lib/api/types'
import { describeCron } from '@/lib/cron-describe'
import { strategyConfigBoundary } from '@/lib/strategy-config/boundary'

function resolveStrategyStatus(strategy: { status?: StrategyStatus; is_active?: boolean }): StrategyStatus {
  if (strategy.status) {
    return strategy.status
  }

  return strategy.is_active ? 'active' : 'inactive'
}

function statusBadgeVariant(status: StrategyStatus): 'success' | 'warning' | 'secondary' {
  switch (status) {
    case 'active':
      return 'success'
    case 'paused':
      return 'warning'
    default:
      return 'secondary'
  }
}

export function StrategyDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [actionError, setActionError] = useState<string | null>(null)
  const [lastPipelineError, setLastPipelineError] = useState<PipelineErrorData | null>(null)

  useWebSocketClient({
    onMessage: (msg) => {
      if (!('type' in msg) || !('strategy_id' in msg)) return
      const wsMsg = msg as { type: string; strategy_id?: string; data?: PipelineErrorData }
      if ((wsMsg.type === 'error' || wsMsg.type === 'pipeline_health') && wsMsg.strategy_id === id && wsMsg.data) {
        setLastPipelineError(wsMsg.data)
      }
    },
  })

  const { data: strategy, isLoading, isError } = useQuery({
    queryKey: ['strategy', id],
    queryFn: () => apiClient.getStrategy(id!),
    enabled: !!id,
  })

  const strategyStatus = useMemo(
    () => (strategy ? resolveStrategyStatus(strategy) : 'inactive'),
    [strategy],
  )
  const isStrategyActive = strategyStatus === 'active'
  const isStrategyPaused = strategyStatus === 'paused'

  function handleMutationError(err: unknown) {
    if (err instanceof ApiClientError && err.status === 409) {
      setActionError(err.message)
      return
    }

    if (err instanceof Error) {
      setActionError(err.message)
      return
    }

    setActionError('Unable to update strategy state.')
  }

  const updateMutation = useMutation({
    mutationFn: (data: StrategyUpdateRequest) => apiClient.updateStrategy(id!, data),
    onMutate: () => setActionError(null),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['strategy', id] })
      queryClient.invalidateQueries({ queryKey: ['strategies'] })
    },
    onError: handleMutationError,
  })

  const deleteMutation = useMutation({
    mutationFn: () => apiClient.deleteStrategy(id!),
    onMutate: () => setActionError(null),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['strategies'] })
      navigate('/strategies')
    },
    onError: handleMutationError,
  })

  const runMutation = useMutation({
    mutationFn: () => apiClient.runStrategy(id!),
    onMutate: () => setActionError(null),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['runs', { strategy_id: id }] })
    },
    onError: handleMutationError,
  })

  const pauseMutation = useMutation({
    mutationFn: () => apiClient.pauseStrategy(id!),
    onMutate: () => setActionError(null),
    onSuccess: (updatedStrategy) => {
      queryClient.setQueryData(['strategy', id], updatedStrategy)
      queryClient.invalidateQueries({ queryKey: ['strategy', id] })
      queryClient.invalidateQueries({ queryKey: ['strategies'] })
    },
    onError: handleMutationError,
  })

  const resumeMutation = useMutation({
    mutationFn: () => apiClient.resumeStrategy(id!),
    onMutate: () => setActionError(null),
    onSuccess: (updatedStrategy) => {
      queryClient.setQueryData(['strategy', id], updatedStrategy)
      queryClient.invalidateQueries({ queryKey: ['strategy', id] })
      queryClient.invalidateQueries({ queryKey: ['strategies'] })
    },
    onError: handleMutationError,
  })

  const skipMutation = useMutation({
    mutationFn: () => apiClient.skipNextRun(id!),
    onMutate: () => setActionError(null),
    onSuccess: (updatedStrategy) => {
      queryClient.setQueryData(['strategy', id], updatedStrategy)
      queryClient.invalidateQueries({ queryKey: ['strategy', id] })
      queryClient.invalidateQueries({ queryKey: ['strategies'] })
    },
    onError: handleMutationError,
  })
  const isLifecycleActionPending = pauseMutation.isPending || resumeMutation.isPending || skipMutation.isPending
  const pauseButtonLabel = isLifecycleActionPending
    ? 'Pause unavailable while another lifecycle action is in progress.'
    : isStrategyActive
      ? 'Pause strategy'
      : isStrategyPaused
        ? 'Pause is unavailable because this strategy is already paused.'
        : 'Pause is unavailable until the strategy is active.'
  const resumeButtonLabel = isLifecycleActionPending
    ? 'Resume unavailable while another lifecycle action is in progress.'
    : isStrategyPaused
      ? 'Resume strategy'
      : isStrategyActive
        ? 'Resume is unavailable because this strategy is already active.'
        : 'Resume is unavailable until the strategy is paused.'

  const { data: ordersData, isLoading: isOrdersLoading, isError: isOrdersError } = useQuery({
    queryKey: ['strategy-orders', id, strategy?.ticker],
    queryFn: () => apiClient.listOrders({ ticker: strategy?.ticker, limit: 5 }),
    enabled: !!strategy?.ticker,
  })

  const { data: backtestsData, isLoading: isBacktestsLoading, isError: isBacktestsError } = useQuery({
    queryKey: ['strategy-backtests', id],
    queryFn: () => apiClient.listBacktestConfigs({ strategy_id: id }),
    enabled: !!id,
  })

  const strategyConfigView = useMemo(
    () => strategyConfigBoundary.view(strategy?.config, strategy?.schedule_cron),
    [strategy?.config, strategy?.schedule_cron],
  )
  const rulesEngineView = strategyConfigView.rulesEngine

  if (isLoading) {
    return (
      <div className="space-y-6" data-testid="strategy-detail-loading">
        <div className="h-8 w-48 animate-pulse rounded bg-muted" />
        <div className="h-64 animate-pulse rounded-lg border bg-muted" />
      </div>
    )
  }

  if (isError || !strategy) {
    return (
      <div className="space-y-4" data-testid="strategy-detail-error">
        <Link to="/strategies" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="size-4" />
          Back to strategies
        </Link>
        <Card>
          <CardContent className="py-8">
            <p className="text-center text-sm text-muted-foreground">
              Unable to load strategy. It may have been deleted or the API server is unavailable.
            </p>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-4" data-testid="strategy-detail-page">
      <PageHeader
        eyebrow="Strategy detail"
        title={strategy.name}
        description={strategy.description || 'Execution settings, recent runs, and lifecycle controls for this strategy.'}
        meta={(
          <>
            <Badge variant={statusBadgeVariant(strategyStatus)} data-testid="strategy-status-badge">
              {strategyStatus}
            </Badge>
            {strategy.is_paper ? <Badge variant="warning">paper</Badge> : null}
            {strategy.skip_next_run ? <Badge variant="outline">skip next queued</Badge> : null}
            {lastPipelineError?.timed_out && (
              <Badge variant="destructive" className="gap-1" title="Last run hit a deadline timeout">
                <AlertTriangle className="size-3" />
                timeout
              </Badge>
            )}
            {lastPipelineError?.used_fallback && (
              <Badge variant="outline" className="gap-1 border-amber-500 text-amber-500" title="Last run used fallback LLM model">
                <Zap className="size-3" />
                fallback model
              </Badge>
            )}
          </>
        )}
        actions={(
          <>
            <Link
              to="/strategies"
              className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:border-primary/25 hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
              Back
            </Link>
            <Button
              variant="outline"
              onClick={() => runMutation.mutate()}
              disabled={runMutation.isPending}
              data-testid="run-strategy-button"
            >
              <Play className="mr-2 size-4" />
              {runMutation.isPending ? 'Running…' : 'Run now'}
            </Button>
            <Button
              variant="outline"
              onClick={() => pauseMutation.mutate()}
              disabled={!isStrategyActive || isLifecycleActionPending}
              data-testid="pause-strategy-button"
              aria-label={pauseButtonLabel}
              title={pauseButtonLabel}
            >
              <Pause className="mr-2 size-4" />
              {pauseMutation.isPending ? 'Pausing…' : 'Pause'}
            </Button>
            <Button
              variant="default"
              onClick={() => resumeMutation.mutate()}
              disabled={!isStrategyPaused || isLifecycleActionPending}
              data-testid="resume-strategy-button"
              aria-label={resumeButtonLabel}
              title={resumeButtonLabel}
            >
              <Play className="mr-2 size-4" />
              {resumeMutation.isPending ? 'Resuming…' : 'Resume'}
            </Button>
            <Button
              variant="ghost"
              onClick={() => skipMutation.mutate()}
              disabled={!isStrategyActive || isLifecycleActionPending}
              data-testid="skip-next-button"
            >
              <SkipForward className="mr-2 size-4" />
              {skipMutation.isPending ? 'Skipping…' : 'Skip next'}
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                if (window.confirm('Delete this strategy and all of its history?')) {
                  deleteMutation.mutate()
                }
              }}
              disabled={deleteMutation.isPending}
              data-testid="delete-strategy-button"
            >
              <Trash2 className="mr-2 size-4" />
              {deleteMutation.isPending ? 'Deleting…' : 'Delete'}
            </Button>
          </>
        )}
      />
      {actionError ? (
        <p className="text-sm text-destructive" data-testid="strategy-action-error">{actionError}</p>
      ) : null}

      <Card data-testid="strategy-human-summary">
        <CardHeader>
          <CardTitle>Strategy summary</CardTitle>
          <CardDescription>Purpose, execution mode, schedule, and guardrails</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <p className="text-sm text-muted-foreground">Purpose / description</p>
            <p className="text-sm leading-6">{strategy.description || 'No strategy description provided.'}</p>
          </div>

          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Ticker / market</dt>
              <dd className="mt-1 flex flex-wrap items-center gap-2 text-sm font-medium">
                <Link to={`/stocks/${strategy.ticker}`} className="text-primary hover:underline">
                  {strategy.ticker}
                </Link>
                <Badge variant={strategy.market_type === 'stock' ? 'default' : strategy.market_type === 'crypto' ? 'secondary' : 'outline'}>
                  {strategy.market_type}
                </Badge>
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Schedule</dt>
              <dd className="mt-1 text-sm font-medium">
                {strategy.schedule_cron ? describeCron(strategy.schedule_cron) : 'Manual only'}
                {strategy.schedule_cron ? (
                  <span className="ml-2 font-mono text-[11px] text-muted-foreground">{strategy.schedule_cron}</span>
                ) : null}
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Mode</dt>
              <dd className="flex items-center gap-2">
                {strategy.is_paper ? <Badge variant="warning">paper</Badge> : null}
                {!strategy.is_paper ? <Badge variant="success">live</Badge> : null}
                <span className="text-sm text-muted-foreground">{strategy.is_paper ? 'Paper trading' : 'Live trading'}</span>
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Status / skip-next</dt>
              <dd className="mt-1 flex flex-wrap items-center gap-2">
                <Badge variant={statusBadgeVariant(strategyStatus)}>{strategyStatus}</Badge>
                <Badge variant={strategy.skip_next_run ? 'outline' : 'secondary'}>
                  {strategy.skip_next_run ? 'skip-next queued' : 'skip-next clear'}
                </Badge>
              </dd>
            </div>
          </dl>

          <div className="space-y-2">
            <p className="text-sm font-medium">Risk / parameter hints</p>
            {rulesEngineView?.riskHints.length ? (
              <ul className="space-y-1 text-sm text-muted-foreground">
                {rulesEngineView.riskHints.map((hint) => (
                  <li key={hint}>{hint}</li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-muted-foreground">No rules-engine hints available.</p>
            )}
          </div>
        </CardContent>
      </Card>

      <Card data-testid="strategy-config-summary">
        <CardHeader>
          <CardTitle>Strategy config</CardTitle>
          <CardDescription>Typed view of the editable config payload</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Schedule</dt>
              <dd className="mt-1 text-sm font-medium">
                {strategyConfigView.scheduleCron ? describeCron(strategyConfigView.scheduleCron) : 'Manual only'}
                {strategyConfigView.scheduleCron ? (
                  <span className="ml-2 font-mono text-[11px] text-muted-foreground">{strategyConfigView.scheduleCron}</span>
                ) : null}
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Analysts</dt>
              <dd className="mt-1 text-sm font-medium">
                {strategyConfigView.analysts.labels.length > 0
                  ? strategyConfigView.analysts.labels.join(', ')
                  : 'Default analysts'}
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Prompt overrides</dt>
              <dd className="mt-1 text-sm font-medium">
                {strategyConfigView.promptOverrideCount > 0
                  ? `${strategyConfigView.promptOverrideCount} override${strategyConfigView.promptOverrideCount === 1 ? '' : 's'}`
                  : 'No overrides'}
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Kelly sizing</dt>
              <dd className="mt-1 flex items-center gap-2 text-sm font-medium">
                {strategyConfigView.risk.useKellySizing ? <Badge variant="warning">opted in</Badge> : <Badge variant="secondary">off</Badge>}
              </dd>
            </div>
          </dl>

          <div className="grid gap-3 sm:grid-cols-2">
            <div className="rounded-md border border-border p-3">
              <p className="text-xs uppercase tracking-[0.16em] text-muted-foreground">LLM</p>
              <p className="mt-1 text-sm text-muted-foreground">
                {strategyConfigView.llm.provider || 'global default'}
                {strategyConfigView.llm.deepThinkModel ? ` • deep ${strategyConfigView.llm.deepThinkModel}` : ''}
                {strategyConfigView.llm.quickThinkModel ? ` • quick ${strategyConfigView.llm.quickThinkModel}` : ''}
              </p>
            </div>
            <div className="rounded-md border border-border p-3">
              <p className="text-xs uppercase tracking-[0.16em] text-muted-foreground">Pipeline</p>
              <p className="mt-1 text-sm text-muted-foreground">
                {strategyConfigView.pipeline.debateRounds || '—'} debate rounds • analysis {strategyConfigView.pipeline.analysisTimeoutSeconds || '—'}s • debate {strategyConfigView.pipeline.debateTimeoutSeconds || '—'}s
              </p>
            </div>
          </div>

          {strategyConfigView.risk.hints.length > 0 ? (
            <div className="space-y-2">
              <p className="text-sm font-medium">Risk hints</p>
              <ul className="space-y-1 text-sm text-muted-foreground">
                {strategyConfigView.risk.hints.map((hint) => (
                  <li key={hint}>{hint}</li>
                ))}
              </ul>
            </div>
          ) : null}
        </CardContent>
      </Card>

      {strategy.prediction_market && (
        <Card>
          <CardHeader>
            <CardTitle>Prediction Market</CardTitle>
            <CardDescription>{strategy.prediction_market.question}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* Probability bar */}
            <div>
              <div className="mb-1 flex justify-between text-xs text-muted-foreground">
                <span>YES {(strategy.prediction_market.yes_price * 100).toFixed(1)}%</span>
                <span>NO {(strategy.prediction_market.no_price * 100).toFixed(1)}%</span>
              </div>
              <div className="flex h-3 overflow-hidden rounded-full bg-muted">
                <div
                  className="bg-success transition-all"
                  style={{ width: `${strategy.prediction_market.yes_price * 100}%` }}
                />
              </div>
            </div>

            <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <div>
                <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Volume 24h</dt>
                <dd className="mt-1 font-mono text-sm font-medium">${strategy.prediction_market.volume_24h.toLocaleString(undefined, { maximumFractionDigits: 0 })}</dd>
              </div>
              <div>
                <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Liquidity</dt>
                <dd className="mt-1 font-mono text-sm font-medium">${strategy.prediction_market.liquidity.toLocaleString(undefined, { maximumFractionDigits: 0 })}</dd>
              </div>
              {strategy.prediction_market.best_ask_yes != null && strategy.prediction_market.best_bid_yes != null && (
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">YES Spread</dt>
                  <dd className="mt-1 font-mono text-sm font-medium">
                    {strategy.prediction_market.best_bid_yes.toFixed(3)} / {strategy.prediction_market.best_ask_yes.toFixed(3)}
                  </dd>
                </div>
              )}
              {strategy.prediction_market.end_date && (
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Resolves</dt>
                  <dd className="mt-1 text-sm font-medium">
                    {new Date(strategy.prediction_market.end_date).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}
                    <span className="ml-1 text-xs text-muted-foreground">
                      ({Math.max(0, Math.ceil((new Date(strategy.prediction_market.end_date).getTime() - Date.now()) / 86400000))}d)
                    </span>
                  </dd>
                </div>
              )}
            </dl>

            {strategy.prediction_market.resolution_criteria && (
              <div>
                <dt className="mb-1 font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Resolution criteria</dt>
                <dd className="text-sm text-muted-foreground">{strategy.prediction_market.resolution_criteria}</dd>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {rulesEngineView ? (
        <Card data-testid="strategy-rules-table">
          <CardHeader>
            <CardTitle>Rules engine</CardTitle>
            <CardDescription>{rulesEngineView.summary}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {(rulesEngineView.name || rulesEngineView.description) && (
              <div className="space-y-1">
                {rulesEngineView.name ? <p className="font-medium">{rulesEngineView.name}</p> : null}
                {rulesEngineView.description ? <p className="text-sm text-muted-foreground">{rulesEngineView.description}</p> : null}
              </div>
            )}

            {rulesEngineView.rows.length > 0 ? (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border text-left font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                      <th className="pb-2 font-medium">Group</th>
                      <th className="pb-2 font-medium">Field</th>
                      <th className="pb-2 font-medium">Operator</th>
                      <th className="pb-2 font-medium">Value/reference</th>
                      <th className="pb-2 font-medium">Explanation</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rulesEngineView.rows.map((row, index) => (
                      <tr key={`${row.group}-${row.field}-${row.operator}-${index}`} className="border-b border-border last:border-0">
                        <td className="py-2 text-xs font-medium uppercase tracking-[0.16em] text-muted-foreground">{row.group}</td>
                        <td className="py-2 font-mono text-[13px]">{row.field}</td>
                        <td className="py-2 font-mono text-[13px]">{row.operator}</td>
                        <td className="py-2 font-mono text-[13px]">{row.value}</td>
                        <td className="py-2 text-sm text-muted-foreground">{row.explanation}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">No entry or exit conditions are defined yet.</p>
            )}

            <div className="grid gap-3 sm:grid-cols-2">
              {rulesEngineView.details.map((detail) => (
                <div key={detail.label} className="rounded-md border border-border p-3">
                  <p className="text-xs uppercase tracking-[0.16em] text-muted-foreground">{detail.label}</p>
                  <p className="mt-1 text-sm text-muted-foreground">{detail.text}</p>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-4 xl:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
        <StrategyRunHistory strategyId={strategy.id} />
        <StrategyConfigEditor
          strategy={strategy}
          onSave={(data) => updateMutation.mutate(data)}
          isSaving={updateMutation.isPending}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Backtests</CardTitle>
          <CardDescription>Backtest configurations linked to this strategy</CardDescription>
        </CardHeader>
        <CardContent>
          {isBacktestsLoading ? (
            <p className="text-sm text-muted-foreground">Loading backtests…</p>
          ) : isBacktestsError ? (
            <p className="text-sm text-muted-foreground">Backtests unavailable right now.</p>
          ) : (backtestsData?.data?.length ?? 0) === 0 ? (
            <div className="space-y-2 rounded-lg border border-dashed border-border bg-background p-4 text-sm text-muted-foreground" data-testid="strategy-backtests-empty">
              <p>No linked backtests yet</p>
              <Link to="/backtests" className="inline-flex items-center gap-1 text-primary hover:underline">
                Browse backtests
              </Link>
            </div>
          ) : (
            <ul className="space-y-2">
              {backtestsData!.data.map((config) => (
                <li key={config.id} className="flex items-center justify-between rounded-md border border-border p-3">
                  <div>
                    <Link to={`/backtests/${config.id}`} className="text-sm font-medium text-primary hover:underline">
                      {config.name}
                    </Link>
                    {config.description && <p className="text-xs text-muted-foreground">{config.description}</p>}
                  </div>
                  <span className="text-xs text-muted-foreground">
                    {new Date(config.start_date).toLocaleDateString()} &ndash; {new Date(config.end_date).toLocaleDateString()}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent Orders</CardTitle>
          <CardDescription>
            Last 5 orders for {strategy.ticker}. The API only exposes ticker-level order history.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isOrdersLoading ? (
            <p className="text-sm text-muted-foreground">Loading recent orders…</p>
          ) : isOrdersError ? (
            <p className="text-sm text-muted-foreground">Recent orders unavailable right now.</p>
          ) : (ordersData?.data?.length ?? 0) === 0 ? (
            <div className="space-y-2 rounded-lg border border-dashed border-border bg-background p-4 text-sm text-muted-foreground" data-testid="strategy-orders-empty">
              <p>No recent orders for {strategy.ticker}</p>
              <p>The order API is ticker-level, so this list is filtered by ticker instead of strategy.</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border text-left font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                    <th className="pb-2 font-medium">Ticker</th>
                    <th className="pb-2 font-medium">Side</th>
                    <th className="pb-2 font-medium">Status</th>
                    <th className="pb-2 font-medium">Date</th>
                  </tr>
                </thead>
                <tbody>
                  {ordersData!.data.map((order) => (
                    <tr key={order.id} className="border-b border-border last:border-0">
                      <td className="py-2 font-mono text-[13px]">
                        <Link to={`/orders/${order.id}`} className="text-primary hover:underline">
                          {order.ticker}
                        </Link>
                      </td>
                      <td className="py-2">
                        <Badge variant={order.side === 'buy' ? 'success' : 'destructive'}>{order.side}</Badge>
                      </td>
                      <td className="py-2">
                        <Badge variant="outline">{order.status}</Badge>
                      </td>
                      <td className="py-2 text-muted-foreground">{new Date(order.created_at).toLocaleDateString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

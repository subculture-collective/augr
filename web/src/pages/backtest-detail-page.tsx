import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Pencil, Play, Trash2 } from 'lucide-react'
import { type FormEvent, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'

import { BacktestEquityChart } from '@/components/backtests/backtest-equity-chart'
import { BacktestMetricsCard } from '@/components/backtests/backtest-metrics-card'
import { BacktestRunHistory } from '@/components/backtests/backtest-run-history'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiClientError, apiClient } from '@/lib/api/client'
import type { BacktestRun } from '@/lib/api/types'

function backtestRunsEmptyCopy() {
  return {
    title: 'No backtest runs yet',
    body:
      'This configuration has not produced a run. Use Run backtest to generate the first result and populate the history.',
  }
}

function backtestRunsErrorCopy(error: unknown) {
  if (error instanceof ApiClientError && error.status === 501) {
    return {
      title: 'Run history unavailable',
      body:
        'Backtest runs are not configured on this deployment yet. Enable the backtest run API before expecting history here.',
      unavailable: true,
    }
  }

  return {
    title: 'Unable to load run history',
    body:
      error instanceof Error && error.message.trim()
        ? error.message
        : 'The run history request failed. Retry after checking the API service.',
    unavailable: false,
  }
}

export function BacktestDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [selectedRun, setSelectedRun] = useState<BacktestRun | null>(null)
  const [editOpen, setEditOpen] = useState(false)
  const [editName, setEditName] = useState('')
  const [editStartDate, setEditStartDate] = useState('')
  const [editEndDate, setEditEndDate] = useState('')
  const [editInitialCapital, setEditInitialCapital] = useState('')

  const deleteMutation = useMutation({
    mutationFn: () => apiClient.deleteBacktestConfig(id!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backtest-configs'] })
      navigate('/backtests')
    },
  })

  const { data: config, isLoading, isError } = useQuery({
    queryKey: ['backtest-config', id],
    queryFn: () => apiClient.getBacktestConfig(id!),
    enabled: !!id,
  })

  const {
    data: runsData,
    isError: runsIsError,
    isLoading: runsIsLoading,
    error: runsError,
    refetch: refetchRuns,
  } = useQuery({
    queryKey: ['backtest-runs', { config_id: id }],
    queryFn: () => apiClient.listBacktestRuns({ backtest_config_id: id }),
    enabled: !!id,
    refetchInterval: 15_000,
  })
  const runs = runsData?.data ?? []

  const runMutation = useMutation({
    mutationFn: () => apiClient.runBacktestConfig(id!),
    onSuccess: (run) => {
      setSelectedRun(run)
      queryClient.invalidateQueries({ queryKey: ['backtest-runs', { config_id: id }] })
    },
  })

  const editMutation = useMutation({
    mutationFn: (data: { name: string; start_date: string; end_date: string; simulation: { initial_capital: number } }) =>
      apiClient.updateBacktestConfig(id!, {
        ...data,
        strategy_id: config!.strategy_id,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['backtest-config', id] })
      queryClient.invalidateQueries({ queryKey: ['backtest-configs'] })
      setEditOpen(false)
    },
  })

  useEffect(() => {
    if (editOpen && config) {
      setEditName(config.name)
      setEditStartDate(config.start_date.split('T')[0])
      setEditEndDate(config.end_date.split('T')[0])
      setEditInitialCapital(String(config.simulation.initial_capital))
    }
  }, [editOpen, config])

  function handleEditSubmit(e: FormEvent) {
    e.preventDefault()
    editMutation.mutate({
      name: editName,
      start_date: editStartDate ? `${editStartDate}T12:00:00Z` : '',
      end_date: editEndDate ? `${editEndDate}T12:00:00Z` : '',
      simulation: { initial_capital: Number(editInitialCapital) },
    })
  }

  if (isLoading) {
    return (
      <div className="space-y-6" data-testid="backtest-detail-loading">
        <div className="h-8 w-48 animate-pulse rounded bg-muted" />
        <div className="h-64 animate-pulse rounded-lg border bg-muted" />
      </div>
    )
  }

  if (isError || !config) {
    return (
      <div className="space-y-4" data-testid="backtest-detail-error">
        <Link to="/backtests" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="size-4" />
          Back to backtests
        </Link>
        <Card>
          <CardContent className="py-8">
            <p className="text-center text-sm text-muted-foreground">
              Unable to load backtest config. It may have been deleted or the API server is unavailable.
            </p>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-4" data-testid="backtest-detail-page">
      <PageHeader
        title={config.name}
        description={config.description || 'Backtest configuration details and run history.'}
        actions={(
          <>
            <Link
              to="/backtests"
              className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:border-primary/25 hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
              Back
            </Link>
            <Button
              variant="outline"
              onClick={() => runMutation.mutate()}
              disabled={runMutation.isPending}
              data-testid="run-backtest-button"
            >
              <Play className="mr-2 size-4" />
              {runMutation.isPending ? 'Running...' : 'Run backtest'}
            </Button>
            <Button
              variant="outline"
              onClick={() => setEditOpen(true)}
              data-testid="edit-backtest-button"
            >
              <Pencil className="mr-2 size-4" />
              Edit
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                if (window.confirm('Delete this backtest config and all its runs?')) {
                  deleteMutation.mutate()
                }
              }}
              disabled={deleteMutation.isPending}
              data-testid="delete-backtest-button"
            >
              <Trash2 className="mr-2 size-4" />
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        )}
      />

      <Card>
        <CardHeader>
          <CardTitle>Configuration</CardTitle>
          <CardDescription>Backtest parameters</CardDescription>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Start Date</dt>
              <dd className="mt-1 text-sm font-medium">{new Date(config.start_date).toLocaleDateString()}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">End Date</dt>
              <dd className="mt-1 text-sm font-medium">{new Date(config.end_date).toLocaleDateString()}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Initial Capital</dt>
              <dd className="mt-1 text-sm font-medium">${config.simulation.initial_capital.toLocaleString()}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Created</dt>
              <dd className="mt-1 text-sm font-medium">{new Date(config.created_at).toLocaleDateString()}</dd>
            </div>
          </dl>
        </CardContent>
      </Card>

      <p className="text-sm text-muted-foreground" data-testid="backtest-config-note">
        Top card shows the configuration only. Metrics, equity curve, and trade log appear after selecting a run.
      </p>

      <Card>
        <CardHeader>
          <CardTitle>Run history</CardTitle>
          <CardDescription>Click a run to view its metrics and equity curve</CardDescription>
        </CardHeader>
        <CardContent>
          {runsIsLoading ? (
            <div className="h-24 animate-pulse rounded-lg border border-border bg-muted/40" data-testid="backtest-runs-loading" />
          ) : runsIsError ? (
            (() => {
              const { title, body, unavailable } = backtestRunsErrorCopy(runsError)

              return (
                <div
                  className="space-y-3 text-center"
                  data-testid={unavailable ? 'backtest-runs-unavailable' : 'backtest-runs-error'}
                >
                  <p className="text-sm font-medium text-foreground">{title}</p>
                  <p className="text-sm text-muted-foreground">{body}</p>
                  <div className="flex flex-wrap items-center justify-center gap-2">
                    <Button type="button" variant="outline" size="sm" onClick={() => void refetchRuns()}>
                      Retry
                    </Button>
                    {unavailable ? (
                      <Button type="button" variant="secondary" size="sm" onClick={() => runMutation.mutate()}>
                        Run backtest
                      </Button>
                    ) : null}
                  </div>
                </div>
              )
            })()
          ) : runs.length === 0 ? (
            <div className="space-y-3 text-center" data-testid="backtest-runs-empty">
              <p className="text-sm font-medium text-foreground">{backtestRunsEmptyCopy().title}</p>
              <p className="text-sm text-muted-foreground">{backtestRunsEmptyCopy().body}</p>
              <div className="flex flex-wrap items-center justify-center gap-2">
                <Button type="button" variant="secondary" size="sm" onClick={() => runMutation.mutate()} data-testid="empty-run-backtest-button">
                  <Play className="mr-2 size-4" />
                  Run backtest
                </Button>
                <Button type="button" variant="outline" size="sm" onClick={() => void refetchRuns()}>
                  Refresh
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-4">
              <BacktestRunHistory
                runs={runs}
                selectedRunId={selectedRun?.id}
                onSelectRun={setSelectedRun}
              />
              {!selectedRun ? (
                <div className="space-y-3 rounded-lg border border-dashed border-border py-10 text-center" data-testid="backtest-run-selection-empty">
                  <p className="text-sm font-medium text-foreground">Select a run to inspect metrics</p>
                  <p className="text-sm text-muted-foreground">Choose any run from the table above to load its metrics and trade log.</p>
                </div>
              ) : null}
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit Backtest Config</DialogTitle>
          </DialogHeader>
          <form onSubmit={handleEditSubmit} className="space-y-4">
            <div className="space-y-2 rounded-lg border border-border bg-background p-4">
              <Label htmlFor="edit-backtest-name">Name</Label>
              <Input
                id="edit-backtest-name"
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                required
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2 rounded-lg border border-border bg-background p-4">
                <Label htmlFor="edit-backtest-start-date">Start Date</Label>
                <Input
                  id="edit-backtest-start-date"
                  type="date"
                  value={editStartDate}
                  onChange={(e) => setEditStartDate(e.target.value)}
                  required
                />
              </div>
              <div className="space-y-2 rounded-lg border border-border bg-background p-4">
                <Label htmlFor="edit-backtest-end-date">End Date</Label>
                <Input
                  id="edit-backtest-end-date"
                  type="date"
                  value={editEndDate}
                  onChange={(e) => setEditEndDate(e.target.value)}
                  required
                />
              </div>
            </div>
            <div className="space-y-2 rounded-lg border border-border bg-background p-4">
              <Label htmlFor="edit-backtest-initial-capital">Initial Capital</Label>
              <Input
                id="edit-backtest-initial-capital"
                type="number"
                min={1}
                value={editInitialCapital}
                onChange={(e) => setEditInitialCapital(e.target.value)}
                required
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setEditOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={editMutation.isPending}>
                {editMutation.isPending ? 'Saving...' : 'Save'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {selectedRun ? (
        <>
          <Card>
            <CardHeader>
              <CardTitle>Run metadata</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Duration</dt>
                  <dd className="mt-1 text-sm font-medium">{selectedRun.duration}</dd>
                </div>
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Prompt Version</dt>
                  <dd className="mt-1 text-sm font-medium font-mono">{selectedRun.prompt_version}</dd>
                </div>
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Prompt Hash</dt>
                  <dd className="mt-1 text-sm font-medium font-mono text-muted-foreground">{selectedRun.prompt_version_hash.slice(0, 12)}</dd>
                </div>
                <div>
                  <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Run Date</dt>
                  <dd className="mt-1 text-sm font-medium">{new Date(selectedRun.run_timestamp).toLocaleString()}</dd>
                </div>
              </dl>
            </CardContent>
          </Card>

          <BacktestMetricsCard metrics={selectedRun.metrics} />

          <Card>
            <CardHeader>
              <CardTitle>Equity curve</CardTitle>
              <CardDescription>
                Run from {new Date(selectedRun.run_timestamp).toLocaleDateString()}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <BacktestEquityChart data={selectedRun.equity_curve ?? []} />
            </CardContent>
          </Card>

          {selectedRun.trade_log && (
            <Card>
              <CardHeader>
                <CardTitle>Trade Log</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b text-left text-muted-foreground">
                        <th className="p-2">Ticker</th>
                        <th className="p-2">Side</th>
                        <th className="p-2">Qty</th>
                        <th className="p-2">Price</th>
                        <th className="p-2">Date</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(Array.isArray(selectedRun.trade_log)
                        ? selectedRun.trade_log
                        : (() => { try { return JSON.parse(selectedRun.trade_log as string) ?? [] } catch { return [] } })()
                      ).map((trade: Record<string, unknown>, i: number) => (
                        <tr key={i} className="border-b border-border/50">
                          <td className="p-2 font-mono">{String(trade.ticker ?? '')}</td>
                          <td className="p-2">
                            <Badge variant={trade.side === 'buy' ? 'success' : 'destructive'}>
                              {String(trade.side ?? '')}
                            </Badge>
                          </td>
                          <td className="p-2 text-right font-mono">{String(trade.quantity ?? '')}</td>
                          <td className="p-2 text-right font-mono">${Number(trade.price).toFixed(2)}</td>
                          <td className="p-2 text-muted-foreground">
                            {trade.executed_at ? new Date(String(trade.executed_at)).toLocaleDateString() : '—'}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>
          )}
        </>
      ) : null}
    </div>
  )
}

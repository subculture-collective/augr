import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, Loader2, ShieldCheck } from 'lucide-react'
import { useMemo } from 'react'
import { Bar, BarChart, Cell, ResponsiveContainer, Tooltip } from 'recharts'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { apiClient } from '@/lib/api/client'
import type { AutomationJobHealth, PipelineRun, Settings } from '@/lib/api/types'

function formatRelativeTime(iso?: string): string {
  if (!iso) return '--'
  const diff = Date.now() - new Date(iso).getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

function formatDuration(ms: number): string {
  if (ms < 60_000) return `${Math.floor(ms / 1000)}s`
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m`
  return `${Math.floor(ms / 3_600_000)}h`
}

function jobStatusBadge(job: AutomationJobHealth) {
  if (!job.enabled) return <Badge variant="secondary">Disabled</Badge>
  if (job.consecutive_failures >= 3) return <Badge variant="destructive">Failing</Badge>
  if (job.consecutive_failures > 0) return <Badge variant="warning">Degraded</Badge>
  return <Badge variant="success">Healthy</Badge>
}

function buildFailureRateSeries(
  runs: { status: string; started_at: string }[],
  bins = 10,
): { bin: string; rate: number; failed: number; total: number }[] {
  if (!runs.length) return []
  const sorted = [...runs].sort(
    (a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime(),
  )
  const perBin = Math.ceil(sorted.length / bins)
  const result = []
  for (let i = 0; i < bins; i++) {
    const chunk = sorted.slice(i * perBin, (i + 1) * perBin)
    if (!chunk.length) break
    const failed = chunk.filter((r) => r.status === 'failed').length
    result.unshift({
      bin: `${i + 1}`,
      rate: chunk.length ? Math.round((failed / chunk.length) * 100) : 0,
      failed,
      total: chunk.length,
    })
  }
  return result
}

const STALE_THRESHOLD_MS = 60 * 60 * 1000

function schemaStatusBadgeVariant(status?: string): 'success' | 'warning' | 'destructive' | 'outline' {
  switch (status?.trim().toLowerCase()) {
    case 'ok':
      return 'success'
    case 'ahead':
      return 'warning'
    case 'behind':
      return 'destructive'
    default:
      return 'outline'
  }
}

function formatSchemaStatus(status?: string): string {
  const normalized = status?.trim().toLowerCase()
  if (!normalized) return 'Unknown'
  return normalized
}

function schemaVersionValue(value?: number): string {
  return value == null ? '--' : String(value)
}

function formatProviderSummary(
  name: string,
  provider: Settings['llm']['providers'][keyof Settings['llm']['providers']],
): string {
  if ('api_key_configured' in provider) {
    const keyLabel = provider.api_key_configured ? 'API key configured' : 'API key missing'
    const last4 = provider.api_key_last4 ? ` • ****${provider.api_key_last4}` : ''
    const baseUrl = provider.base_url ? ` • ${provider.base_url}` : ''
    return `${name}: ${keyLabel}${last4}${baseUrl} • ${provider.model}`
  }

  const baseUrl = provider.base_url ? provider.base_url : 'local'
  return `${name}: ${baseUrl} • ${provider.model}`
}

function providerBadgeVariant(
  provider: Settings['llm']['providers'][keyof Settings['llm']['providers']],
): 'success' | 'warning' | 'outline' {
  if ('api_key_configured' in provider) {
    return provider.api_key_configured ? 'success' : 'warning'
  }

  return provider.base_url ? 'outline' : 'warning'
}

function providerBadgeLabel(
  provider: Settings['llm']['providers'][keyof Settings['llm']['providers']],
): string {
  if ('api_key_configured' in provider) {
    return provider.api_key_configured ? 'configured' : 'missing key'
  }

  return provider.base_url ? 'local' : 'missing url'
}

function runLabel(run: Pick<PipelineRun, 'ticker' | 'status' | 'started_at'>) {
  return `${run.ticker} · ${run.status} · ${formatRelativeTime(run.started_at)}`
}

function SchemaStatusCard({ system }: { system?: Settings['system'] }) {
  const status = formatSchemaStatus(system?.schema_status)

  return (
    <Card data-testid="schema-status-card">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Schema Status
          <Badge variant={schemaStatusBadgeVariant(system?.schema_status)}>{status}</Badge>
        </CardTitle>
        <CardDescription>Current schema version and the required target from settings.</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wider text-muted-foreground">
              Current schema
            </p>
            <p className="font-mono text-lg font-semibold tabular-nums">
              {schemaVersionValue(system?.current_schema_version)}
            </p>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wider text-muted-foreground">
              Required schema
            </p>
            <p className="font-mono text-lg font-semibold tabular-nums">
              {schemaVersionValue(system?.required_schema_version)}
            </p>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function ProviderConfigurationCard({ settings }: { settings?: Settings }) {
  return (
    <Card data-testid="provider-config-card">
      <CardHeader>
        <CardTitle>Provider Configuration</CardTitle>
        <CardDescription>Default provider, model picks, and configured LLM backends.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        {!settings ? (
          <div className="flex items-center gap-2 py-2 text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Loading settings...
          </div>
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="rounded-none border border-border bg-background p-3">
                <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Default</div>
                <div className="mt-1 font-medium">{settings.llm.default_provider}</div>
              </div>
              <div className="rounded-none border border-border bg-background p-3">
                <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Deep think</div>
                <div className="mt-1 font-medium">{settings.llm.deep_think_model}</div>
              </div>
              <div className="rounded-none border border-border bg-background p-3">
                <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Quick think</div>
                <div className="mt-1 font-medium">{settings.llm.quick_think_model}</div>
              </div>
            </div>

            <div className="space-y-2">
              {Object.entries(settings.llm.providers).map(([name, provider]) => (
                <div key={name} className="rounded-none border border-border p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="font-mono text-sm font-medium uppercase tracking-wide">{name}</div>
                    <Badge variant={providerBadgeVariant(provider)}>{providerBadgeLabel(provider)}</Badge>
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">{formatProviderSummary(name, provider)}</div>
                </div>
              ))}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}

function SignalHubCard({ settings }: { settings?: Settings }) {
  const brokers = settings?.system.connected_brokers ?? []
  const heuristicStatus = settings ? (brokers.length > 0 ? 'Heuristic: likely active' : 'Unknown') : '--'

  return (
    <Card data-testid="signal-hub-card">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Websocket / Signal Hub
          <Badge variant={brokers.length > 0 ? 'warning' : 'outline'}>{heuristicStatus}</Badge>
        </CardTitle>
        <CardDescription>
          No direct websocket/signal-hub field is exposed, so this card is a heuristic view.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        {!settings ? (
          <div className="flex items-center gap-2 py-2 text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Loading settings...
          </div>
        ) : (
          <>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="rounded-none border border-border bg-background p-3">
                <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Summary</div>
                <div className="mt-1">Signal hub health is inferred from connected broker metadata only.</div>
              </div>
              <div className="rounded-none border border-border bg-background p-3">
                <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Why no data</div>
                <div className="mt-1">
                  There is no backend websocket or signal-hub field here, so the status can only be heuristic.
                </div>
              </div>
              <div className="rounded-none border border-border bg-background p-3">
                <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Last known state</div>
                <div className="mt-1">
                  {brokers.length > 0
                    ? `${brokers.length} connected broker${brokers.length !== 1 ? 's' : ''}`
                    : 'No connected brokers reported'}
                </div>
              </div>
              <div className="rounded-none border border-border bg-background p-3">
                <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Current status</div>
                <div className="mt-1 font-medium">{heuristicStatus}</div>
              </div>
            </div>

            {brokers.length > 0 ? (
              <div className="space-y-2">
                {brokers.map((broker) => (
                  <div key={broker.name} className="flex flex-wrap items-center justify-between gap-2 rounded-none border border-border p-3">
                    <div>
                      <div className="font-medium">{broker.name}</div>
                      <div className="text-xs text-muted-foreground">
                        {broker.configured ? 'configured' : 'not configured'}
                        {' · '}
                        {broker.paper_mode ? 'paper mode' : 'live mode'}
                      </div>
                    </div>
                    <Badge variant={broker.configured ? 'success' : 'warning'}>
                      {broker.configured ? 'configured' : 'missing'}
                    </Badge>
                  </div>
                ))}
              </div>
            ) : null}
          </>
        )}
      </CardContent>
    </Card>
  )
}

function ActiveRunReconciliationCard({
  runningRuns,
  recentRuns,
}: {
  runningRuns: PipelineRun[]
  recentRuns: PipelineRun[]
}) {
  const recentRunningRuns = recentRuns.filter((run) => run.status === 'running')
  const recentRunIds = new Set(recentRuns.map((run) => run.id))
  const liveRunIds = new Set(runningRuns.map((run) => run.id))
  const overlapCount = runningRuns.filter((run) => recentRunIds.has(run.id)).length
  const liveOnlyRuns = runningRuns.filter((run) => !recentRunIds.has(run.id))
  const historyOnlyRuns = recentRunningRuns.filter((run) => !liveRunIds.has(run.id))
  const oldestRunningMs = runningRuns.length
    ? Date.now() - Math.min(...runningRuns.map((run) => new Date(run.started_at).getTime()))
    : null
  const staleRuns = runningRuns.filter(
    (run) => Date.now() - new Date(run.started_at).getTime() > STALE_THRESHOLD_MS,
  )
  const reconciliationStatus =
    liveOnlyRuns.length || historyOnlyRuns.length || staleRuns.length
      ? 'Needs review'
      : 'Reconciled'

  return (
    <Card data-testid="active-run-reconciliation-card">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Active Runs
          <Badge variant={reconciliationStatus === 'Reconciled' ? 'success' : 'warning'}>
            {reconciliationStatus}
          </Badge>
          {staleRuns.length > 0 && <AlertTriangle className="size-4 text-amber-500" />}
        </CardTitle>
        <CardDescription>Live active runs reconciled against recent history.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-none border border-border bg-background p-3">
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Summary</div>
            <div className="mt-1">
              {runningRuns.length} running live, {recentRunningRuns.length} running in recent history.
            </div>
          </div>
          <div className="rounded-none border border-border bg-background p-3">
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Data source</div>
            <div className="mt-1">This card only uses the latest live and recent run lists from the API.</div>
          </div>
          <div className="rounded-none border border-border bg-background p-3">
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Last known state</div>
            <div className="mt-1">
              {runningRuns.length > 0
                ? `${overlapCount}/${runningRuns.length} live runs matched recent history`
                : 'No active runs reported'}
            </div>
          </div>
          <div className="rounded-none border border-border bg-background p-3">
            <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Current status</div>
            <div className="mt-1">
              {staleRuns.length > 0
                ? `${staleRuns.length} stale active run${staleRuns.length !== 1 ? 's' : ''}`
                : runningRuns.length > 0
                  ? 'Active runs are current'
                  : 'No active runs'}
            </div>
          </div>
        </div>

        {runningRuns.length > 0 ? (
          <>
            <div className="flex flex-wrap gap-2">
              <Badge variant="outline">{runningRuns.length} live</Badge>
              <Badge variant="outline">{recentRunningRuns.length} in history</Badge>
              <Badge variant={staleRuns.length > 0 ? 'warning' : 'success'}>
                {staleRuns.length > 0 ? `${staleRuns.length} stale` : 'No stale runs'}
              </Badge>
            </div>
            <p className="text-xs text-muted-foreground">
              {liveOnlyRuns.length || historyOnlyRuns.length
                ? `Reconciliation gap: ${liveOnlyRuns.length} live-only, ${historyOnlyRuns.length} history-only.`
                : 'Live and recent run lists are aligned.'}
            </p>
            {oldestRunningMs != null && (
              <p className="text-xs text-muted-foreground">Oldest active run: {formatDuration(oldestRunningMs)}</p>
            )}
            {staleRuns.length > 0 ? (
              <p className="text-xs font-medium text-amber-500">
                {staleRuns.length} stale run{staleRuns.length !== 1 ? 's' : ''} (&gt;1h)
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">No stale runs</p>
            )}
          </>
        ) : (
          <p className="text-muted-foreground">No active runs.</p>
        )}
      </CardContent>
    </Card>
  )
}

function LastSuccessfulJobsCard({ jobs }: { jobs: AutomationJobHealth[] }) {
  const successfulJobs = jobs
    .filter((job) => job.enabled && !job.running && job.consecutive_failures === 0 && job.last_run)
    .sort((a, b) => new Date(b.last_run ?? 0).getTime() - new Date(a.last_run ?? 0).getTime())
    .slice(0, 4)

  return (
    <Card data-testid="last-successful-jobs-card">
      <CardHeader>
        <CardTitle>Last Successful Jobs</CardTitle>
        <CardDescription>Derived from automation health jobs with zero consecutive failures.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {successfulJobs.length > 0 ? (
          successfulJobs.map((job) => (
            <div key={job.name} className="rounded-none border border-border p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="font-mono text-sm font-medium uppercase tracking-wide">{job.name}</div>
                <Badge variant="success">healthy</Badge>
              </div>
              <div className="mt-1 text-xs text-muted-foreground">
                Last run {formatRelativeTime(job.last_run)} · {job.run_count} total runs
              </div>
            </div>
          ))
        ) : (
          <p className="text-muted-foreground">No successful jobs recorded yet.</p>
        )}
      </CardContent>
    </Card>
  )
}

function RecentFailuresCard({ runs }: { runs: PipelineRun[] }) {
  const failures = runs
    .filter((run) => run.status === 'failed')
    .sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
    .slice(0, 4)

  return (
    <Card data-testid="recent-failures-card">
      <CardHeader>
        <CardTitle>Recent Failures</CardTitle>
        <CardDescription>Most recent failed runs from the latest run history.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {failures.length > 0 ? (
          failures.map((run) => (
            <div key={run.id} className="rounded-none border border-border p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="font-mono text-sm font-medium uppercase tracking-wide">{runLabel(run)}</div>
                <Badge variant="destructive">failed</Badge>
              </div>
              <div className="mt-1 text-xs text-muted-foreground">
                {run.error_message ? run.error_message : 'No error message reported.'}
              </div>
            </div>
          ))
        ) : (
          <p className="text-muted-foreground">No recent failures in the latest run history.</p>
        )}
      </CardContent>
    </Card>
  )
}

export function ReliabilityPage() {
  const healthQuery = useQuery({
    queryKey: ['automation-health'],
    queryFn: () => apiClient.getAutomationHealth(),
    refetchInterval: 30_000,
  })

  const settingsQuery = useQuery({
    queryKey: ['settings'],
    queryFn: () => apiClient.getSettings(),
    refetchInterval: 60_000,
  })

  const runningRunsQuery = useQuery({
    queryKey: ['runs', { status: 'running', limit: 50 }],
    queryFn: () => apiClient.listRuns({ status: 'running', limit: 50 } as Parameters<typeof apiClient.listRuns>[0]),
    refetchInterval: 30_000,
  })

  const recentRunsQuery = useQuery({
    queryKey: ['runs', { limit: 50 }],
    queryFn: () => apiClient.listRuns({ limit: 50 } as Parameters<typeof apiClient.listRuns>[0]),
    refetchInterval: 60_000,
  })

  const data = healthQuery.data
  const jobs = useMemo(() => data?.jobs ?? [], [data])

  const runningRuns = useMemo(() => runningRunsQuery.data?.data ?? [], [runningRunsQuery.data])
  const recentRuns = useMemo(() => recentRunsQuery.data?.data ?? [], [recentRunsQuery.data])

  const successfulJobs = useMemo(
    () =>
      jobs
        .filter((job) => job.enabled && !job.running && job.consecutive_failures === 0 && job.last_run)
        .sort((a, b) => new Date(b.last_run ?? 0).getTime() - new Date(a.last_run ?? 0).getTime()),
    [jobs],
  )

  const recentFailures = useMemo(
    () => recentRuns.filter((run) => run.status === 'failed').sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime()),
    [recentRuns],
  )

  const failureRateSeries = useMemo(() => buildFailureRateSeries(recentRuns), [recentRuns])

  const overallFailureRate = useMemo(() => {
    if (!recentRuns.length) return null
    const failed = recentRuns.filter((r) => r.status === 'failed').length
    return Math.round((failed / recentRuns.length) * 100)
  }, [recentRuns])

  return (
    <div className="space-y-4" data-testid="reliability-page">
      <PageHeader
        title="Reliability"
        description="Automation health and system status."
        meta={<ShieldCheck className="size-4 text-muted-foreground" />}
      />

      {data && (
        <Card>
          <CardHeader>
            <CardTitle>System Status</CardTitle>
            <CardDescription>Overall automation health from the latest health endpoint.</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="flex items-center gap-3">
              <Badge variant={data.healthy ? 'success' : 'destructive'}>
                {data.healthy ? 'Healthy' : 'Degraded'}
              </Badge>
              <span className="text-sm text-muted-foreground">
                {data.total_jobs} job{data.total_jobs !== 1 ? 's' : ''} total
                {data.failing_jobs > 0 && (
                  <span className="ml-2 text-destructive">
                    · {data.failing_jobs} failing
                  </span>
                )}
                {data.degraded_jobs > 0 && (
                  <span className="ml-2 text-amber-500">
                    · {data.degraded_jobs} degraded
                  </span>
                )}
              </span>
            </div>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
        <SchemaStatusCard system={settingsQuery.data?.system} />

        <ProviderConfigurationCard settings={settingsQuery.data} />

        <SignalHubCard settings={settingsQuery.data} />

        <ActiveRunReconciliationCard runningRuns={runningRuns} recentRuns={recentRuns} />

        <LastSuccessfulJobsCard jobs={successfulJobs} />

        <RecentFailuresCard runs={recentFailures} />

        <Card data-testid="failure-rate-card">
          <CardHeader>
            <CardTitle>Pipeline Failure Rate</CardTitle>
            <CardDescription>Failure percentage across the latest run sample.</CardDescription>
          </CardHeader>
          <CardContent>
            {recentRunsQuery.isLoading ? (
              <div className="flex items-center gap-2 py-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Loading...
              </div>
            ) : (
              <div className="space-y-2">
                <div className="flex items-baseline gap-2">
                  <span className="text-3xl font-bold tabular-nums">
                    {overallFailureRate ?? '--'}
                    {overallFailureRate != null && '%'}
                  </span>
                  <span className="text-sm text-muted-foreground">last {recentRuns.length} runs</span>
                </div>
                {failureRateSeries.length > 1 ? (
                  <ResponsiveContainer width="100%" height={48}>
                    <BarChart data={failureRateSeries} barSize={8}>
                      <Tooltip
                        content={({ active, payload }) => {
                          if (!active || !payload?.length) return null
                          const d = payload[0].payload as { failed: number; total: number; rate: number }
                          return (
                            <div className="rounded border border-border bg-background px-2 py-1 text-xs shadow">
                              {d.failed}/{d.total} failed ({d.rate}%)
                            </div>
                          )
                        }}
                      />
                      <Bar dataKey="rate" radius={[2, 2, 0, 0]}>
                        {failureRateSeries.map((entry, index) => (
                          <Cell
                            key={index}
                            fill={entry.rate > 50 ? 'hsl(var(--destructive))' : entry.rate > 0 ? 'hsl(var(--warning))' : 'hsl(var(--success))'}
                          />
                        ))}
                      </Bar>
                    </BarChart>
                  </ResponsiveContainer>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    {recentRuns.length === 0 ? 'No run history yet' : 'Insufficient data for chart'}
                  </p>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Automation Health</CardTitle>
          <CardDescription>Job-by-job execution health and current running state.</CardDescription>
        </CardHeader>
        <CardContent>
          {healthQuery.isLoading && (
            <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Loading...
            </div>
          )}

          {healthQuery.isError && (
            <p className="py-4 text-sm text-destructive">
              Failed to load automation health.
            </p>
          )}

          {!healthQuery.isLoading && jobs.length === 0 && !healthQuery.isError && (
            <p className="py-4 text-sm text-muted-foreground">
              No automation jobs found.
            </p>
          )}

          {jobs.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs font-medium uppercase tracking-wider text-muted-foreground">
                    <th className="px-2 py-2">Name</th>
                    <th className="px-2 py-2">Status</th>
                    <th className="px-2 py-2">Running</th>
                    <th className="px-2 py-2 text-right">Error Count</th>
                    <th className="px-2 py-2">Last Run</th>
                  </tr>
                </thead>
                <tbody>
                  {jobs.map((job) => (
                    <tr
                      key={job.name}
                      className="border-b border-border/50 hover:bg-accent/30"
                    >
                      <td className="px-2 py-1.5 font-mono font-medium">
                        {job.name}
                      </td>
                      <td className="px-2 py-1.5">
                        {jobStatusBadge(job)}
                      </td>
                      <td className="px-2 py-1.5">
                        {job.running ? (
                          <span className="inline-flex items-center gap-1 text-emerald-400">
                            <span className="inline-block size-2 rounded-full bg-emerald-400" />
                            Yes
                          </span>
                        ) : (
                          <span className="text-muted-foreground">No</span>
                        )}
                      </td>
                      <td className="px-2 py-1.5 text-right font-mono">
                        {job.error_count > 0 ? (
                          <span className="text-destructive">{job.error_count}</span>
                        ) : (
                          job.error_count
                        )}
                      </td>
                      <td className="px-2 py-1.5 text-xs text-muted-foreground">
                        {formatRelativeTime(job.last_run)}
                      </td>
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

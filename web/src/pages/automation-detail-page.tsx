import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Loader2, Play } from 'lucide-react'
import { useMemo } from 'react'
import { Link, useParams } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { apiClient } from '@/lib/api/client'
import type { AutomationJobRun, JobStatus } from '@/lib/api/types'

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

function formatAbsoluteTime(iso?: string): string {
  if (!iso) return '--'
  return new Date(iso).toLocaleString()
}

function formatDurationNs(ns?: number): string {
  if (ns == null) return '--'
  const totalSeconds = Math.max(Math.floor(ns / 1_000_000_000), 0)
  if (totalSeconds < 60) return `${totalSeconds}s`
  const minutes = Math.floor(totalSeconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  return `${days}d`
}

function formatSchedule(schedule: string): string {
  return schedule.trim() || 'Unscheduled'
}

function deriveWorkflowLabels(job: JobStatus): string[] {
  const text = `${job.name} ${job.description} ${job.schedule}`.toLowerCase()
  const labels: string[] = []

  const push = (label: string) => {
    if (!labels.includes(label)) labels.push(label)
  }

  if (/report|digest|summary/.test(text)) push('report')
  if (/monitor|watch|signal|alert/.test(text)) push('monitor')
  if (/sync|refresh|update|fetch/.test(text)) push('sync')
  if (/research|discover|scan|explore/.test(text)) push('research')
  if (/cleanup|archive|prune|trim/.test(text)) push('cleanup')
  if (/risk|limit|breaker/.test(text)) push('risk')
  if (/trade|order|rebalance|execution/.test(text)) push('trade')

  if (/1-5|mon-fri|weekday/i.test(job.schedule)) push('weekday')
  if (/every \d+ minutes|frequent/i.test(job.schedule)) push('frequent')
  if (/hour/i.test(job.schedule)) push('hourly')
  if (/daily/i.test(job.schedule)) push('daily')

  return labels.length > 0 ? labels.slice(0, 3) : ['general']
}

function formatLastSummary(summary?: Record<string, number>): string {
  if (!summary) return '--'
  const entries = Object.entries(summary)
  if (entries.length === 0) return '--'
  return entries
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => `${key}: ${value}`)
    .join(' · ')
}

function formatPercent(part: number, total: number): string {
  if (total <= 0) return '--'
  return `${Math.round((part / total) * 100)}%`
}

function formatResult(result: string): { label: string; variant: 'secondary' | 'success' | 'destructive' | 'warning' } {
  if (!result) return { label: '--', variant: 'secondary' }
  if (result.startsWith('ok')) return { label: 'success', variant: 'success' }
  if (result.startsWith('error')) return { label: 'failed', variant: 'destructive' }
  return { label: result, variant: 'warning' }
}

function runStatusBadge(run: AutomationJobRun) {
  return <Badge variant={run.status.startsWith('ok') ? 'success' : run.status.startsWith('error') ? 'destructive' : 'secondary'}>{run.status}</Badge>
}

export function AutomationDetailPage() {
  const { name } = useParams<{ name: string }>()
  const queryClient = useQueryClient()

  const { data: allJobs, isLoading, isError } = useQuery({
    queryKey: ['automation-status'],
    queryFn: () => apiClient.getAutomationStatus(),
    refetchInterval: 5000,
  })

  const runsQuery = useQuery({
    queryKey: ['automation-runs', name],
    queryFn: () => apiClient.listAutomationRuns({ limit: 50 }),
    refetchInterval: 15_000,
    enabled: Boolean(name),
  })

  const job = allJobs?.find((entry) => entry.name === name)

  const runMutation = useMutation({
    mutationFn: (jobName: string) => apiClient.runAutomationJob(jobName),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['automation-status'] })
      queryClient.invalidateQueries({ queryKey: ['automation-runs', name] })
    },
  })

  const enableMutation = useMutation({
    mutationFn: ({ jobName, enabled }: { jobName: string; enabled: boolean }) =>
      apiClient.setAutomationJobEnabled(jobName, enabled),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['automation-status'] })
    },
  })

  const jobRuns = useMemo(() => {
    const runs = runsQuery.data?.data ?? []
    return runs
      .filter((run) => run.job_name === name)
      .slice()
      .sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
  }, [name, runsQuery.data])

  const summary = useMemo(() => {
    const runCount = job?.run_count ?? 0
    const errorCount = job?.error_count ?? 0
    const successCount = Math.max(runCount - errorCount, 0)
    const consecutiveFailures = job?.consecutive_failures ?? null
    return {
      runCount,
      errorCount,
      successRate: formatPercent(successCount, runCount),
      errorRate: formatPercent(errorCount, runCount),
      consecutiveFailures,
    }
  }, [job])

  if (isLoading) {
    return (
      <div className="space-y-6" data-testid="automation-detail-loading">
        <div className="h-8 w-48 animate-pulse rounded bg-muted" />
        <div className="h-64 animate-pulse rounded-lg border bg-muted" />
      </div>
    )
  }

  if (isError || !job) {
    return (
      <div className="space-y-4" data-testid="automation-detail-error">
        <Link to="/automation" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="size-4" />
          Back to Automation
        </Link>
        <Card>
          <CardContent className="py-8">
            <p className="text-center text-sm text-muted-foreground">
              Unable to load job. It may not exist or the API server is unavailable.
            </p>
          </CardContent>
        </Card>
      </div>
    )
  }

  const lastResult = formatResult(job.last_result)

  return (
    <div className="space-y-4" data-testid="automation-detail-page">
      <PageHeader
        title={job.name}
        description={job.description}
        meta={
          <>
            {job.running ? <Badge variant="success">running</Badge> : <Badge variant="secondary">idle</Badge>}
            {!job.enabled && <Badge variant="warning">disabled</Badge>}
          </>
        }
        actions={
          <>
            <Link
              to="/automation"
              className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:border-primary/25 hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
              Back
            </Link>
            <Button
              size="sm"
              variant="outline"
              disabled={job.running || runMutation.isPending}
              onClick={() => runMutation.mutate(job.name)}
              data-testid="automation-run-button"
            >
              {job.running ? <Loader2 className="mr-1 size-4 animate-spin" /> : <Play className="mr-1 size-4" />}
              {runMutation.isPending ? 'Running...' : 'Run now'}
            </Button>
            <Button
              size="sm"
              variant={job.enabled ? 'outline' : 'secondary'}
              disabled={enableMutation.isPending}
              onClick={() => enableMutation.mutate({ jobName: job.name, enabled: !job.enabled })}
              data-testid="automation-toggle-button"
            >
              {job.enabled ? 'Disable' : 'Enable'}
            </Button>
          </>
        }
      />

      <Card data-testid="automation-overview-card">
        <CardHeader>
          <CardTitle>Overview</CardTitle>
          <CardDescription>Schedule, workflow, and live status in one place.</CardDescription>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Schedule</dt>
              <dd className="mt-1 text-sm font-medium font-mono">{formatSchedule(job.schedule)}</dd>
              <div className="mt-1 font-mono text-[11px] text-muted-foreground">Backend schedule description</div>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Workflow</dt>
              <dd className="mt-1 flex flex-wrap gap-1.5">
                {deriveWorkflowLabels(job).map((label) => (
                  <Badge key={label} variant="outline">
                    {label}
                  </Badge>
                ))}
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Enabled</dt>
              <dd className="mt-1 text-sm font-medium">{job.enabled ? 'Yes' : 'No'}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Last Run</dt>
              <dd className="mt-1 space-y-1">
                <div className="text-sm font-medium">{formatRelativeTime(job.last_run)}</div>
                <Badge variant={lastResult.variant}>{lastResult.label}</Badge>
              </dd>
            </div>
          </dl>
        </CardContent>
      </Card>

      <Card data-testid="automation-detail-stats-card">
        <CardHeader>
          <CardTitle>Stats</CardTitle>
          <CardDescription>Operational counters and state from the backend snapshot.</CardDescription>
        </CardHeader>
        <CardContent>
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Run Count</dt>
              <dd className="mt-1 text-2xl font-semibold font-mono">{summary.runCount}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Success Rate</dt>
              <dd className="mt-1 text-2xl font-semibold font-mono">{summary.successRate}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Error Rate</dt>
              <dd className="mt-1 text-2xl font-semibold font-mono text-destructive">{summary.errorRate}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Errors</dt>
              <dd className="mt-1 text-2xl font-semibold font-mono">{summary.errorCount}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Consecutive Failures</dt>
              <dd className="mt-1 text-sm font-medium">{summary.consecutiveFailures ?? '--'}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Last Error At</dt>
              <dd className="mt-1 space-y-0.5 text-sm font-medium">
                <div>{formatRelativeTime(job.last_error_at)}</div>
                <div className="text-xs text-muted-foreground">{formatAbsoluteTime(job.last_error_at)}</div>
              </dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Stuck For</dt>
              <dd className="mt-1 text-sm font-medium">{formatDurationNs(job.stuck_for)}</dd>
            </div>
            <div>
              <dt className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Last Run Time</dt>
              <dd className="mt-1 text-sm font-medium">{formatAbsoluteTime(job.last_run)}</dd>
            </div>
          </dl>

          <div className="mt-4 grid gap-4 lg:grid-cols-2">
            <div>
              <p className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Last Summary</p>
              <p className="mt-1 text-sm text-muted-foreground" data-testid="automation-last-summary">
                {formatLastSummary(job.last_summary)}
              </p>
            </div>
            <div>
              <p className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Human Description</p>
              <p className="mt-1 text-sm text-muted-foreground">
                {deriveWorkflowLabels(job).join(' · ')} · {formatSchedule(job.schedule)}
              </p>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card data-testid="automation-run-timeline">
        <CardHeader>
          <CardTitle>Last run result timeline</CardTitle>
          <CardDescription>Latest global automation runs, filtered locally for this job.</CardDescription>
        </CardHeader>
        <CardContent>
          {runsQuery.isLoading ? (
            <div className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Loading run history...
            </div>
          ) : runsQuery.isError ? (
            <p className="text-sm text-muted-foreground">Run history unavailable.</p>
          ) : jobRuns.length === 0 ? (
            <p className="text-sm text-muted-foreground">No matching runs in the latest global run window. Older job history may still exist.</p>
          ) : (
            <div className="space-y-3">
              {jobRuns.slice(0, 5).map((run) => (
                <div key={run.id} className="rounded-md border border-border bg-card px-4 py-3">
                  <div className="flex flex-wrap items-center gap-2">
                    {runStatusBadge(run)}
                    <span className="font-mono text-[13px] text-muted-foreground">
                      {formatRelativeTime(run.started_at)} · {formatAbsoluteTime(run.started_at)}
                    </span>
                    <span className="font-mono text-[13px] text-muted-foreground">
                      Duration {formatDurationNs(run.duration_ns)}
                    </span>
                  </div>
                  <div className="mt-2 text-sm text-muted-foreground">
                    Result: {run.error || run.status}
                  </div>
                  {run.last_error_at && (
                    <div className="mt-1 text-xs text-muted-foreground">
                      Last error at {formatAbsoluteTime(run.last_error_at)}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {job.last_error && (
        <Card>
          <CardHeader>
            <CardTitle className="text-destructive">Last Error</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="whitespace-pre-wrap rounded-md border border-destructive/30 bg-destructive/5 px-4 py-3 font-mono text-sm text-destructive">
              {job.last_error}
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  )
}

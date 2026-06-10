import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, Play, Zap } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { apiClient } from '@/lib/api/client'
import type { JobStatus } from '@/lib/api/types'

type JobFilter = 'all' | 'enabled' | 'running' | 'failing' | 'disabled'

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

function formatPercent(part: number, total: number): string {
  if (total <= 0) return '--'
  return `${Math.round((part / total) * 100)}%`
}

function formatRatio(successes: number, errors: number): string {
  if (successes + errors <= 0) return '--'
  return `${successes}:${errors}`
}

function formatScheduleDisplay(schedule: string): string {
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

function workflowSummary(job: JobStatus): string {
  return `${deriveWorkflowLabels(job).join(' · ')} · ${formatScheduleDisplay(job.schedule)}`
}

function isFailing(job: JobStatus): boolean {
  return Boolean(job.last_error) || (job.consecutive_failures ?? 0) > 0
}

function matchesStatusFilter(job: JobStatus, filter: JobFilter): boolean {
  if (filter === 'all') return true
  if (filter === 'enabled') return job.enabled
  if (filter === 'running') return job.running
  if (filter === 'failing') return isFailing(job)
  if (filter === 'disabled') return !job.enabled
  return true
}

function matchesSearch(job: JobStatus, search: string): boolean {
  if (!search.trim()) return true
  const needle = search.trim().toLowerCase()
  return [job.name, job.description, job.schedule, workflowSummary(job)].some((value) =>
    value.toLowerCase().includes(needle),
  )
}

function resultBadge(result: string) {
  if (!result) return <Badge variant="secondary">--</Badge>
  if (result.startsWith('ok')) return <Badge variant="success">success</Badge>
  if (result.startsWith('error')) return <Badge variant="destructive">failed</Badge>
  return <Badge variant="warning">{result}</Badge>
}

export function AutomationPage() {
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<JobFilter>('all')

  const statusQuery = useQuery({
    queryKey: ['automation-status'],
    queryFn: () => apiClient.getAutomationStatus(),
    refetchInterval: 10_000,
  })

  const runMutation = useMutation({
    mutationFn: (name: string) => apiClient.runAutomationJob(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['automation-status'] })
    },
  })

  const enableMutation = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      apiClient.setAutomationJobEnabled(name, enabled),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['automation-status'] })
    },
  })

  const jobs: JobStatus[] = statusQuery.data ?? []

  const filteredJobs = jobs.filter((job) => {
    if (!matchesSearch(job, search)) return false
    return matchesStatusFilter(job, statusFilter)
  })

  const totalJobs = jobs.length
  const enabledJobs = jobs.filter((job) => job.enabled).length
  const runningJobs = jobs.filter((job) => job.running).length
  const failingJobs = jobs.filter((job) => isFailing(job)).length
  const totalRuns = jobs.reduce((sum, job) => sum + job.run_count, 0)
  const totalErrors = jobs.reduce((sum, job) => sum + job.error_count, 0)
  const totalSuccesses = Math.max(totalRuns - totalErrors, 0)
  const summary = {
    totalJobs,
    enabledJobs,
    runningJobs,
    failingJobs,
    totalRuns,
    totalErrors,
    successRate: formatPercent(totalSuccesses, totalRuns),
    errorRate: formatPercent(totalErrors, totalRuns),
    successErrorRatio: formatRatio(totalSuccesses, totalErrors),
  }

  const workflowCounts = new Map<string, number>()
  for (const job of jobs) {
    for (const label of deriveWorkflowLabels(job)) {
      workflowCounts.set(label, (workflowCounts.get(label) ?? 0) + 1)
    }
  }
  const workflowMix = [...workflowCounts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, 4)

  const hasActiveFilters = search.trim().length > 0 || statusFilter !== 'all'

  return (
    <div className="space-y-4" data-testid="automation-page">
      <PageHeader
        title="Automation"
        description="Scheduled jobs and their execution status."
        meta={<Zap className="size-4 text-muted-foreground" />}
      />

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4" data-testid="automation-summary-metrics">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Jobs</CardTitle>
            <CardDescription>Configured automation jobs.</CardDescription>
          </CardHeader>
          <CardContent className="text-3xl font-semibold font-mono tabular-nums">{summary.totalJobs}</CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Success rate</CardTitle>
            <CardDescription>Successful runs / total runs.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-3xl font-semibold font-mono tabular-nums">{summary.successRate}</div>
            <div className="text-xs text-muted-foreground">Success / error ratio {summary.successErrorRatio}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Enabled</CardTitle>
            <CardDescription>Jobs allowed to run.</CardDescription>
          </CardHeader>
          <CardContent className="text-3xl font-semibold font-mono tabular-nums">{summary.enabledJobs}</CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Running</CardTitle>
            <CardDescription>Jobs currently executing.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-1">
            <div className="text-3xl font-semibold font-mono tabular-nums">{summary.runningJobs}</div>
            <div className="text-xs text-muted-foreground">{summary.failingJobs} failing</div>
          </CardContent>
        </Card>
      </div>

      <Card data-testid="automation-workflow-card">
        <CardHeader>
          <CardTitle>Workflow mix</CardTitle>
          <CardDescription>Local labels inferred from job names, descriptions, and schedules.</CardDescription>
        </CardHeader>
        <CardContent>
          {workflowMix.length === 0 ? (
            <p className="text-sm text-muted-foreground">No workflow labels available.</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {workflowMix.map(([label, count]) => (
                <Badge key={label} variant="outline">
                  {label} · {count}
                </Badge>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>View controls</CardTitle>
          <CardDescription>
            Search and filters are local only. Reset view clears this page state; the backend has no reset endpoint.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid gap-3 lg:grid-cols-[1fr_220px_auto]">
            <Input
              aria-label="Search automation jobs"
              placeholder="Search name, description, schedule, or workflow"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <select
              aria-label="Filter automation jobs by status"
              className="flex h-9 w-full rounded-md border border-input bg-card px-3 py-1 text-sm text-foreground transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
              value={statusFilter}
              onChange={(event) => setStatusFilter(event.target.value as typeof statusFilter)}
            >
              <option value="all">All statuses</option>
              <option value="enabled">Enabled</option>
              <option value="running">Running</option>
              <option value="failing">Failing</option>
              <option value="disabled">Disabled</option>
            </select>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setSearch('')
                setStatusFilter('all')
              }}
              disabled={!hasActiveFilters}
              data-testid="automation-reset-view"
            >
              Reset view
            </Button>
          </div>
          <p className="mt-3 text-xs text-muted-foreground" data-testid="automation-local-reset-note">
            Showing {filteredJobs.length} of {jobs.length} jobs.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Jobs</CardTitle>
          <CardDescription>
            Human schedule copy and workflow labels help explain each automation at a glance.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {statusQuery.isLoading && (
            <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Loading...
            </div>
          )}

          {statusQuery.isError && (
            <p className="py-4 text-sm text-destructive">Failed to load automation status.</p>
          )}

          {!statusQuery.isLoading && !statusQuery.isError && jobs.length === 0 && (
            <p className="py-4 text-sm text-muted-foreground">No automation jobs configured.</p>
          )}

          {!statusQuery.isLoading && jobs.length > 0 && filteredJobs.length === 0 && (
            <p className="py-4 text-sm text-muted-foreground" data-testid="automation-no-results">
              No jobs match the current search and filter.
            </p>
          )}

          {filteredJobs.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs font-medium uppercase tracking-wider text-muted-foreground">
                    <th className="w-8 px-2 py-2" />
                    <th className="px-2 py-2">Name</th>
                    <th className="px-2 py-2">Description</th>
                    <th className="px-2 py-2">Workflow</th>
                    <th className="px-2 py-2">Schedule</th>
                    <th className="px-2 py-2">Last Run</th>
                    <th className="px-2 py-2">Last Result</th>
                    <th className="px-2 py-2 text-right">Success</th>
                    <th className="px-2 py-2 text-right">Runs</th>
                    <th className="px-2 py-2 text-right">Errors</th>
                    <th className="px-2 py-2">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredJobs.map((job) => (
                    <tr key={job.name} className="border-b border-border/50 hover:bg-accent/30">
                      <td className="px-2 py-1.5">{job.running ? <span className="inline-block size-2 rounded-full bg-emerald-400" /> : job.last_error ? <span className="inline-block size-2 rounded-full bg-red-400" /> : <span className="inline-block size-2 rounded-full bg-zinc-500" />}</td>
                      <td className="px-2 py-1.5 font-mono font-medium">
                        <Link to={`/automation/${job.name}`} className="font-medium text-primary hover:underline">
                          {job.name}
                        </Link>
                      </td>
                      <td className="max-w-48 truncate px-2 py-1.5 text-muted-foreground">
                        {job.description}
                      </td>
                      <td className="px-2 py-1.5">
                        <div className="flex flex-wrap gap-1.5">
                          {deriveWorkflowLabels(job).map((label) => (
                            <Badge key={label} variant="outline">
                              {label}
                            </Badge>
                          ))}
                        </div>
                      </td>
                      <td className="px-2 py-1.5 text-muted-foreground">
                        <div className="space-y-0.5">
                          <div>{formatScheduleDisplay(job.schedule)}</div>
                          <div className="font-mono text-[11px] text-muted-foreground/80">Backend schedule description</div>
                        </div>
                      </td>
                      <td className="px-2 py-1.5 text-xs text-muted-foreground">{formatRelativeTime(job.last_run)}</td>
                      <td className="px-2 py-1.5">{resultBadge(job.last_result)}</td>
                      <td className="px-2 py-1.5 text-right font-mono">
                        {formatPercent(Math.max(job.run_count - job.error_count, 0), job.run_count)}
                      </td>
                      <td className="px-2 py-1.5 text-right font-mono">{job.run_count}</td>
                      <td className="px-2 py-1.5 text-right font-mono">
                        {job.error_count > 0 ? <span className="text-destructive">{job.error_count}</span> : job.error_count}
                      </td>
                      <td className="px-2 py-1.5">
                        <div className="flex items-center gap-1.5">
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={job.running || runMutation.isPending}
                            onClick={() => runMutation.mutate(job.name)}
                          >
                            {job.running ? <Loader2 className="mr-1 size-3 animate-spin" /> : <Play className="mr-1 size-3" />}
                            Run
                          </Button>
                          <Button
                            size="sm"
                            variant={job.enabled ? 'outline' : 'secondary'}
                            disabled={enableMutation.isPending}
                            onClick={() => enableMutation.mutate({ name: job.name, enabled: !job.enabled })}
                          >
                            {job.enabled ? 'Disable' : 'Enable'}
                          </Button>
                        </div>
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

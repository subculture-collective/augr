import type { AutomationJobStatus } from '@/shared/types/domain'

export const automationCutover = {
  deployment: '37da1b975217',
  occurredAt: '2026-08-26T03:05:08.820431707Z',
} as const

export type AutomationOperationalState = 'disabled' | 'running' | 'unverified' | 'blocked' | 'failing' | 'degraded' | 'healthy'

export function isPostAutomationCutover(value?: string): boolean {
  if (!value) return false
  const timestamp = Date.parse(value)
  return !Number.isNaN(timestamp) && timestamp >= Date.parse(automationCutover.occurredAt)
}

export function automationOperationalState(job: AutomationJobStatus): AutomationOperationalState {
  if (!job.enabled) return 'disabled'
  if (job.running) return 'running'
  if (!isPostAutomationCutover(job.last_run)) return 'unverified'
  if (isDependencyBlocked(job)) return 'blocked'
  if (/^degraded(?: after .+)?$/i.test(job.last_result.trim())) return 'degraded'
  if (currentAutomationErrorCount(job) >= 3) return 'failing'
  if (currentAutomationErrorCount(job) > 0) return 'degraded'
  return 'healthy'
}

export function currentAutomationErrorCount(job: AutomationJobStatus): number {
  if (!isPostAutomationCutover(job.last_run) || isDependencyBlocked(job)) return 0
  return isPostAutomationCutover(job.last_error_at) ? job.consecutive_failures : 0
}

export function automationDependencyReason(job: AutomationJobStatus): string | undefined {
  const result = job.last_result.trim()
  const detail = job.last_detail?.trim() ?? ''
  if ((/^skipped$/i.test(result) || /^skipped:/i.test(result)) && /^dependency\s+/i.test(detail)) return detail
  return result.match(/^skipped:\s*(dependency\s+.+)$/i)?.[1]
}

function isDependencyBlocked(job: AutomationJobStatus): boolean {
  return automationDependencyReason(job) !== undefined
}

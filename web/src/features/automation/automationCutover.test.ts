import { describe, expect, it } from 'vitest'

import { automationOperationalState, currentAutomationErrorCount, isPostAutomationCutover } from './automationCutover'

const job = (overrides: Record<string, unknown> = {}) => ({
  name: 'current_data_refresh',
  description: 'Refreshes current data.',
  schedule: '*/15 * * * 1-5',
  last_run: '2026-08-21T21:12:44Z',
  last_error_at: '2026-08-21T21:12:44Z',
  last_result: 'error',
  run_count: 12,
  error_count: 6,
  consecutive_failures: 6,
  running: false,
  enabled: true,
  ...overrides,
})

describe('automation cutover', () => {
  it('excludes runs before the deployment', () => {
    expect(isPostAutomationCutover('2026-08-21T21:12:44Z')).toBe(false)
    expect(isPostAutomationCutover('2026-08-21T21:12:45Z')).toBe(true)
    expect(isPostAutomationCutover()).toBe(false)
  })

  it('marks pre-deployment failures unverified instead of failing', () => {
    const staleJob = job()

    expect(automationOperationalState(staleJob)).toBe('unverified')
    expect(currentAutomationErrorCount(staleJob)).toBe(0)
  })

  it('retains post-deployment failures as operational failures', () => {
    const currentFailure = job({ last_run: '2026-08-22T00:00:00Z', last_error_at: '2026-08-22T00:00:00Z' })

    expect(automationOperationalState(currentFailure)).toBe('failing')
    expect(currentAutomationErrorCount(currentFailure)).toBe(6)
  })
})

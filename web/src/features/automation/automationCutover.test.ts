import { describe, expect, it } from 'vitest'

import { automationOperationalState, currentAutomationErrorCount, isPostAutomationCutover } from './automationCutover'

const job = (overrides: Record<string, unknown> = {}) => ({
  name: 'current_data_refresh',
  description: 'Refreshes current data.',
  schedule: '*/15 * * * 1-5',
  last_run: '2026-08-26T03:05:08.819Z',
  last_error_at: '2026-08-26T03:05:08.819Z',
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
    expect(isPostAutomationCutover('2026-08-26T03:05:08.819Z')).toBe(false)
    expect(isPostAutomationCutover('2026-08-26T03:05:08.820431707Z')).toBe(true)
    expect(isPostAutomationCutover()).toBe(false)
  })

  it('marks pre-deployment failures unverified instead of failing', () => {
    const staleJob = job()

    expect(automationOperationalState(staleJob)).toBe('unverified')
    expect(currentAutomationErrorCount(staleJob)).toBe(0)
  })

  it('retains post-deployment failures as operational failures', () => {
    const currentFailure = job({ last_run: '2026-08-26T03:05:09Z', last_error_at: '2026-08-26T03:05:09Z' })

    expect(automationOperationalState(currentFailure)).toBe('failing')
    expect(currentAutomationErrorCount(currentFailure)).toBe(6)
  })

  it.each(['degraded', 'degraded after 12.345ms'])(
    'classifies a post-deployment %s result before error counts',
    (lastResult) => {
      const degradedJob = job({
        last_run: '2026-08-26T03:05:09Z',
        last_error_at: undefined,
        last_result: lastResult,
        error_count: 6,
        consecutive_failures: 0,
      })

      expect(automationOperationalState(degradedJob)).toBe('degraded')
      expect(currentAutomationErrorCount(degradedJob)).toBe(0)
    },
  )

  it('classifies a live dependency skip as blocked before its retained failure streak', () => {
    const blockedJob = job({
      name: 'hot_scan',
      last_run: '2026-08-26T03:05:09Z',
      last_error_at: '2026-08-26T03:05:09Z',
      last_result: 'skipped',
      last_detail: 'dependency current_data_refresh still running',
      consecutive_failures: 5,
    })

    expect(automationOperationalState(blockedJob)).toBe('blocked')
    expect(currentAutomationErrorCount(blockedJob)).toBe(0)
  })

  it('blocks a dependency reason embedded in the live result', () => {
    const blockedJob = job({
      last_run: '2026-08-26T03:05:09Z',
      last_result: 'skipped: dependency current_data_refresh still running',
      last_detail: undefined,
    })

    expect(automationOperationalState(blockedJob)).toBe('blocked')
  })

  it('does not treat an arbitrary skipped reason as dependency blocking', () => {
    const skippedJob = job({
      last_run: '2026-08-26T03:05:09Z',
      last_error_at: '2026-08-26T03:05:09Z',
      last_result: 'skipped: market closed',
      last_detail: 'market closed',
      consecutive_failures: 5,
    })

    expect(automationOperationalState(skippedJob)).toBe('failing')
    expect(currentAutomationErrorCount(skippedJob)).toBe(5)
  })

  it.each([
    ['success', 'success', 'healthy'],
    ['degraded', 'degraded', 'degraded'],
    ['error', 'error', 'failing'],
  ])('classifies a %s carrying stale dependency detail by its current result', (_, lastResult, expected) => {
    const currentJob = job({
      last_run: '2026-08-26T03:05:09Z',
      last_error_at: lastResult === 'error' ? '2026-08-26T03:05:09Z' : undefined,
      last_result: lastResult,
      last_detail: 'dependency current_data_refresh still running',
      consecutive_failures: lastResult === 'error' ? 5 : 0,
    })

    expect(automationOperationalState(currentJob)).toBe(expected)
    expect(currentAutomationErrorCount(currentJob)).toBe(lastResult === 'error' ? 5 : 0)
  })
})

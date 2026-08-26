import { describe, expect, it } from 'vitest'

import { normalizeStatus } from './status'

describe('normalizeStatus automation results', () => {
  it.each([
    ['ok in 12.345ms', 'success'],
    ['ok in 1m2s', 'success'],
    ['error after 800µs', 'danger'],
    ['error after 2h3m4.5s', 'danger'],
    ['degraded after 12.345ms', 'warning'],
    ['degraded after 2h3m4.5s', 'warning'],
  ] as const)('normalizes %s as %s', (value, expected) => {
    expect(normalizeStatus(value)).toBe(expected)
  })

  it.each([
    'ok in eventually',
    'ok in 12 seconds',
    'error after retry',
    'degraded after partial response',
    'not ok in 2s',
  ])('leaves arbitrary text unknown: %s', (value) => {
    expect(normalizeStatus(value)).toBe('unknown')
  })
})

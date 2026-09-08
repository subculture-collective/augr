import '@testing-library/jest-dom/vitest'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import App from '@/App'
import { setTokenSnapshot } from '@/shared/auth/tokenStore'
import { buildAuthResponse } from '@/test/fixtures'
import { apiBaseUrl, FakeWebSocket, installAppTestHarness, resetApp, server } from '@/test/app-harness'

describe('fixed account resolution', () => {
  installAppTestHarness()

  it.each([0, 2])('blocks account cardinality %s', async (count) => {
    resetApp('/')
    setTokenSnapshot(buildAuthResponse())
    const accounts = Array.from({ length: count }, (_, index) => ({
      id: `00000000-0000-4000-8000-${String(index + 1).padStart(12, '0')}`, name: `Account ${index + 1}`,
      environment: 'paper_scored', venue: 'paper', base_currency: 'USD', storage_namespace: `account-${index + 1}`,
      evidence_class: 'paper_scored', starting_capital: '100000', buying_power_multiplier: '1', margin_profile: 'cash',
      status: 'active', created_by: 'test', creation_metadata: {}, created_at: '2026-01-15T12:00:00Z',
    }))
    server.use(http.get(`${apiBaseUrl}/me/accounts`, () => HttpResponse.json(accounts)))
    render(<App />)
    expect(await screen.findByRole('heading', { name: /account configuration error/i })).toBeTruthy()
    expect(FakeWebSocket.instances).toHaveLength(0)
  })

  it('rejects an account URL different from the sole server account', async () => {
    resetApp('/accounts/00000000-0000-4000-8000-000000000099/cockpit')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)
    expect(await screen.findByRole('heading', { name: /page not found/i })).toBeTruthy()
  })
})

import '@testing-library/jest-dom/vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import App from '@/App'
import { setTokenSnapshot } from '@/shared/auth/tokenStore'
import { buildAuthResponse, buildRiskStatus } from '@/test/fixtures'
import { apiBaseUrl, installAppTestHarness, resetApp, server, state } from '@/test/app-harness'

describe('account risk and global safety separation', () => {
  installAppTestHarness()

  it('renders only account-scoped risk status and cockpit evidence on Risk', async () => {
    resetApp('/risk')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)

    expect(await screen.findByRole('heading', { name: /^risk$/i })).toBeTruthy()
    expect(await screen.findByRole('heading', { name: /risk status/i })).toBeTruthy()
    expect(await screen.findByRole('table', { name: /risk exposures/i })).toBeTruthy()
    expect(screen.getByText(/global safety controls are intentionally separate/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /activate global kill switch/i })).toBeNull()
    expect(screen.queryByRole('heading', { name: /tripped breakers/i })).toBeNull()
  })

  it('renders genuine empty account risk evidence', async () => {
    resetApp('/risk')
    state.scenario = 'empty-data'
    setTokenSnapshot(buildAuthResponse())
    render(<App />)

    expect(await screen.findByText(/no risk exposures/i)).toBeTruthy()
  })

  it('supports account risk retry and unavailable states', async () => {
    resetApp('/risk')
    setTokenSnapshot(buildAuthResponse())
    let calls = 0
    server.use(http.get(`${apiBaseUrl}/accounts/:accountId/risk/status`, () => {
      calls += 1
      if (calls === 1) return HttpResponse.json({ error: 'risk exploded', code: 'ERR_VALIDATION' }, { status: 400 })
      return HttpResponse.json(buildRiskStatus())
    }))
    render(<App />)

    const statusPanel = (await screen.findByRole('heading', { name: /risk status/i })).closest('section') as HTMLElement
    expect(await within(statusPanel).findByRole('alert')).toHaveTextContent('risk exploded')
    await userEvent.click(within(statusPanel).getByRole('button', { name: /reload/i }))
    expect(await within(statusPanel).findByText('normal')).toBeTruthy()

    resetApp('/risk')
    setTokenSnapshot(buildAuthResponse())
    server.use(http.get(`${apiBaseUrl}/accounts/:accountId/risk/cockpit`, () => HttpResponse.json({ error: 'risk cockpit unavailable', code: 'ERR_NOT_IMPLEMENTED' }, { status: 501 })))
    render(<App />)
    expect(await screen.findByText(/feature unavailable/i)).toBeTruthy()
  })

  it('keeps global controls and automation status on System Safety', async () => {
    resetApp('/system/safety')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)

    expect(await screen.findByRole('heading', { name: /^system safety$/i })).toBeTruthy()
    expect(screen.getByRole('button', { name: /^activate global kill switch$/i })).toBeDisabled()
    expect(await screen.findByRole('heading', { name: /tripped breakers/i })).toBeTruthy()
    expect(await screen.findByRole('heading', { name: /automation status/i })).toBeTruthy()
    expect(screen.queryByRole('table', { name: /risk exposures/i })).toBeNull()
  })
})

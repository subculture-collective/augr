import '@testing-library/jest-dom/vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import App from '@/App'
import { setTokenSnapshot } from '@/shared/auth/tokenStore'
import { buildAuthResponse, fixtureDate } from '@/test/fixtures'
import { apiBaseUrl, installAppTestHarness, resetApp, server } from '@/test/app-harness'

describe('SystemSafetyPage', () => {
  installAppTestHarness()

  it('keeps global safety controls outside account risk', async () => {
    resetApp('/system/safety')
    setTokenSnapshot(buildAuthResponse())
    render(<App />)
    expect(await screen.findByRole('heading', { name: 'System Safety' })).toBeTruthy()
    expect(screen.getByRole('button', { name: /^activate global kill switch$/i })).toBeTruthy()
    expect(screen.getByRole('heading', { name: /automation status/i })).toBeTruthy()
  })

  it('confirms global safety mutations and refreshes server state', async () => {
    resetApp('/system/safety')
    setTokenSnapshot(buildAuthResponse())
    let activationCalls = 0
    server.use(
      http.post(`${apiBaseUrl}/risk/killswitch`, async ({ request }) => {
        activationCalls += 1
        expect(await request.json()).toMatchObject({ active: true, reason: 'operator halt' })
        return HttpResponse.json({
          active: true,
          reason: 'operator halt',
          mechanisms: ['api_toggle'],
          updated_at: fixtureDate,
        })
      }),
    )
    render(<App />)

    await screen.findByRole('heading', { name: 'System Safety' })
    await userEvent.type(screen.getByLabelText(/operator reason/i), 'operator halt')
    await userEvent.click(screen.getByRole('button', { name: /^activate global kill switch$/i }))
    expect(activationCalls).toBe(0)
    const dialog = screen.getByRole('dialog', { name: /activate global kill switch/i })
    await userEvent.click(within(dialog).getByRole('button', { name: /confirm safety change/i }))

    expect(await screen.findByRole('status')).toHaveTextContent(/refreshed server state/i)
    expect(activationCalls).toBe(1)
  })

  it('treats a failed safety mutation as an unknown completion until refreshed', async () => {
    resetApp('/system/safety')
    setTokenSnapshot(buildAuthResponse())
    server.use(
      http.post(`${apiBaseUrl}/risk/killswitch`, () =>
        HttpResponse.json({ error: 'connection lost', code: 'ERR_INTERNAL' }, { status: 500 }),
      ),
    )
    render(<App />)

    await screen.findByRole('heading', { name: 'System Safety' })
    await userEvent.type(screen.getByLabelText(/operator reason/i), 'operator halt')
    await userEvent.click(screen.getByRole('button', { name: /^activate global kill switch$/i }))
    const dialog = screen.getByRole('dialog', { name: /activate global kill switch/i })
    await userEvent.click(within(dialog).getByRole('button', { name: /confirm safety change/i }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      /completion may be unknown; refresh and verify server state before retrying/i,
    )
  })
})

import '@testing-library/jest-dom/vitest'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import App from '@/App'
import { setTokenSnapshot } from '@/shared/auth/tokenStore'
import { buildAuthResponse } from '@/test/fixtures'
import { apiBaseUrl, FakeWebSocket, installAppTestHarness, resetApp, server } from '@/test/app-harness'

describe('AppProviders account realtime ordering', () => {
  installAppTestHarness()

  it('does not open realtime when account loading fails', async () => {
    resetApp('/')
    setTokenSnapshot(buildAuthResponse())
    server.use(http.get(`${apiBaseUrl}/me/accounts`, () => HttpResponse.json({ error: 'account store unavailable', code: 'ERR_INTERNAL' }, { status: 500 })))
    render(<App />)
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(FakeWebSocket.instances).toHaveLength(0)
  })
})

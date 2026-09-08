import '@testing-library/jest-dom/vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { ProtectedRoute } from '@/app/router/ProtectedRoute'
import { AppProviders } from '@/app/providers/AppProviders'
import { EventsPage } from '@/features/events/EventsPage'
import { RunsListPage } from '@/features/runs/RunsListPage'
import { StrategiesListPage } from '@/features/strategies/StrategiesListPage'
import { setTokenSnapshot } from '@/shared/auth/tokenStore'
import { buildAgentEvent, buildAuthResponse, buildRun, buildStrategy } from '@/test/fixtures'
import { apiBaseUrl, installAppTestHarness, resetApp, server } from '@/test/app-harness'

// Feature components own query states; App tests retain navigation, account/auth,
// realtime coordination, and confirmed mutations across routes.
const lists = [
  { name: 'strategies', Page: StrategiesListPage, endpoint: '/strategies', empty: /no strategies found/i, row: () => buildStrategy() },
  { name: 'runs', Page: RunsListPage, endpoint: '/accounts/:accountId/runs', empty: /no runs found/i, row: () => buildRun() },
  { name: 'events', Page: EventsPage, endpoint: '/accounts/:accountId/events', empty: /no persisted events/i, row: () => buildAgentEvent() },
]

describe.each(lists)('$name list query states', ({ name, Page, endpoint, empty, row }) => {
  installAppTestHarness()

  function renderPage() {
    resetApp()
    setTokenSnapshot(buildAuthResponse())
    return render(<AppProviders><MemoryRouter><Routes><Route element={<ProtectedRoute />}><Route path="/" element={<Page />} /></Route></Routes></MemoryRouter></AppProviders>)
  }

  it('explains an empty successful response', async () => {
    server.use(http.get(`${apiBaseUrl}${endpoint}`, () => HttpResponse.json({ data: [], total: 0, limit: 20, offset: 0 })))
    renderPage()
    expect(await screen.findByText(empty)).toBeInTheDocument()
    if (name === 'strategies') expect(screen.getByText(/create a paper strategy/i)).toBeInTheDocument()
  })

  it('retries a failed request and replaces the error with returned rows', async () => {
    let calls = 0
    server.use(http.get(`${apiBaseUrl}${endpoint}`, () => {
      calls += 1
      return calls === 1
        ? HttpResponse.json({ error: `${name} unavailable`, code: 'ERR_VALIDATION' }, { status: 400 })
        : HttpResponse.json({ data: [row()], total: 1, limit: 20, offset: 0 })
    }))
    renderPage()
    expect(await screen.findByText(`${name} unavailable`)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /reload/i }))
    if (name === 'events') expect(await screen.findByText('Analyst decision recorded')).toBeInTheDocument()
    else expect(await screen.findByRole('table')).toBeInTheDocument()
    expect(screen.queryByText(`${name} unavailable`)).not.toBeInTheDocument()
    expect(calls).toBe(2)
    if (name === 'strategies') expect(screen.getAllByRole('link', { name: /dev paper mean reversion/i }).length).toBeGreaterThan(0)
  })

  it('distinguishes a missing capability from empty data', async () => {
    server.use(http.get(`${apiBaseUrl}${endpoint}`, () => HttpResponse.json({ error: 'not configured', code: 'ERR_NOT_IMPLEMENTED' }, { status: 501 })))
    renderPage()
    expect(await screen.findByText(/feature unavailable/i)).toBeInTheDocument()
    expect(screen.queryByText(empty)).not.toBeInTheDocument()
  })
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AutomationPage } from '@/pages/automation-page'

const apiClientMock = vi.hoisted(() => ({
  getAutomationStatus: vi.fn(),
  runAutomationJob: vi.fn(),
  setAutomationJobEnabled: vi.fn(),
}))

vi.mock('@/lib/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api/client')>('@/lib/api/client')
  return {
    ...actual,
    apiClient: apiClientMock,
  }
})

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false }, mutations: { retry: false } },
  })

  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AutomationPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const jobs = [
  {
    name: 'daily-report',
    description: 'Daily report for operations',
    schedule: 'Daily at 9:00 AM ET, Mon-Fri (market hours only), skip holidays',
    last_run: '2025-02-02T11:00:00Z',
    last_result: 'ok: completed',
    run_count: 10,
    error_count: 1,
    running: false,
    enabled: true,
  },
  {
    name: 'risk-monitor',
    description: 'Monitor failing jobs and alert',
    schedule: 'Every 15 minutes',
    last_run: '2025-02-02T11:15:00Z',
    last_result: 'error: timeout',
    last_error: 'timeout',
    run_count: 5,
    error_count: 2,
    running: false,
    enabled: false,
    consecutive_failures: 2,
  },
  {
    name: 'sync-refresh',
    description: 'Sync watchlists and refresh caches',
    schedule: 'Hourly',
    last_result: 'ok',
    run_count: 0,
    error_count: 0,
    running: true,
    enabled: true,
  },
]

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('AutomationPage', () => {
  it('shows global metrics, filters, and reset view controls', async () => {
    const user = userEvent.setup()
    apiClientMock.getAutomationStatus.mockResolvedValue(jobs)

    renderPage()

    expect(await screen.findByRole('link', { name: 'daily-report' })).toBeInTheDocument()
    expect(screen.getByTestId('automation-summary-metrics')).toHaveTextContent('80%')
    expect(screen.getByTestId('automation-summary-metrics')).toHaveTextContent('12:3')
    expect(screen.getByTestId('automation-workflow-card')).toHaveTextContent('monitor')
    expect(screen.getByTestId('automation-workflow-card')).toHaveTextContent('report · 1')
    expect(screen.getByTestId('automation-local-reset-note')).toHaveTextContent('Showing 3 of 3 jobs.')

    await user.type(screen.getByLabelText('Search automation jobs'), 'sync')
    await waitFor(() => expect(screen.getByTestId('automation-local-reset-note')).toHaveTextContent('Showing 1 of 3 jobs.'))
    expect(screen.getByRole('link', { name: 'sync-refresh' })).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('Filter automation jobs by status'), 'failing')
    await waitFor(() => expect(screen.getByTestId('automation-local-reset-note')).toHaveTextContent('Showing 0 of 3 jobs.'))
    expect(screen.getByTestId('automation-no-results')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /reset view/i }))
    await waitFor(() => expect(screen.getByTestId('automation-local-reset-note')).toHaveTextContent('Showing 3 of 3 jobs.'))
    expect(screen.getByRole('link', { name: 'risk-monitor' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'sync-refresh' })).toBeInTheDocument()
  })
})

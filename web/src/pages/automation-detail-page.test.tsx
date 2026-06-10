import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AutomationDetailPage } from '@/pages/automation-detail-page'

const jobName = 'risk-monitor'

const apiClientMock = vi.hoisted(() => ({
  getAutomationStatus: vi.fn(),
  listAutomationRuns: vi.fn(),
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
      <MemoryRouter initialEntries={[`/automation/${jobName}`]}>
        <Routes>
          <Route path="automation/:name" element={<AutomationDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const statusFixture = [
  {
    name: jobName,
    description: 'Monitor failing jobs and alert',
    schedule: 'Daily at 9:00 AM ET, Mon-Fri (market hours only), skip holidays',
    last_run: '2025-02-02T11:00:00Z',
    last_result: 'error: timeout',
    last_error: 'timeout while polling queue',
    last_error_at: '2025-02-02T11:00:00Z',
    last_summary: { ok: 2, error: 1 },
    run_count: 9,
    error_count: 3,
    consecutive_failures: 4,
    stuck_for: 300_000_000_000,
    running: false,
    enabled: true,
  },
]

const runFixture = [
  {
    id: 'run-1',
    job_name: jobName,
    status: 'error',
    started_at: '2025-02-02T11:00:00Z',
    completed_at: '2025-02-02T11:01:00Z',
    duration_ns: 60_000_000_000,
    error: 'timeout while polling queue',
    last_error_at: '2025-02-02T11:00:00Z',
    consecutive_failures: 4,
    created_at: '2025-02-02T11:01:00Z',
  },
  {
    id: 'run-2',
    job_name: jobName,
    status: 'ok',
    started_at: '2025-02-01T11:00:00Z',
    completed_at: '2025-02-01T11:00:30Z',
    duration_ns: 30_000_000_000,
    consecutive_failures: 0,
    created_at: '2025-02-01T11:00:30Z',
  },
  {
    id: 'run-3',
    job_name: 'other-job',
    status: 'ok',
    started_at: '2025-02-01T10:00:00Z',
    completed_at: '2025-02-01T10:00:15Z',
    duration_ns: 15_000_000_000,
    consecutive_failures: 0,
    created_at: '2025-02-01T10:00:15Z',
  },
]

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  vi.useRealTimers()
})

describe('AutomationDetailPage', () => {
  it('renders optional backend fields, stats, and timeline history', async () => {
    apiClientMock.getAutomationStatus.mockResolvedValue(statusFixture)
    apiClientMock.listAutomationRuns.mockResolvedValue({ data: runFixture, limit: 50, offset: 0 })

    renderPage()

    expect(await screen.findByText(jobName)).toBeInTheDocument()
    expect(screen.getByTestId('automation-overview-card')).toHaveTextContent('Daily at 9:00 AM ET, Mon-Fri')
    expect(screen.getByTestId('automation-overview-card')).toHaveTextContent('monitor')
    expect(screen.getByTestId('automation-detail-stats-card')).toHaveTextContent('67%')
    expect(screen.getByTestId('automation-detail-stats-card')).toHaveTextContent('33%')
    expect(screen.getByTestId('automation-detail-stats-card')).toHaveTextContent('4')
    expect(screen.getByTestId('automation-detail-stats-card')).toHaveTextContent('5m')
    expect(screen.getByTestId('automation-detail-stats-card')).toHaveTextContent('Last Error At')
    expect(screen.getByTestId('automation-detail-stats-card')).toHaveTextContent('2025')
    expect(screen.getByTestId('automation-last-summary')).toHaveTextContent('error: 1')
    expect(screen.getByTestId('automation-last-summary')).toHaveTextContent('ok: 2')
    expect(screen.getByTestId('automation-run-timeline')).toHaveTextContent('timeout while polling queue')
    expect(screen.getByTestId('automation-run-timeline')).toHaveTextContent('Duration 1m')
    expect(screen.getByTestId('automation-run-timeline')).toHaveTextContent('error')
    expect(screen.queryByText('other-job')).not.toBeInTheDocument()
  })

  it('calls run and enable actions from the detail page', async () => {
    const user = userEvent.setup()
    apiClientMock.getAutomationStatus.mockResolvedValue(statusFixture)
    apiClientMock.listAutomationRuns.mockResolvedValue({ data: runFixture, limit: 50, offset: 0 })
    apiClientMock.runAutomationJob.mockResolvedValue(undefined)
    apiClientMock.setAutomationJobEnabled.mockResolvedValue(undefined)

    renderPage()

    await user.click(await screen.findByTestId('automation-run-button'))
    await user.click(screen.getByTestId('automation-toggle-button'))

    await waitFor(() => expect(apiClientMock.runAutomationJob).toHaveBeenCalledWith(jobName))
    expect(apiClientMock.setAutomationJobEnabled).toHaveBeenCalledWith(jobName, false)
  })
})

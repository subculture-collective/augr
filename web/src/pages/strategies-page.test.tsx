import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { StrategiesPage } from '@/pages/strategies-page'

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={client}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  )
}

type StrategyFixture = {
  id: string
  name: string
  ticker: string
  market_type: 'stock' | 'crypto' | 'polymarket' | 'options'
  is_active: boolean
  is_paper: boolean
  schedule_cron?: string
  config: Record<string, unknown>
  created_at: string
  updated_at: string
  latest_run_summary?: {
    id: string
    strategy_id: string
    ticker: string
    status: 'running' | 'completed' | 'failed' | 'cancelled'
    signal?: 'buy' | 'sell' | 'hold'
    started_at: string
    completed_at?: string
  }
}

type RunFixture = {
  id: string
  strategy_id: string
  ticker: string
  status: 'running' | 'completed' | 'failed' | 'cancelled'
  signal?: 'buy' | 'sell' | 'hold'
  started_at: string
  completed_at?: string
  outcome?: string
}

function createResponse(body: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  }
}

function stubStrategiesFetch({
  strategies,
  runsByStrategyId = {},
}: {
  strategies: StrategyFixture[]
  runsByStrategyId?: Record<string, RunFixture[]>
}) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(typeof input === 'string' ? input : input.toString(), 'http://localhost')

    if (url.pathname === '/api/v1/strategies') {
      return createResponse({ data: strategies, total: strategies.length, limit: 100, offset: 0 })
    }

    if (url.pathname === '/api/v1/runs') {
      const strategyId = url.searchParams.get('strategy_id') ?? ''
      const runs = runsByStrategyId[strategyId] ?? []
      return createResponse({ data: runs, total: runs.length, limit: 1, offset: 0 })
    }

    return createResponse({ error: 'unexpected request' }, 500)
  })

  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('StrategiesPage', () => {
  it('renders strategy list on successful fetch', async () => {
    const strategies: StrategyFixture[] = [
      {
        id: '00000000-0000-0000-0000-000000000001',
        name: 'AAPL Momentum',
        ticker: 'AAPL',
        market_type: 'stock',
        is_active: true,
        is_paper: false,
        schedule_cron: '0 9 * * *',
        config: {},
        created_at: '2025-01-01T00:00:00Z',
        updated_at: '2025-01-01T00:00:00Z',
        latest_run_summary: {
          id: '00000000-0000-0000-0000-000000000011',
          strategy_id: '00000000-0000-0000-0000-000000000001',
          ticker: 'AAPL',
          status: 'completed',
          signal: 'buy',
          started_at: '2025-01-02T09:00:00Z',
          completed_at: '2025-01-02T09:01:00Z',
        },
      },
      {
        id: '00000000-0000-0000-0000-000000000002',
        name: 'BTC Swing',
        ticker: 'BTCUSD',
        market_type: 'crypto',
        is_active: false,
        is_paper: true,
        config: {},
        created_at: '2025-01-01T00:00:00Z',
        updated_at: '2025-01-01T00:00:00Z',
      },
    ]

    const fetchMock = stubStrategiesFetch({ strategies })

    render(<StrategiesPage />, { wrapper: Wrapper })

    expect(await screen.findByText('AAPL Momentum')).toBeInTheDocument()
    expect(screen.getByText('BTC Swing')).toBeInTheDocument()
    expect(screen.getByText('active')).toBeInTheDocument()
    expect(screen.getByText('inactive')).toBeInTheDocument()
    expect(screen.getByText('paper')).toBeInTheDocument()
    expect(screen.getByTestId('strategies-list')).toBeInTheDocument()
    expect(await within(screen.getByTestId('strategy-last-run-00000000-0000-0000-0000-000000000002')).findByText('No runs yet')).toBeInTheDocument()
    const lastRun = screen.getByTestId('strategy-last-run-00000000-0000-0000-0000-000000000001')
    expect(within(lastRun).getByText('Completed')).toBeInTheDocument()
    expect(within(lastRun).getByText('buy')).toBeInTheDocument()
    expect(within(lastRun).getByRole('link', { name: 'Open run' })).toHaveAttribute(
      'href',
      '/runs/00000000-0000-0000-0000-000000000011',
    )

    const ranRunsQuery = fetchMock.mock.calls.some(([input]) => {
      const url = new URL(typeof input === 'string' ? input : input.toString(), 'http://localhost')
      return url.pathname === '/api/v1/runs'
    })
    expect(ranRunsQuery).toBe(false)
  })

  it('shows last-run no-run copy and completed run link', async () => {
    const runId = '00000000-0000-0000-0000-000000000099'
    const strategies: StrategyFixture[] = [
      {
        id: '00000000-0000-0000-0000-000000000001',
        name: 'No Run Strategy',
        ticker: 'NRUN',
        market_type: 'stock' as const,
        is_active: true,
        is_paper: false,
        config: {},
        created_at: '2025-01-01T00:00:00Z',
        updated_at: '2025-01-01T00:00:00Z',
      },
      {
        id: '00000000-0000-0000-0000-000000000002',
        name: 'Completed Run Strategy',
        ticker: 'DONE',
        market_type: 'stock' as const,
        is_active: true,
        is_paper: false,
        config: {},
        created_at: '2025-01-01T00:00:00Z',
        updated_at: '2025-01-01T00:00:00Z',
        latest_run_summary: {
          id: runId,
          strategy_id: '00000000-0000-0000-0000-000000000002',
          ticker: 'DONE',
          status: 'completed',
          signal: 'buy',
          started_at: '2025-01-02T09:00:00Z',
          completed_at: '2025-01-02T09:01:00Z',
        },
      },
    ]

    const fetchMock = stubStrategiesFetch({
      strategies,
      runsByStrategyId: {
        '00000000-0000-0000-0000-000000000002': [
          {
            id: runId,
            strategy_id: '00000000-0000-0000-0000-000000000002',
            ticker: 'DONE',
            status: 'completed',
            signal: 'buy',
            started_at: '2025-01-02T09:00:00Z',
            completed_at: '2025-01-02T09:01:00Z',
            outcome: 'profit',
          },
        ],
      },
    })

    render(<StrategiesPage />, { wrapper: Wrapper })

    const noRunCard = await screen.findByTestId('strategy-last-run-00000000-0000-0000-0000-000000000001')
    expect(await within(noRunCard).findByText('No runs yet')).toBeInTheDocument()

    const completedCard = await screen.findByTestId('strategy-last-run-00000000-0000-0000-0000-000000000002')
    expect(await within(completedCard).findByText('Completed')).toBeInTheDocument()
    expect(within(completedCard).getByText('buy')).toBeInTheDocument()
    expect(within(completedCard).getByRole('link', { name: 'Open run' })).toHaveAttribute('href', `/runs/${runId}`)

    const ranRunsQuery = fetchMock.mock.calls.some(([input]) => {
      const url = new URL(typeof input === 'string' ? input : input.toString(), 'http://localhost')
      return url.pathname === '/api/v1/runs'
    })
    expect(ranRunsQuery).toBe(false)
  })

  it('shows empty state when no strategies exist', async () => {
    stubStrategiesFetch({ strategies: [] })

    render(<StrategiesPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('strategies-empty')).toBeInTheDocument()
  })

  it('shows empty state when API returns null data array', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ data: null, total: 0, limit: 100, offset: 0 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<StrategiesPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('strategies-empty')).toBeInTheDocument()
  })

  it('shows error state when fetch fails', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('Network error'))
    vi.stubGlobal('fetch', fetchMock)

    render(<StrategiesPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('strategies-error')).toBeInTheDocument()
  })

  it('shows create strategy button', async () => {
    stubStrategiesFetch({ strategies: [] })

    render(<StrategiesPage />, { wrapper: Wrapper })

    const page = await screen.findByTestId('strategies-page')
    expect(page).toHaveTextContent('New strategy')
  })

  it('shows run buttons for each strategy', async () => {
    const strategies: StrategyFixture[] = [
      {
        id: '00000000-0000-0000-0000-000000000001',
        name: 'Test Strategy',
        ticker: 'TEST',
        market_type: 'stock',
        is_active: true,
        is_paper: false,
        config: {},
        created_at: '2025-01-01T00:00:00Z',
        updated_at: '2025-01-01T00:00:00Z',
      },
    ]

    stubStrategiesFetch({ strategies })

    render(<StrategiesPage />, { wrapper: Wrapper })

    expect(
      await screen.findByTestId('run-strategy-00000000-0000-0000-0000-000000000001'),
    ).toBeInTheDocument()
  })
})

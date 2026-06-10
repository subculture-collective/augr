import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { StrategyDetailPage } from '@/pages/strategy-detail-page'

const strategyId = '00000000-0000-0000-0000-000000000001'

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/strategies/${strategyId}`]}>
        <Routes>
          <Route path="strategies/:id" element={children} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

type StrategyFixture = {
  id: string
  name: string
  description?: string
  ticker: string
  market_type: 'stock' | 'crypto' | 'polymarket'
  status: 'active' | 'paused' | 'inactive'
  skip_next_run: boolean
  is_paper: boolean
  schedule_cron?: string
  config: Record<string, unknown>
  created_at: string
  updated_at: string
}

type MutationStub = {
  status: number
  body: StrategyFixture | { error: string; code?: string }
}

function createStrategy(overrides: Partial<StrategyFixture> = {}): StrategyFixture {
  return {
    id: strategyId,
    name: 'AAPL Momentum',
    description: 'A momentum-based strategy',
    ticker: 'AAPL',
    market_type: 'stock',
    status: 'active',
    skip_next_run: false,
    is_paper: false,
    schedule_cron: '0 9 * * 1-5',
    config: { analysts: ['market'] },
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    ...overrides,
  }
}

function createResponse(body: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  }
}

function createListResponse<T>(data: T[]) {
  return { data, limit: data.length, offset: 0 }
}

function stubStrategyFetch({
  strategy = createStrategy(),
  runs = [],
  pauseResult,
  resumeResult,
  skipResult,
  deleteResult,
  orders = [],
  backtests = [],
}: {
  strategy?: StrategyFixture
  runs?: Array<{ id: string; strategy_id: string; ticker: string; status: 'running' | 'completed' | 'failed' | 'cancelled'; signal?: 'buy' | 'sell' | 'hold'; started_at: string; completed_at?: string }>
  pauseResult?: MutationStub
  resumeResult?: MutationStub
  skipResult?: MutationStub
  deleteResult?: MutationStub
  orders?: Array<{ id: string; ticker: string; side: 'buy' | 'sell'; status: string; created_at: string }>
  backtests?: Array<{ id: string; name: string; description?: string; start_date: string; end_date: string }>
} = {}) {
  let currentStrategy = strategy
  const runList = createListResponse(runs)
  const orderList = createListResponse(orders)
  const backtestList = createListResponse(backtests)

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()

    if (url.includes(`/api/v1/strategies/${strategyId}`) && init?.method === 'DELETE') {
      const result = deleteResult ?? { status: 204, body: null }
      return createResponse(result.body, result.status)
    }

    if (url.includes('/runs')) {
      return createResponse(runList)
    }

    if (url.includes('/orders')) {
      return createResponse(orderList)
    }

    if (url.includes('/backtests/configs')) {
      return createResponse(backtestList)
    }

    if (url.includes(`/api/v1/strategies/${strategyId}/pause`)) {
      const result = pauseResult ?? { status: 200, body: { ...currentStrategy, status: 'paused' as const } }
      if (result.status >= 200 && result.status < 300 && 'status' in result.body) {
        currentStrategy = result.body
      }
      return createResponse(result.body, result.status)
    }

    if (url.includes(`/api/v1/strategies/${strategyId}/resume`)) {
      const result = resumeResult ?? { status: 200, body: { ...currentStrategy, status: 'active' as const } }
      if (result.status >= 200 && result.status < 300 && 'status' in result.body) {
        currentStrategy = result.body
      }
      return createResponse(result.body, result.status)
    }

    if (url.includes(`/api/v1/strategies/${strategyId}/skip-next`)) {
      const result = skipResult ?? { status: 200, body: { ...currentStrategy, skip_next_run: true } }
      if (result.status >= 200 && result.status < 300 && 'status' in result.body) {
        currentStrategy = result.body
      }
      return createResponse(result.body, result.status)
    }

    return createResponse(currentStrategy)
  })

  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function renderPage() {
  render(<StrategyDetailPage />, { wrapper: Wrapper })
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('StrategyDetailPage', () => {
  it('renders active strategies with the correct lifecycle action matrix', async () => {
    stubStrategyFetch()

    renderPage()

    expect(await screen.findByText('AAPL Momentum')).toBeInTheDocument()
    expect(screen.getByTestId('strategy-human-summary')).toHaveTextContent('A momentum-based strategy')
    expect(screen.getByText('AAPL')).toBeInTheDocument()
    expect(screen.getByTestId('strategy-detail-page')).toBeInTheDocument()
    expect(screen.getByTestId('strategy-status-badge')).toHaveTextContent('active')
    expect(screen.getByTestId('run-strategy-button')).toBeInTheDocument()
    expect(screen.getByTestId('pause-strategy-button')).toBeEnabled()
    expect(screen.getByTestId('resume-strategy-button')).toBeDisabled()
    expect(screen.getByTestId('skip-next-button')).toBeEnabled()
    expect(screen.getByTestId('delete-strategy-button')).toBeInTheDocument()
    expect(screen.getByTestId('pause-strategy-button')).toHaveAttribute('title', 'Pause strategy')
    expect(screen.getByTestId('resume-strategy-button')).toHaveAttribute(
      'title',
      'Resume is unavailable because this strategy is already active.',
    )
  })

  it('renders paused strategies with the correct lifecycle action matrix', async () => {
    stubStrategyFetch({ strategy: createStrategy({ status: 'paused', name: 'Paused Strategy' }) })

    renderPage()

    expect(await screen.findByText('Paused Strategy')).toBeInTheDocument()
    expect(screen.getByTestId('pause-strategy-button')).toBeDisabled()
    expect(screen.getByTestId('resume-strategy-button')).toBeEnabled()
    expect(screen.getByTestId('skip-next-button')).toBeDisabled()
    expect(screen.getByTestId('pause-strategy-button')).toHaveAttribute(
      'title',
      'Pause is unavailable because this strategy is already paused.',
    )
    expect(screen.getByTestId('resume-strategy-button')).toHaveAttribute('title', 'Resume strategy')
  })

  it('renders inactive strategies with all lifecycle actions disabled', async () => {
    stubStrategyFetch({ strategy: createStrategy({ status: 'inactive', name: 'Inactive Strategy' }) })

    renderPage()

    expect(await screen.findByText('Inactive Strategy')).toBeInTheDocument()
    expect(screen.getByTestId('pause-strategy-button')).toBeDisabled()
    expect(screen.getByTestId('resume-strategy-button')).toBeDisabled()
    expect(screen.getByTestId('skip-next-button')).toBeDisabled()
  })

  it('renders the human summary and rules table when rules engine config is present', async () => {
    stubStrategyFetch({
      strategy: createStrategy({
        description: 'Trades trend continuation with risk controls',
        schedule_cron: '0 9 * * 1-5',
        is_paper: true,
        config: {
          rules_engine: {
            name: 'Momentum Rules',
            description: 'Entry on strength, exit on weakness',
            entry: {
              operator: 'all',
              conditions: [
                { field: 'rsi', op: 'lt', value: 30, explanation: 'Oversold entry' },
              ],
            },
            exit: {
              operator: 'any',
              conditions: [
                { field: 'price', op: 'lt', ref: 'support_level' },
              ],
            },
            position_sizing: { method: 'fraction', fraction_pct: 5, risk_per_trade_pct: 0.02 },
            stop_loss: { method: 'atr', atr_multiplier: 2, pct: 3 },
            take_profit: { method: 'rr', ratio: 2 },
            filters: { min_volume: 1000000, min_atr: 2.5 },
          },
        },
      }),
    })

    renderPage()

    const summary = await screen.findByTestId('strategy-human-summary')
    expect(summary).toHaveTextContent('Trades trend continuation with risk controls')
    expect(summary).toHaveTextContent('AAPL')
    expect(summary).toHaveTextContent('stock')
    expect(summary).toHaveTextContent('paper')
    expect(summary).toHaveTextContent('skip-next clear')
    const rules = await screen.findByTestId('strategy-rules-table')
    expect(rules).toHaveTextContent('Momentum Rules')
    expect(rules).toHaveTextContent('Entry rules: 1; exit rules: 1')
    expect(rules).toHaveTextContent('Group')
    expect(rules).toHaveTextContent('Field')
    expect(rules).toHaveTextContent('rsi')
    expect(rules).toHaveTextContent('support_level')
  })

  it('renders empty backtests and orders sections with truthful copy', async () => {
    stubStrategyFetch()

    renderPage()

    const backtests = await screen.findByTestId('strategy-backtests-empty')
    expect(backtests).toHaveTextContent('No linked backtests yet')
    expect(within(backtests).getByRole('link', { name: 'Browse backtests' })).toHaveAttribute('href', '/backtests')

    const orders = await screen.findByTestId('strategy-orders-empty')
    expect(orders).toHaveTextContent('No recent orders for AAPL')
    expect(orders).toHaveTextContent('ticker-level')
  })

  it('pauses an active strategy through the pause endpoint and refreshes the action state', async () => {
    const fetchMock = stubStrategyFetch()

    renderPage()

    fireEvent.click(await screen.findByTestId('pause-strategy-button'))

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.objectContaining({ pathname: `/api/v1/strategies/${strategyId}/pause` }),
        expect.objectContaining({ method: 'POST' }),
      )
    })

    await waitFor(() => {
      expect(screen.getByTestId('strategy-status-badge')).toHaveTextContent('paused')
      expect(screen.getByTestId('pause-strategy-button')).toBeDisabled()
      expect(screen.getByTestId('resume-strategy-button')).toBeEnabled()
      expect(screen.getByTestId('skip-next-button')).toBeDisabled()
    })
  })

  it('resumes a paused strategy through the resume endpoint', async () => {
    const fetchMock = stubStrategyFetch({ strategy: createStrategy({ status: 'paused' }) })

    renderPage()

    fireEvent.click(await screen.findByTestId('resume-strategy-button'))

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.objectContaining({ pathname: `/api/v1/strategies/${strategyId}/resume` }),
        expect.objectContaining({ method: 'POST' }),
      )
    })
  })

  it('queues skip next through the skip endpoint', async () => {
    const fetchMock = stubStrategyFetch()

    renderPage()

    fireEvent.click(await screen.findByTestId('skip-next-button'))

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.objectContaining({ pathname: `/api/v1/strategies/${strategyId}/skip-next` }),
        expect.objectContaining({ method: 'POST' }),
      )
    })

    expect(await screen.findByText('skip next queued')).toBeInTheDocument()
  })

  it('shows conflict feedback when a lifecycle transition fails', async () => {
    stubStrategyFetch({
      pauseResult: {
        status: 409,
        body: { error: 'strategy already paused', code: 'strategy_conflict' },
      },
    })

    renderPage()

    fireEvent.click(await screen.findByTestId('pause-strategy-button'))

    expect(await screen.findByTestId('strategy-action-error')).toHaveTextContent('strategy already paused')
  })

  it('asks for confirmation before deleting a strategy', async () => {
    const fetchMock = stubStrategyFetch()
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)

    renderPage()

    fireEvent.click(await screen.findByTestId('delete-strategy-button'))

    expect(confirmSpy).toHaveBeenCalledWith('Delete this strategy and all of its history?')
    expect(fetchMock).not.toHaveBeenCalledWith(
      expect.objectContaining({ pathname: `/api/v1/strategies/${strategyId}` }),
      expect.objectContaining({ method: 'DELETE' }),
    )
  })

  it('shows inline delete error when deletion fails', async () => {
    stubStrategyFetch({
      deleteResult: {
        status: 500,
        body: { error: 'delete failed' },
      },
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    renderPage()

    fireEvent.click(await screen.findByTestId('delete-strategy-button'))

    expect(await screen.findByTestId('strategy-action-error')).toHaveTextContent('delete failed')
  })

  it('shows error state when strategy fetch fails', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('Network error'))
    vi.stubGlobal('fetch', fetchMock)

    renderPage()

    expect(await screen.findByTestId('strategy-detail-error')).toBeInTheDocument()
  })

  it('renders run history and config editor', async () => {
    const strategy = createStrategy({ name: 'Test Strategy', ticker: 'TEST', is_paper: true })
    const runs = {
      data: [
        {
          id: '00000000-0000-0000-0000-000000000010',
          strategy_id: strategyId,
          ticker: 'TEST',
          trade_date: '2025-01-02',
          status: 'completed',
          signal: 'buy',
          started_at: '2025-01-02T09:00:00Z',
          completed_at: '2025-01-02T09:01:00Z',
        },
      ],
      limit: 20,
      offset: 0,
    }
    const orders = {
      data: [
        {
          id: '00000000-0000-0000-0000-000000000020',
          ticker: 'TEST',
          side: 'buy' as const,
          status: 'filled',
          created_at: '2025-01-02T10:00:00Z',
        },
      ],
      limit: 5,
      offset: 0,
    }
    const backtests = {
      data: [
        {
          id: '00000000-0000-0000-0000-000000000030',
          name: 'TEST breakout',
          description: 'Linked backtest',
          start_date: '2024-12-01',
          end_date: '2024-12-31',
        },
      ],
      limit: 20,
      offset: 0,
    }

    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString()

      if (url.includes('/runs')) {
        return Promise.resolve(createResponse(runs))
      }

      if (url.includes('/orders')) {
        expect(url).toContain('ticker=TEST')
        return Promise.resolve(createResponse(orders))
      }

      if (url.includes('/backtests/configs')) {
        return Promise.resolve(createResponse(backtests))
      }

      return Promise.resolve(createResponse(strategy))
    })
    vi.stubGlobal('fetch', fetchMock)

    renderPage()

    expect(await screen.findByTestId('strategy-run-history')).toBeInTheDocument()
    expect(screen.getByTestId('strategy-config-editor')).toBeInTheDocument()
    expect(await screen.findByTestId('run-history-list')).toBeInTheDocument()
    expect(await screen.findByText('Backtests')).toBeInTheDocument()
    expect(await screen.findByText('Recent Orders')).toBeInTheDocument()

    const backtestsHeading = screen.getByRole('heading', { name: 'Backtests' })
    const recentOrdersHeading = screen.getByRole('heading', { name: 'Recent Orders' })
    expect(backtestsHeading.compareDocumentPosition(recentOrdersHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  }, 15_000)
})

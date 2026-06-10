import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiClientError } from '@/lib/api/client'
import { BacktestDetailPage } from '@/pages/backtest-detail-page'
import type { BacktestConfig, BacktestRun } from '@/lib/api/types'

const backtestId = 'backtest-1'

const apiClientMock = vi.hoisted(() => ({
  getBacktestConfig: vi.fn(),
  listBacktestRuns: vi.fn(),
  runBacktestConfig: vi.fn(),
}))

vi.mock('@/lib/api/client', () => ({
  apiClient: apiClientMock,
  ApiClientError: class ApiClientError extends Error {
    status: number

    constructor(message: string, status: number) {
      super(message)
      this.status = status
    }
  },
}))

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })

  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/backtests/${backtestId}`]}>
        <Routes>
          <Route path="backtests/:id" element={children} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

const configFixture: BacktestConfig = {
  id: backtestId,
  strategy_id: 'strategy-1',
  name: 'AAPL Mean Reversion',
  description: 'A simple backtest config',
  start_date: '2025-01-01T00:00:00Z',
  end_date: '2025-01-31T00:00:00Z',
  simulation: { initial_capital: 100000 },
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
}

const runFixture: BacktestRun = {
  id: 'run-1',
  backtest_config_id: backtestId,
  metrics: {
    total_return: 0.12,
    buy_and_hold_return: 0.08,
    max_drawdown: 0.05,
    sharpe_ratio: 1.4,
    sortino_ratio: 1.7,
    calmar_ratio: 2.1,
    alpha: 0.01,
    beta: 0.9,
    win_rate: 0.55,
    profit_factor: 1.3,
    avg_win_loss_ratio: 1.2,
    volatility: 0.18,
    start_equity: 100000,
    end_equity: 112000,
    total_bars: 200,
    realized_pnl: 12000,
    unrealized_pnl: 0,
  },
  trade_log: [],
  equity_curve: [],
  run_timestamp: '2025-02-01T12:00:00Z',
  duration: '00:10:00',
  prompt_version: 'v1',
  prompt_version_hash: 'abcdef1234567890',
  created_at: '2025-02-01T12:00:00Z',
  updated_at: '2025-02-01T12:00:00Z',
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('BacktestDetailPage', () => {
  it('shows a truthful empty state and run CTA when no runs exist yet', async () => {
    apiClientMock.getBacktestConfig.mockResolvedValue(configFixture)
    apiClientMock.listBacktestRuns.mockResolvedValue({ data: [] })
    apiClientMock.runBacktestConfig.mockResolvedValue(runFixture)

    render(<BacktestDetailPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('backtest-runs-empty')).toBeInTheDocument()
    expect(screen.getByText('No backtest runs yet')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('empty-run-backtest-button'))

    await waitFor(() => expect(apiClientMock.runBacktestConfig).toHaveBeenCalledWith(backtestId))
    expect(await screen.findByTestId('backtest-metrics-card')).toBeInTheDocument()
  })

  it('shows a selection empty state when runs exist but none is selected', async () => {
    apiClientMock.getBacktestConfig.mockResolvedValue(configFixture)
    apiClientMock.listBacktestRuns.mockResolvedValue({ data: [runFixture] })

    render(<BacktestDetailPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('backtest-run-selection-empty')).toBeInTheDocument()
    expect(screen.getByText('Select a run to inspect metrics')).toBeInTheDocument()
    expect(screen.getByTestId('backtest-config-note')).toHaveTextContent(
      'Top card shows the configuration only.',
    )
  })

  it('selects the returned run after running the backtest', async () => {
    apiClientMock.getBacktestConfig.mockResolvedValue(configFixture)
    apiClientMock.listBacktestRuns.mockResolvedValue({ data: [] })
    apiClientMock.runBacktestConfig.mockResolvedValue(runFixture)

    render(<BacktestDetailPage />, { wrapper: Wrapper })

    fireEvent.click(await screen.findByTestId('run-backtest-button'))

    await waitFor(() => expect(apiClientMock.runBacktestConfig).toHaveBeenCalledWith(backtestId))
    expect(await screen.findByTestId('backtest-metrics-card')).toBeInTheDocument()
    expect(screen.getByTestId('backtest-config-note')).toBeInTheDocument()
  })

  it('shows an unavailable state when run history is not configured', async () => {
    apiClientMock.getBacktestConfig.mockResolvedValue(configFixture)
    apiClientMock.listBacktestRuns.mockRejectedValue(new ApiClientError('Request failed with status 501', 501))

    render(<BacktestDetailPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('backtest-runs-unavailable')).toBeInTheDocument()
    expect(screen.getByText('Run history unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })
})

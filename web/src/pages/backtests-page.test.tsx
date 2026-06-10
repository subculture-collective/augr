import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { BacktestsPage } from '@/pages/backtests-page'

const apiClientMock = vi.hoisted(() => ({
  listBacktestConfigs: vi.fn(),
  listStrategies: vi.fn(),
  listBacktestRuns: vi.fn(),
  createBacktestConfig: vi.fn(),
  runBacktestConfig: vi.fn(),
}))

vi.mock('@/lib/api/client', () => ({
  apiClient: apiClientMock,
}))

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })

  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/backtests']}>
        <Routes>
          <Route path="backtests" element={children} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('BacktestsPage', () => {
  it('shows last run empty state for configs with no runs', async () => {
    apiClientMock.listStrategies.mockResolvedValue({ data: [{ id: 'strategy-1', name: 'Mean Reversion' }] })
    apiClientMock.listBacktestConfigs.mockResolvedValue({
      data: [
        {
          id: 'config-1',
          strategy_id: 'strategy-1',
          name: 'Config One',
          description: '',
          start_date: '2025-01-01T00:00:00Z',
          end_date: '2025-01-31T00:00:00Z',
          simulation: { initial_capital: 100000 },
          created_at: '2025-01-01T00:00:00Z',
          updated_at: '2025-01-02T00:00:00Z',
          latest_run_summary: undefined,
        },
      ],
      total: 1,
    })

    render(<BacktestsPage />, { wrapper: Wrapper })

    const lastRun = await screen.findByTestId('backtest-last-run-config-1')
    await waitFor(() => expect(lastRun).toHaveTextContent('No runs yet'))
    expect(screen.getByRole('heading', { name: /configuration inventory/i })).toBeInTheDocument()
    expect(apiClientMock.listBacktestRuns).not.toHaveBeenCalled()
  })

  it('shows the latest run summary on each config card', async () => {
    apiClientMock.listStrategies.mockResolvedValue({ data: [{ id: 'strategy-1', name: 'Mean Reversion' }] })
    apiClientMock.listBacktestConfigs.mockResolvedValue({
      data: [
        {
          id: 'config-1',
          strategy_id: 'strategy-1',
          name: 'Config One',
          description: '',
          start_date: '2025-01-01T00:00:00Z',
          end_date: '2025-01-31T00:00:00Z',
          simulation: { initial_capital: 100000 },
          created_at: '2025-01-01T00:00:00Z',
          updated_at: '2025-01-02T00:00:00Z',
          latest_run_summary: {
            id: 'run-1',
            backtest_config_id: 'config-1',
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
            run_timestamp: '2025-02-01T12:00:00Z',
          },
        },
      ],
      total: 1,
    })

    render(<BacktestsPage />, { wrapper: Wrapper })

    const lastRun = await screen.findByTestId('backtest-last-run-config-1')
    await waitFor(() => expect(lastRun).toHaveTextContent('Latest run result'))
    expect(lastRun).toHaveTextContent('total return 12.00%')
    expect(lastRun).toHaveTextContent('max drawdown 5.00%')
    expect(lastRun).toHaveTextContent('sharpe ratio 1.40')
    expect(apiClientMock.listBacktestRuns).not.toHaveBeenCalled()
  })
})

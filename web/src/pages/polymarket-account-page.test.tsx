import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'

import { PolymarketAccountPage } from '@/pages/polymarket-account-page'
import { ApiClientError } from '@/lib/api/client'

const apiMocks = vi.hoisted(() => ({
  getPolymarketAccount: vi.fn(),
  listPolymarketAccountTrades: vi.fn(),
  getPolymarketJobsStatus: vi.fn(),
  setPolymarketAccountTracked: vi.fn(),
}))

vi.mock('@/lib/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api/client')>('@/lib/api/client')
  return { ...actual, apiClient: apiMocks as unknown as typeof actual.apiClient }
})

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}><MemoryRouter initialEntries={['/polymarket/accounts/0xabc']}><Routes><Route path="polymarket/accounts/:address" element={children} /></Routes></MemoryRouter></QueryClientProvider>
}

const baseAccount = {
  address: '0xabc',
  first_seen: '2025-01-01T00:00:00Z',
  last_active: '2025-01-02T00:00:00Z',
  total_trades: 10,
  total_volume: 100,
  markets_entered: 4,
  markets_won: 3,
  markets_lost: 1,
  win_rate: 0.75,
  resolved_markets: 4,
  bayesian_win_rate: 0.68,
  consistency_score: 1.23,
  avg_position: 1,
  max_position: 5,
  early_entry_rate: 0.25,
  tracked: true,
  updated_at: '2025-01-01T00:00:00Z',
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

it('renders account stats', async () => {
  apiMocks.getPolymarketAccount.mockResolvedValue(baseAccount)
  apiMocks.listPolymarketAccountTrades.mockResolvedValue({
    data: [
      {
        id: 'trade-1',
        account_address: '0xabc',
        market_slug: 'market-1',
        side: 'YES',
        action: 'buy',
        price: 0.42,
        size_usdc: 15,
        timestamp: '2025-01-02T00:00:00Z',
        created_at: '2025-01-02T00:00:00Z',
      },
    ],
    limit: 100,
    offset: 0,
  })
  apiMocks.getPolymarketJobsStatus.mockResolvedValue([])
  apiMocks.setPolymarketAccountTracked.mockResolvedValue({ ok: true })

  render(<PolymarketAccountPage />, { wrapper: Wrapper })

  expect(await screen.findByTestId('polymarket-account-page')).toBeInTheDocument()
  expect(await screen.findByText('0xabc')).toBeInTheDocument()
  expect(await screen.findByText('total_volume')).toBeInTheDocument()
  expect(await screen.findByText('Trades')).toBeInTheDocument()
})

it('explains when wallet trades have not been ingested yet', async () => {
  apiMocks.getPolymarketAccount.mockResolvedValue({ ...baseAccount, total_trades: 0, total_volume: 0, markets_entered: 0, markets_won: 0, markets_lost: 0, win_rate: 0, resolved_markets: 0, bayesian_win_rate: 0, consistency_score: 0, max_position: 0, early_entry_rate: 0, tracked: false })
  apiMocks.listPolymarketAccountTrades.mockResolvedValue({ data: [], limit: 100, offset: 0 })
  apiMocks.getPolymarketJobsStatus.mockResolvedValue([
    {
      name: 'polymarket_profiles',
      description: 'Fetch recent Polymarket trades and update account profiles',
      schedule: '*/20 * * * *',
      last_result: 'ok: done',
      run_count: 0,
      error_count: 0,
      running: false,
      enabled: true,
    },
    {
      name: 'polymarket_resolutions',
      description: 'Process resolved Polymarket markets and update win rates',
      schedule: '0 * * * *',
      last_result: 'never run',
      run_count: 0,
      error_count: 0,
      running: false,
      enabled: true,
    },
  ])
  apiMocks.setPolymarketAccountTracked.mockResolvedValue({ ok: true })

  render(<PolymarketAccountPage />, { wrapper: Wrapper })

  expect(await screen.findByText(/No wallet trades ingested yet/i)).toBeInTheDocument()
  expect(screen.getByText(/confirm the Polymarket profile and resolution jobs/i)).toBeInTheDocument()
  expect(screen.getByText('polymarket_profiles')).toBeInTheDocument()
  expect(screen.getByText('polymarket_resolutions')).toBeInTheDocument()
})

it('shows an explicit not-configured state when the account api is unavailable', async () => {
  apiMocks.getPolymarketAccount.mockRejectedValue(new ApiClientError('polymarket accounts not configured', 503, 'ERR_NOT_IMPLEMENTED'))
  apiMocks.listPolymarketAccountTrades.mockResolvedValue({ data: [], limit: 100, offset: 0 })
  apiMocks.getPolymarketJobsStatus.mockResolvedValue([])
  apiMocks.setPolymarketAccountTracked.mockResolvedValue({ ok: true })

  render(<PolymarketAccountPage />, { wrapper: Wrapper })

  expect(await screen.findByText(/Polymarket profiles API unavailable/i)).toBeInTheDocument()
  expect(screen.getByText(/Enable the Polymarket account repository/i)).toBeInTheDocument()
})

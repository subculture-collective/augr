import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'

import { PolymarketPage } from '@/pages/polymarket-page'

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}><MemoryRouter>{children}</MemoryRouter></QueryClientProvider>
}

vi.mock('@/lib/api/client', () => ({ apiClient: { listStrategies: vi.fn().mockResolvedValue({ data: [{ id: '1', name: 'PM', market_type: 'polymarket' }] }), listPolymarketAccounts: vi.fn().mockResolvedValue({ data: [{ address: '0xabc', first_seen: '2025-01-01T00:00:00Z', total_trades: 1, total_volume: 10, markets_entered: 1, markets_won: 1, markets_lost: 0, win_rate: 1, avg_position: 1, max_position: 2, early_entry_rate: 0.5, tracked: true, updated_at: '2025-01-01T00:00:00Z' }], limit: 25, offset: 0 }), listPolymarketWatched: vi.fn().mockResolvedValue({ data: [] }), getPolymarketJobsStatus: vi.fn().mockResolvedValue([]), listPolymarketRecentTrades: vi.fn().mockResolvedValue({ data: [] }), listPolymarketRecentSignals: vi.fn().mockResolvedValue({ data: [{ id: 's1', received_at: '2025-01-01T00:00:00Z', source: 'polymarket-whale', title: 'Signal', body: '', urgency: 1, summary: '', recommended_action: '', affected_strategy_ids: [] }], total: 1 }) } }))

afterEach(() => cleanup())

it('renders polymarket sections and accounts', async () => {
  render(<PolymarketPage />, { wrapper: Wrapper })
  expect(await screen.findByTestId('polymarket-page')).toBeInTheDocument()
  expect(screen.getByText('Polymarket Jobs')).toBeInTheDocument()
  expect(screen.getAllByText('Tracked Wallets').length).toBeGreaterThan(0)
  expect(await screen.findByText(/0xabc/i)).toBeInTheDocument()
})

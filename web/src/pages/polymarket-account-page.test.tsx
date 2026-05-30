import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'

import { PolymarketAccountPage } from '@/pages/polymarket-account-page'

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}><MemoryRouter initialEntries={['/polymarket/accounts/0xabc']}><Routes><Route path="polymarket/accounts/:address" element={children} /></Routes></MemoryRouter></QueryClientProvider>
}

vi.mock('@/lib/api/client', () => ({ apiClient: { getPolymarketAccount: vi.fn().mockResolvedValue({ address: '0xabc', first_seen: '2025-01-01T00:00:00Z', total_trades: 10, total_volume: 100, markets_entered: 4, markets_won: 3, markets_lost: 1, win_rate: 0.75, avg_position: 1, max_position: 5, early_entry_rate: 0.25, tracked: true, updated_at: '2025-01-01T00:00:00Z' }), listPolymarketAccountTrades: vi.fn().mockResolvedValue({ data: [] }) } }))

afterEach(() => cleanup())

it('renders account stats', async () => {
  render(<PolymarketAccountPage />, { wrapper: Wrapper })
  expect(await screen.findByTestId('polymarket-account-page')).toBeInTheDocument()
  expect(screen.getByText('0xabc')).toBeInTheDocument()
  expect(screen.getByText('total_volume')).toBeInTheDocument()
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { StockDetailPage } from '@/pages/stock-detail-page'

const ticker = 'AAPL'

const apiClientMock = vi.hoisted(() => ({
  listStrategies: vi.fn(),
  listOrders: vi.fn(),
  listTrades: vi.fn(),
  listPositions: vi.fn(),
  getEarningsCalendar: vi.fn(),
  getFilings: vi.fn(),
  getHistoricalOHLCV: vi.fn(),
  listNews: vi.fn(),
  getSocialSentiment: vi.fn(),
  listUniverse: vi.fn(),
  listBacktestConfigs: vi.fn(),
}))

vi.mock('@/lib/api/client', () => ({
  apiClient: apiClientMock,
}))

vi.mock('@/components/calendar/upcoming-events-widget', () => ({
  UpcomingEventsWidget: () => <div data-testid="upcoming-events-mock" />,
}))

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })

  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/stocks/${ticker}`]}>
        <Routes>
          <Route path="stocks/:ticker" element={children} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

function createBars() {
  return Array.from({ length: 60 }).map((_, index) => {
    const base = 100 + index
    return {
      ticker,
      provider: 'test-feed',
      timeframe: '1d',
      timestamp: `2025-01-${String(index + 1).padStart(2, '0')}T12:00:00Z`,
      open: base,
      high: base + 3,
      low: base - 2,
      close: base + 1,
      volume: 1_000_000 + index * 10_000,
    }
  })
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('StockDetailPage', () => {
  it('exposes chart indicator toggles and hover/selected bar summaries', async () => {
    apiClientMock.listStrategies.mockResolvedValue({ data: [] })
    apiClientMock.listOrders.mockResolvedValue({ data: [] })
    apiClientMock.listTrades.mockResolvedValue({ data: [] })
    apiClientMock.listPositions.mockResolvedValue({ data: [] })
    apiClientMock.getEarningsCalendar.mockResolvedValue([])
    apiClientMock.getFilings.mockResolvedValue([])
    apiClientMock.getHistoricalOHLCV.mockResolvedValue(createBars())
    apiClientMock.listNews.mockResolvedValue([])
    apiClientMock.getSocialSentiment.mockResolvedValue([])
    apiClientMock.listUniverse.mockResolvedValue({ data: [{ ticker, name: 'Apple Inc.', exchange: 'XNAS', index_group: 'nasdaq', watch_score: 0.9, active: true }] })
    apiClientMock.listBacktestConfigs.mockResolvedValue({ data: [] })

    render(<StockDetailPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('stock-detail-page')).toBeInTheDocument()
    expect(screen.getByTestId('upcoming-events-mock')).toBeInTheDocument()

    expect(await screen.findByText('Latest close')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'SMA 50' }))
    fireEvent.click(screen.getByRole('button', { name: 'Volume overlay' }))
    expect(screen.getByRole('button', { name: 'SMA 50' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Volume overlay' })).toHaveAttribute('aria-pressed', 'true')
  })
})

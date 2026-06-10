import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { WatchlistTable } from '@/components/universe/watchlist-table'

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={client}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  )
}

describe('WatchlistTable', () => {
  it('renders scored tickers using score field', () => {
    render(
      <WatchlistTable
        tickers={[
          {
            ticker: 'AAPL',
            score: 0.82,
            reasons: ['gap_up'],
            day_volume: 1200000,
            day_close: 182.35,
            change_pct: 1.2,
            gap_pct: 0.6,
          },
        ]}
      />,
      { wrapper: Wrapper },
    )

    expect(screen.getByText('AAPL')).toBeInTheDocument()
    expect(screen.getByText('0.82')).toBeInTheDocument()
    expect(screen.getByText('gap_up')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'AAPL' })).toHaveAttribute('href', '/stocks/AAPL')
    expect(screen.getByRole('link', { name: 'Discovery' })).toHaveAttribute(
      'href',
      '/discovery?tickers=AAPL',
    )
  })

  it('falls back to watch_score when score is absent', () => {
    render(
      <WatchlistTable
        tickers={[
          {
            ticker: 'MSFT',
            name: 'Microsoft',
            exchange: 'XNAS',
            index_group: 'nasdaq',
            watch_score: 0.64,
            active: true,
          },
        ]}
      />,
      { wrapper: Wrapper },
    )

    expect(screen.getByText('MSFT')).toBeInTheDocument()
    expect(screen.getByText('0.64')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
  })

  it('renders 0.00 when score is undefined (null-guard)', () => {
    render(
      <WatchlistTable
        tickers={[
          {
            ticker: 'NVDA',
            score: undefined as unknown as number,
            reasons: [],
            day_volume: 500000,
            day_close: 300,
            change_pct: 0,
            gap_pct: 0,
          },
        ]}
      />,
      { wrapper: Wrapper },
    )

    expect(screen.getByText('NVDA')).toBeInTheDocument()
    expect(screen.getByText('0.00')).toBeInTheDocument()
  })

  it('shows current holding and strategy state badges when notes are present', () => {
    render(
      <WatchlistTable
        tickers={[
          {
            ticker: 'TSLA',
            score: 0.55,
            reasons: [],
            day_volume: 1000,
            day_close: 250,
            change_pct: -0.5,
            gap_pct: 0,
            active: true,
            notes: 'Current holding',
            strategy_count: 2,
            position_count: 1,
          },
        ]}
      />,
      { wrapper: Wrapper },
    )

    expect(screen.getAllByText('Current holding').length).toBeGreaterThan(0)
    expect(screen.getByText('2 strategies')).toBeInTheDocument()
    expect(screen.getByText('1 positions')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'TSLA' })).toHaveAttribute('href', '/stocks/TSLA')
  })
})

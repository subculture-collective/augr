import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SignalsPage } from '@/pages/signals-page'

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
      },
    },
  })

  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const baseSignal = {
  id: '00000000-0000-0000-0000-000000000001',
  received_at: '2025-01-01T12:00:00Z',
  source: 'market-feed',
  title: 'Momentum fading',
  body: 'Momentum faded after the opening move.',
  urgency: 4,
  summary: 'Momentum faded after the opening move.',
  recommended_action: 'monitor',
  affected_strategy_ids: [],
}

describe('SignalsPage', () => {
  it('explains empty evaluated signals as a transient in-memory buffer', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ data: [], total: 0 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<SignalsPage />, { wrapper: Wrapper })

    const emptyState = await screen.findByTestId('signals-evaluated-empty')
    expect(emptyState).toHaveTextContent('in-memory buffer')
    expect(emptyState).toHaveTextContent('signal stack has run for the first time')
  })

  it('explains urgency filters can hide evaluated signals', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ data: [], total: 0 }),
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<SignalsPage />, { wrapper: Wrapper })

    fireEvent.click(await screen.findByRole('button', { name: '4+' }))

    const emptyState = await screen.findByTestId('signals-evaluated-empty')
    expect(emptyState).toHaveTextContent('Lower the urgency filter')
    expect(emptyState).toHaveTextContent('Signals below the current filter are hidden')
  })

  it('shows an explicit API error when the trigger log is unavailable', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [baseSignal], total: 1 }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 501,
        json: async () => ({ error: 'Signal intelligence not configured', code: 'NOT_CONFIGURED' }),
      })
    vi.stubGlobal('fetch', fetchMock)

    render(<SignalsPage />, { wrapper: Wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /trigger log/i }))

    const errorState = await screen.findByTestId('signals-triggers-error')
    expect(errorState).toHaveTextContent('not configured on this deployment')
  })

  it('renders the trigger log empty state with in-memory guidance', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [baseSignal], total: 1 }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [], total: 0 }),
      })
    vi.stubGlobal('fetch', fetchMock)

    render(<SignalsPage />, { wrapper: Wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /trigger log/i }))

    const emptyState = await screen.findByTestId('signals-triggers-empty')
    expect(emptyState).toHaveTextContent('in-memory')
    expect(emptyState).toHaveTextContent('urgency 3')
  })

  it('shows an explicit API error when the watchlist is unavailable', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [baseSignal], total: 1 }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 501,
        json: async () => ({ error: 'Signal intelligence not configured', code: 'NOT_CONFIGURED' }),
      })
    vi.stubGlobal('fetch', fetchMock)

    render(<SignalsPage />, { wrapper: Wrapper })

    fireEvent.click(await screen.findByRole('button', { name: /watchlist/i }))

    const errorState = await screen.findByTestId('signals-watchlist-error')
    expect(errorState).toHaveTextContent('not configured on this deployment')
    expect(screen.queryByText(/No watch terms yet/i)).not.toBeInTheDocument()
  })
})

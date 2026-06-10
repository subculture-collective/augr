import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { OptionsPage } from '@/pages/options-page'
import { ApiClientError } from '@/lib/api/client'
import type { OptionSnapshot, ResearchOpportunity } from '@/lib/api/types'

const mockOptionsChain = vi.hoisted((): OptionSnapshot[] => [
  {
    contract: {
      occ_symbol: 'TSLA260619C00100000',
      underlying: 'TSLA',
      option_type: 'call',
      strike: 100,
      expiry: '2026-06-19',
      multiplier: 100,
    },
    greeks: { delta: 0.52, gamma: 0.01, theta: -0.02, vega: 0.11, iv: 0.25 },
    bid: 10.1,
    ask: 10.4,
    mid: 10.25,
    last: 10.3,
    volume: 1234,
    open_interest: 5678,
  },
  {
    contract: {
      occ_symbol: 'TSLA260619P00095000',
      underlying: 'TSLA',
      option_type: 'put',
      strike: 95,
      expiry: '2026-06-19',
      multiplier: 100,
    },
    greeks: { delta: -0.47, gamma: 0.02, theta: -0.03, vega: 0.09, iv: 0.31 },
    bid: 6.2,
    ask: 6.5,
    mid: 6.35,
    last: 6.4,
    volume: 845,
    open_interest: 2468,
  },
  {
    contract: {
      occ_symbol: 'TSLA260717C00110000',
      underlying: 'TSLA',
      option_type: 'call',
      strike: 110,
      expiry: '2026-07-17',
      multiplier: 100,
    },
    greeks: { delta: 0.4, gamma: 0.01, theta: -0.02, vega: 0.08, iv: 0.27 },
    bid: 8.1,
    ask: 8.4,
    mid: 8.25,
    last: 8.3,
    volume: 600,
    open_interest: 1800,
  },
])

const mockOpportunities = vi.hoisted((): ResearchOpportunity[] => [
  {
    decision: {
      id: 'opp-1',
      market_type: 'options',
      instrument_key: 'TSLA260619C00100000',
      side: 'buy',
      fair_value: 11.25,
      executable_price: 10.25,
      spread: 0.3,
      depth: 1234,
      gross_ev: 18.2,
      updated_at: '2026-06-10T12:00:00Z',
      net_ev: 14.5,
      kelly_fraction: 0.08,
      proposed_size: 5,
      approved_size: 3,
      risk_status: 'approved',
      status: 'candidate',
      risk_reasons: ['good edge'],
      regime_tags: ['options-scanner'],
      created_at: '2026-06-10T11:55:00Z',
    },
  },
])

const apiClientMock = vi.hoisted(() => ({
  getOptionsChain: vi.fn(),
  listOptionsOpportunities: vi.fn(),
}))

vi.mock('@/lib/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api/client')>('@/lib/api/client')
  return {
    ...actual,
    apiClient: apiClientMock,
  }
})

function renderAt(search = '') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/options${search}`]}>
        <Routes>
          <Route path="/options" element={<OptionsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('OptionsPage', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('renders the page without crashing', () => {
    renderAt()
    expect(screen.getByTestId('options-page')).toBeInTheDocument()
  })

  it('offers quick-pick tickers without auto-loading', async () => {
    const user = userEvent.setup()

    renderAt()

    await user.click(screen.getByRole('button', { name: 'AAPL' }))

    expect(screen.getByRole<HTMLInputElement>('textbox', { name: /underlying ticker/i }).value).toBe('AAPL')
    expect(apiClientMock.getOptionsChain).not.toHaveBeenCalled()
  })

  it('shows truthful no-ticker empty states before any lookup', () => {
    renderAt()

    expect(screen.getByTestId('options-empty')).toHaveTextContent('No ticker selected yet. Search for a ticker to view its options chain.')
    expect(screen.getByTestId('options-opportunities-no-ticker')).toHaveTextContent(
      'No ticker selected yet. Search a ticker above to enable the scanner.',
    )
  })

  it('seeds ticker input from ?ticker= URL param and loads chain', async () => {
    apiClientMock.getOptionsChain.mockResolvedValueOnce(mockOptionsChain)
    apiClientMock.listOptionsOpportunities.mockResolvedValueOnce({ data: mockOpportunities })

    renderAt('?ticker=TSLA')

    await waitFor(() => {
      expect(apiClientMock.getOptionsChain).toHaveBeenCalledWith(
        'TSLA',
        expect.objectContaining({ expiry: undefined, type: undefined }),
      )
    })

    expect(screen.getByRole<HTMLInputElement>('textbox', { name: /underlying ticker/i }).value).toBe('TSLA')
    expect(await screen.findByText('Expiry 2026-06-19')).toBeInTheDocument()
    expect(screen.getAllByText('call')[0]).toBeInTheDocument()
    expect(screen.getByText('100.00')).toBeInTheDocument()
  })

  it('switches among loaded expiries and refetches chain and opportunities', async () => {
    const user = userEvent.setup()
    apiClientMock.getOptionsChain.mockResolvedValue(mockOptionsChain)
    apiClientMock.listOptionsOpportunities.mockResolvedValue({ data: mockOpportunities })

    renderAt('?ticker=TSLA')

    await screen.findByRole('option', { name: '2026-07-17' })
    const expirySelect = screen.getByLabelText('Loaded expiries')
    expect(expirySelect).toHaveValue('')

    await user.selectOptions(expirySelect, '2026-07-17')

    await waitFor(() => {
      expect(apiClientMock.getOptionsChain).toHaveBeenLastCalledWith(
        'TSLA',
        expect.objectContaining({ expiry: '2026-07-17', type: undefined }),
      )
    })
    await waitFor(() => {
      expect(apiClientMock.listOptionsOpportunities).toHaveBeenLastCalledWith(
        'TSLA',
        expect.objectContaining({ expiry: '2026-07-17', type: undefined }),
      )
    })

    expect(expirySelect).toHaveValue('2026-07-17')
    expect(await screen.findByText('Expiry 2026-07-17')).toBeInTheDocument()
  })

  it('loads and displays contracts after submit', async () => {
    const user = userEvent.setup()
    apiClientMock.getOptionsChain.mockResolvedValueOnce(mockOptionsChain)
    apiClientMock.listOptionsOpportunities.mockResolvedValueOnce({ data: mockOpportunities })

    renderAt()

    await user.type(screen.getByRole('textbox', { name: /underlying ticker/i }), 'tsla')
    await user.click(screen.getByRole('button', { name: /load chain/i }))

    await waitFor(() => {
      expect(apiClientMock.getOptionsChain).toHaveBeenCalledWith(
        'TSLA',
        expect.objectContaining({ expiry: undefined, type: undefined }),
      )
    })

    expect(await screen.findByText('Expiry 2026-06-19')).toBeInTheDocument()
    expect(screen.getByText('put')).toBeInTheDocument()
    expect(screen.getByText('95.00')).toBeInTheDocument()
    expect(await screen.findByText('1 opportunities')).toBeInTheDocument()
  })

  it('explains when the provider returns no contracts', async () => {
    const user = userEvent.setup()
    apiClientMock.getOptionsChain.mockResolvedValueOnce([])
    apiClientMock.listOptionsOpportunities.mockResolvedValueOnce({ data: [] })

    renderAt()

    await user.type(screen.getByRole('textbox', { name: /underlying ticker/i }), 'tsla')
    await user.click(screen.getByRole('button', { name: /load chain/i }))

    expect(await screen.findByTestId('options-empty-contracts')).toHaveTextContent('No contracts returned for this ticker.')
    expect(await screen.findByTestId('options-opportunities-empty')).toHaveTextContent('No opportunities because no contracts were returned for this ticker.')
  })

  it('explains when filters or defaults reject all opportunities', async () => {
    const user = userEvent.setup()
    apiClientMock.getOptionsChain.mockResolvedValueOnce(mockOptionsChain)
    apiClientMock.listOptionsOpportunities.mockResolvedValueOnce({ data: [] })

    renderAt()

    await user.type(screen.getByRole('textbox', { name: /underlying ticker/i }), 'tsla')
    await user.type(screen.getByLabelText(/expiry date filter/i), '2026-06-19')
    await user.click(screen.getByRole('button', { name: /load chain/i }))

    expect(await screen.findByTestId('options-opportunities-empty')).toHaveTextContent(
      'No opportunities matched the selected expiry/type filters or scanner defaults.',
    )
  })

  it('shows a configured-provider message when the chain endpoint is not available', async () => {
    const user = userEvent.setup()
    apiClientMock.getOptionsChain.mockRejectedValueOnce(new ApiClientError('options data provider not configured', 501))
    apiClientMock.listOptionsOpportunities.mockResolvedValueOnce({ data: [] })

    renderAt()

    await user.type(screen.getByRole('textbox', { name: /underlying ticker/i }), 'tsla')
    await user.click(screen.getByRole('button', { name: /load chain/i }))

    expect(await screen.findByTestId('options-error')).toHaveTextContent('Options provider not configured on this deployment.')
    expect(screen.getByTestId('options-opportunities-unavailable')).toHaveTextContent('Options provider not configured on this deployment.')
  })
})

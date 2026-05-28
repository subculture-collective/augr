import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { OptionsPage } from '@/pages/options-page'
import type { OptionSnapshot } from '@/lib/api/types'

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
])

const apiClientMock = vi.hoisted(() => ({
  getOptionsChain: vi.fn().mockResolvedValue(mockOptionsChain),
}))

vi.mock('@/lib/api/client', () => ({
  apiClient: apiClientMock,
}))

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

  it('seeds ticker input from ?ticker= URL param and loads chain', async () => {
    renderAt('?ticker=TSLA')

    await waitFor(() => {
      expect(apiClientMock.getOptionsChain).toHaveBeenCalledWith(
        'TSLA',
        expect.objectContaining({ expiry: undefined, type: undefined }),
      )
    })

    expect(screen.getByRole<HTMLInputElement>('textbox', { name: /underlying ticker/i }).value).toBe('TSLA')
    expect(await screen.findByText('Expiry 2026-06-19')).toBeInTheDocument()
    expect(screen.getByText('call')).toBeInTheDocument()
    expect(screen.getByText('100.00')).toBeInTheDocument()
  })

  it('loads and displays contracts after submit', async () => {
    const user = userEvent.setup()
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
  })
})

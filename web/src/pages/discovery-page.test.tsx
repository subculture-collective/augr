import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { DiscoveryPage } from '@/pages/discovery-page'
import { ApiClientError } from '@/lib/api/client'

const apiClientMock = vi.hoisted(() => ({
  runDiscovery: vi.fn(),
}))

vi.mock('@/lib/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api/client')>('@/lib/api/client')
  return {
    ...actual,
    apiClient: apiClientMock,
  }
})

function renderPage(initialPath = '/discovery') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })

  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initialPath]}>
        <DiscoveryPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('DiscoveryPage', () => {
  it('seeds tickers from discovery links', () => {
    renderPage('/discovery?tickers=tsla,nvda')

    expect(screen.getByLabelText('Tickers')).toHaveValue('TSLA, NVDA')
  })

  it('shows a dedicated rate-limit panel for 429 responses', async () => {
    const user = userEvent.setup()
    apiClientMock.runDiscovery.mockRejectedValueOnce(new ApiClientError('rate limit exceeded', 429, 'ERR_RATE_LIMITED'))

    renderPage()

    await user.click(screen.getByRole('button', { name: /run discovery/i }))

    expect(await screen.findByTestId('discovery-rate-limit')).toHaveTextContent(/wait a bit/i)
    expect(screen.getByTestId('discovery-rate-limit')).toHaveTextContent(/reduce how often/i)
    expect(screen.queryByTestId('discovery-error')).not.toBeInTheDocument()
  })

  it('keeps generic failures separate from rate limits', async () => {
    const user = userEvent.setup()
    apiClientMock.runDiscovery.mockRejectedValueOnce(new ApiClientError('boom', 500))

    renderPage()

    await user.click(screen.getByRole('button', { name: /run discovery/i }))

    await waitFor(() => expect(apiClientMock.runDiscovery).toHaveBeenCalled())
    expect(await screen.findByTestId('discovery-error')).toHaveTextContent('Discovery failed: boom')
    expect(screen.queryByTestId('discovery-rate-limit')).not.toBeInTheDocument()
  })
})

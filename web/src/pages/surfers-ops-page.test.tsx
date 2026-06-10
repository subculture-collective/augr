import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiClientError } from '@/lib/api/client'
import { SurfersOpsPage } from '@/pages/surfers-ops-page'

const apiClientMock = vi.hoisted(() => ({
  getPolymarketMarketDataStatus: vi.fn(),
  listRiskBreakers: vi.fn(),
  getBacktestDivergence: vi.fn(),
}))

vi.mock('@/lib/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api/client')>('@/lib/api/client')
  return {
    ...actual,
    apiClient: apiClientMock,
  }
})

describe('SurfersOpsPage', () => {
  afterEach(() => {
    cleanup()
    vi.resetAllMocks()
  })

  it('labels disabled services and empty data states clearly', async () => {
    const user = userEvent.setup()

    apiClientMock.getPolymarketMarketDataStatus.mockResolvedValue({
      enabled: false,
      ws_connections: 0,
      avg_jitter_ms: 0,
      dropped: 0,
      ready_slugs: [],
      recorder_lag_seconds: 0,
      updated_at: '2026-06-10T12:00:00Z',
    })
    apiClientMock.listRiskBreakers.mockResolvedValue({ tripped: [] })
    apiClientMock.getBacktestDivergence.mockRejectedValue(new ApiClientError('divergence not found', 404))

    render(<SurfersOpsPage />)

    expect(await screen.findByTestId('surfers-status-disabled')).toHaveTextContent('Feed disabled or not configured')
    expect(screen.getByText('No tripped breakers.')).toBeInTheDocument()

    await user.type(screen.getByLabelText(/strategy id/i), 'strategy-123')
    await user.click(screen.getByRole('button', { name: /load/i }))

    await waitFor(() => {
      expect(apiClientMock.getBacktestDivergence).toHaveBeenCalledWith('strategy-123')
    })

    expect(await screen.findByTestId('surfers-divergence-empty')).toHaveTextContent('No divergence data exists for this strategy.')
  })

  it('explains when dependencies are not configured', async () => {
    apiClientMock.getPolymarketMarketDataStatus.mockRejectedValue(
      new ApiClientError('polymarket feed unavailable', 503, 'ERR_NOT_IMPLEMENTED'),
    )
    apiClientMock.listRiskBreakers.mockRejectedValue(
      new ApiClientError('risk breaker not configured', 503, 'ERR_NOT_IMPLEMENTED'),
    )

    render(<SurfersOpsPage />)

    expect(await screen.findByTestId('surfers-status-unavailable')).toHaveTextContent('Polymarket feed is not configured on this deployment.')
    expect(screen.getByTestId('surfers-breakers-unavailable')).toHaveTextContent('Risk breaker service is not configured on this deployment.')
  })

  it('does not mask generic API failures as empty or disabled states', async () => {
    const user = userEvent.setup()

    apiClientMock.getPolymarketMarketDataStatus.mockRejectedValue(new ApiClientError('feed exploded', 500, 'ERR_INTERNAL'))
    apiClientMock.listRiskBreakers.mockRejectedValue(new ApiClientError('breaker exploded', 500, 'ERR_INTERNAL'))
    apiClientMock.getBacktestDivergence.mockRejectedValue(new ApiClientError('divergence exploded', 500, 'ERR_INTERNAL'))

    render(<SurfersOpsPage />)

    expect(await screen.findByTestId('surfers-status-error')).toHaveTextContent('Unable to load Polymarket feed status')
    expect(screen.getByTestId('surfers-breakers-error')).toHaveTextContent('Unable to load risk breaker state')
    expect(screen.queryByText('No tripped breakers.')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText(/strategy id/i), 'strategy-500')
    await user.click(screen.getByRole('button', { name: /load/i }))

    expect(await screen.findByTestId('surfers-divergence-error')).toHaveTextContent('Unable to load divergence for this strategy')
    expect(screen.queryByText('Load a strategy to inspect divergence.')).not.toBeInTheDocument()
  })
})

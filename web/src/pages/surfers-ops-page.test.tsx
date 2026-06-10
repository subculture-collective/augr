import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
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

    const statusPanel = await screen.findByTestId('surfers-status-panel')
    expect(within(statusPanel).getByText('Summary')).toBeInTheDocument()
    expect(within(statusPanel).getByText('Why no data')).toBeInTheDocument()
    expect(within(statusPanel).getByText('Last known state')).toBeInTheDocument()
    expect(within(statusPanel).getByText('Current status')).toBeInTheDocument()
    const disabledStatus = await screen.findByTestId('surfers-status-disabled')
    expect(disabledStatus).toHaveTextContent('Feed is disabled')
    expect(disabledStatus).not.toHaveTextContent('not configured')
    expect(screen.getByText('No tripped breakers.')).toBeInTheDocument()

    await user.type(screen.getByLabelText(/strategy id/i), 'strategy-123')
    await user.click(screen.getByRole('button', { name: /load/i }))

    await waitFor(() => {
      expect(apiClientMock.getBacktestDivergence).toHaveBeenCalledWith('strategy-123')
    })

    const divergencePanel = await screen.findByTestId('surfers-divergence-panel')
    expect(within(divergencePanel).getByText('404 means this strategy has no divergence record yet.')).toBeInTheDocument()
    expect(within(divergencePanel).getByText('Last known state')).toBeInTheDocument()
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

    const statusPanel = await screen.findByTestId('surfers-status-panel')
    expect(within(statusPanel).getByText('The backend returned 501/503, or this deployment does not expose the feed endpoint.')).toBeInTheDocument()
    expect(await screen.findByTestId('surfers-status-unavailable')).toHaveTextContent('Polymarket feed is not configured on this deployment.')

    const breakersPanel = await screen.findByTestId('surfers-breakers-panel')
    expect(within(breakersPanel).getByText('A 501/503 response means the breaker service is not configured on this deployment.')).toBeInTheDocument()
    expect(screen.getByTestId('surfers-breakers-unavailable')).toHaveTextContent('Risk breaker service is not configured on this deployment.')
  })

  it('does not mask generic API failures as empty or disabled states', async () => {
    const user = userEvent.setup()

    apiClientMock.getPolymarketMarketDataStatus.mockRejectedValue(new ApiClientError('feed exploded', 500, 'ERR_INTERNAL'))
    apiClientMock.listRiskBreakers.mockRejectedValue(new ApiClientError('breaker exploded', 500, 'ERR_INTERNAL'))
    apiClientMock.getBacktestDivergence.mockRejectedValue(new ApiClientError('divergence exploded', 500, 'ERR_INTERNAL'))

    render(<SurfersOpsPage />)

    const statusPanel = await screen.findByTestId('surfers-status-panel')
    expect(within(statusPanel).getByText('The API returned a non-configuration error, so this is not an empty state.')).toBeInTheDocument()
    expect(await screen.findByTestId('surfers-status-error')).toHaveTextContent('Unable to load Polymarket feed status')

    const breakersPanel = await screen.findByTestId('surfers-breakers-panel')
    expect(within(breakersPanel).getByText('The API returned an unexpected error, so the breaker state is not empty — it is unavailable.')).toBeInTheDocument()
    expect(screen.getByTestId('surfers-breakers-error')).toHaveTextContent('Unable to load risk breaker state')
    expect(screen.queryByText('No tripped breakers.')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText(/strategy id/i), 'strategy-500')
    await user.click(screen.getByRole('button', { name: /load/i }))

    const divergencePanel = await screen.findByTestId('surfers-divergence-panel')
    expect(within(divergencePanel).getByText('A non-configuration API error prevented the divergence lookup.')).toBeInTheDocument()
    expect(await screen.findByTestId('surfers-divergence-error')).toHaveTextContent('Unable to load divergence for this strategy')
    expect(screen.queryByText('Load a strategy to inspect divergence.')).not.toBeInTheDocument()
  })
})

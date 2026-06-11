import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { DecisionJournalPage } from '@/pages/decision-journal-page'
import { ApiClientError } from '@/lib/api/client'
import type { EngineStatus, TradeDecision } from '@/lib/api/types'

const apiClientMock = vi.hoisted(() => ({
  getRiskStatus: vi.fn(),
  listTradeDecisions: vi.fn(),
}))

vi.mock('@/lib/api/client', () => ({
  apiClient: apiClientMock,
  ApiClientError: class ApiClientError extends Error {
    status: number

    constructor(message: string, status: number) {
      super(message)
      this.status = status
    }
  },
}))

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })

  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/journal']}>{children}</MemoryRouter>
    </QueryClientProvider>
  )
}

const riskStatus: EngineStatus = {
  risk_status: 'normal',
  circuit_breaker: { state: 'open', reason: '', tripped_at: '', cooldown_end: '' },
  kill_switch: { active: false, reason: '', activated_at: '', mechanisms: [] },
  position_limits: {
    max_per_position_pct: 10,
    max_total_pct: 50,
    max_concurrent: 5,
    max_per_market_pct: 25,
  },
  updated_at: '2025-01-01T10:00:00Z',
}

const decisionFixture: TradeDecision = {
  id: 'decision-1',
  market_type: 'stock',
  instrument_key: 'AAPL-2025-03-21-C200',
  side: 'buy',
  fair_value: 1.2,
  executable_price: 1.1,
  spread: 0.1,
  depth: 100,
  gross_ev: 0.3,
  net_ev: 0.25,
  kelly_fraction: 0.1,
  proposed_size: 100,
  approved_size: 50,
  risk_status: 'approved',
  risk_reasons: ['Within limits'],
  regime_tags: ['trend'],
  prompt_text: 'system: trade carefully',
  llm_provider: 'openai',
  llm_model: 'gpt-4.1',
  prompt_tokens: 123,
  completion_tokens: 45,
  latency_ms: 678,
  cost_usd: 0.0123,
  status: 'candidate',
  created_at: '2025-01-01T10:00:00Z',
  updated_at: '2025-01-01T10:05:00Z',
  paper_order_id: 'paper-1',
  live_order_id: 'live-1',
  outcome: 'winner',
  strategy_id: 'strategy-1',
  pipeline_run_id: 'run-1',
  external_market_id: 'ext-1',
  evidence: null,
  features: null,
} satisfies TradeDecision

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('DecisionJournalPage', () => {
  it('shows a truthful empty state when no decisions exist yet', async () => {
    apiClientMock.getRiskStatus.mockResolvedValue(riskStatus)
    apiClientMock.listTradeDecisions.mockResolvedValue({ data: [] })

    render(<DecisionJournalPage />, { wrapper: Wrapper })

    expect(await screen.findByTestId('decision-journal-empty')).toBeInTheDocument()
    expect(screen.getByText('No decisions recorded yet')).toBeInTheDocument()
    expect(screen.getByText(/once the recorder writes/i)).toBeInTheDocument()
  })

  it('explains when filters hide the remaining journal rows', async () => {
    apiClientMock.getRiskStatus.mockResolvedValue(riskStatus)
    apiClientMock.listTradeDecisions.mockImplementation(async (params: { market_type?: string; status?: string }) => {
      if (params.market_type || params.status) return { data: [] }

      return { data: [decisionFixture] }
    })

    render(<DecisionJournalPage />, { wrapper: Wrapper })

    expect(await screen.findAllByText('Within limits')).toHaveLength(2)

    fireEvent.change(screen.getByLabelText(/market type filter/i), { target: { value: 'crypto' } })

    expect(await screen.findByTestId('decision-journal-empty-filters')).toBeInTheDocument()
    expect(screen.getByText('No decisions matched these filters')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /clear filters/i })).toBeInTheDocument()
  })

  it('renders prompt and llm metadata when recorded', async () => {
    apiClientMock.getRiskStatus.mockResolvedValue(riskStatus)
    apiClientMock.listTradeDecisions.mockResolvedValue({ data: [decisionFixture] })

    render(<DecisionJournalPage />, { wrapper: Wrapper })

    const metadata = await screen.findAllByTestId('decision-llm-metadata')
    expect(metadata).toHaveLength(2)
    metadata.forEach((node) => {
      expect(node).toHaveTextContent('openai')
      expect(node).toHaveTextContent('gpt-4.1')
      expect(node).toHaveTextContent('123 / 45')
      expect(node).toHaveTextContent('678ms')
      expect(node).toHaveTextContent('system: trade carefully')
    })
  })

  it('shows n/a when prompt and llm metadata are absent', async () => {
    apiClientMock.getRiskStatus.mockResolvedValue(riskStatus)
    const decisionWithoutMetadata: TradeDecision = { ...decisionFixture }
    delete decisionWithoutMetadata.prompt_text
    delete decisionWithoutMetadata.llm_provider
    delete decisionWithoutMetadata.llm_model
    delete decisionWithoutMetadata.prompt_tokens
    delete decisionWithoutMetadata.completion_tokens
    delete decisionWithoutMetadata.latency_ms
    delete decisionWithoutMetadata.cost_usd
    apiClientMock.listTradeDecisions.mockResolvedValue({ data: [decisionWithoutMetadata] })

    render(<DecisionJournalPage />, { wrapper: Wrapper })

    const metadata = await screen.findAllByTestId('decision-llm-metadata')
    expect(metadata).toHaveLength(2)
    metadata.forEach((node) => {
      expect(node).toHaveTextContent('n/a')
      expect(node).toHaveTextContent('No prompt text recorded for this decision.')
    })
  })

  it('shows an unavailable state when the journal endpoint is not configured', async () => {
    apiClientMock.getRiskStatus.mockResolvedValue(riskStatus)
    apiClientMock.listTradeDecisions.mockRejectedValue(new ApiClientError('Request failed with status 501', 501))

    render(<DecisionJournalPage />, { wrapper: Wrapper })

    await waitFor(() => expect(screen.getByTestId('decision-journal-unavailable')).toBeInTheDocument())
    expect(screen.getByText('Decision journal unavailable')).toBeInTheDocument()
  })
})

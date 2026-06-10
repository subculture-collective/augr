import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ReliabilityPage } from '@/pages/reliability-page'

const automationHealthResponse = {
  jobs: [],
  healthy: true,
  total_jobs: 0,
  failing_jobs: 0,
  degraded_jobs: 0,
}

const runsResponse = {
  data: [],
  total: 0,
  limit: 50,
  offset: 0,
}

const settingsResponse = {
  llm: {
    default_provider: 'openai',
    deep_think_model: 'gpt-5.2',
    quick_think_model: 'gpt-5-mini',
    providers: {
      openai: {
        api_key_configured: true,
        api_key_last4: '1234',
        base_url: 'https://api.openai.com/v1',
        model: 'gpt-5-mini',
      },
      anthropic: {
        api_key_configured: false,
        model: 'claude-3-7-sonnet-latest',
      },
      google: {
        api_key_configured: false,
        model: 'gemini-2.5-flash',
      },
      openrouter: {
        api_key_configured: false,
        model: 'openai/gpt-4.1-mini',
      },
      xai: {
        api_key_configured: false,
        model: 'grok-3-mini',
      },
      ollama: {
        base_url: 'http://localhost:11434',
        model: 'llama3.2',
      },
    },
  },
  risk: {
    max_position_size_pct: 10,
    max_daily_loss_pct: 2,
    max_drawdown_pct: 8,
    max_open_positions: 6,
    max_total_exposure_pct: 80,
    max_per_market_exposure_pct: 40,
    circuit_breaker_threshold_pct: 5,
    circuit_breaker_cooldown_min: 15,
  },
  system: {
    environment: 'development',
    version: 'v1.2.3',
    current_schema_version: 27,
    required_schema_version: 28,
    schema_status: 'behind',
    uptime_seconds: 5400,
    connected_brokers: [],
  },
}

const hoursAgo = (hours: number) => new Date(Date.now() - hours * 60 * 60 * 1000).toISOString()

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false },
    },
  })

  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

describe('ReliabilityPage', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('renders schema status from settings metadata', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString()

      if (url.includes('/api/v1/automation/health')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => automationHealthResponse,
        })
      }

      if (url.includes('/api/v1/settings')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => settingsResponse,
        })
      }

      if (url.includes('/api/v1/runs')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => runsResponse,
        })
      }

      return Promise.reject(new Error(`Unhandled request: ${url}`))
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<ReliabilityPage />, { wrapper: Wrapper })

    const card = await screen.findByTestId('schema-status-card')
    expect(await screen.findByText('27')).toBeInTheDocument()

    expect(within(card).getByText('Schema Status')).toBeInTheDocument()
    expect(within(card).getByText('behind')).toBeInTheDocument()
    expect(within(card).getByText('Current schema')).toBeInTheDocument()
    expect(within(card).getByText('Required schema')).toBeInTheDocument()
    expect(within(card).getByText('27')).toBeInTheDocument()
    expect(within(card).getByText('28')).toBeInTheDocument()
  })

  it('renders provider summaries, reconciles active runs, and surfaces failures', async () => {
    const richSettingsResponse = {
      ...settingsResponse,
      system: {
        ...settingsResponse.system,
        connected_brokers: [{ name: 'broker-a', paper_mode: true, configured: true }],
      },
    }

    const automationHealthRichResponse = {
      jobs: [
        {
          name: 'nightly-backfill',
          enabled: true,
          running: false,
          last_run: hoursAgo(2),
          last_error: undefined,
          error_count: 0,
          consecutive_failures: 0,
          run_count: 18,
        },
        {
          name: 'risk-refresh',
          enabled: true,
          running: false,
          last_run: hoursAgo(5),
          last_error: 'timeout while fetching risk inputs',
          error_count: 2,
          consecutive_failures: 0,
          run_count: 11,
        },
      ],
      healthy: false,
      total_jobs: 2,
      failing_jobs: 0,
      degraded_jobs: 1,
    }

    const recentRunsResponse = {
      data: [
        {
          id: 'run-completed',
          strategy_id: 'strategy-1',
          ticker: 'AAPL',
          trade_date: '2026-06-10',
          status: 'completed',
          started_at: hoursAgo(4),
          completed_at: hoursAgo(3.5),
        },
        {
          id: 'run-failed',
          strategy_id: 'strategy-2',
          ticker: 'MSFT',
          trade_date: '2026-06-10',
          status: 'failed',
          started_at: hoursAgo(2.5),
          completed_at: hoursAgo(2.25),
          error_message: 'Feed lag exceeded threshold',
        },
        {
          id: 'run-running-recent',
          strategy_id: 'strategy-3',
          ticker: 'NVDA',
          trade_date: '2026-06-10',
          status: 'running',
          started_at: hoursAgo(0.5),
        },
      ],
      total: 3,
      limit: 50,
      offset: 0,
    }

    const runningRunsResponse = {
      data: [
        {
          id: 'run-running-recent',
          strategy_id: 'strategy-3',
          ticker: 'NVDA',
          trade_date: '2026-06-10',
          status: 'running',
          started_at: hoursAgo(0.5),
        },
        {
          id: 'run-running-stale',
          strategy_id: 'strategy-4',
          ticker: 'TSLA',
          trade_date: '2026-06-10',
          status: 'running',
          started_at: hoursAgo(2.5),
        },
      ],
      total: 2,
      limit: 50,
      offset: 0,
    }

    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString()

      if (url.includes('/api/v1/automation/health')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => automationHealthRichResponse,
        })
      }

      if (url.includes('/api/v1/settings')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => richSettingsResponse,
        })
      }

      if (url.includes('/api/v1/runs') && url.includes('status=running')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => runningRunsResponse,
        })
      }

      if (url.includes('/api/v1/runs')) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: async () => recentRunsResponse,
        })
      }

      return Promise.reject(new Error(`Unhandled request: ${url}`))
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<ReliabilityPage />, { wrapper: Wrapper })

    await screen.findByText(/openai: API key configured/)
    const providerCard = screen.getByTestId('provider-config-card')
    expect(within(providerCard).getByText('Default')).toBeInTheDocument()
    expect(within(providerCard).getByText(/openai: API key configured/)).toBeInTheDocument()
    expect(within(providerCard).getByText(/API key configured/)).toBeInTheDocument()
    expect(within(providerCard).getByText(/\*\*\*\*1234/)).toBeInTheDocument()

    const signalHubCard = screen.getByTestId('signal-hub-card')
    expect(within(signalHubCard).getAllByText('Heuristic: likely active').length).toBeGreaterThan(0)
    expect(within(signalHubCard).getByText('Summary')).toBeInTheDocument()
    expect(within(signalHubCard).getByText('Current status')).toBeInTheDocument()

    const activeRunsCard = screen.getByTestId('active-run-reconciliation-card')
    expect(within(activeRunsCard).getByText('Needs review')).toBeInTheDocument()
    expect(within(activeRunsCard).getByText('Data source')).toBeInTheDocument()
    expect(within(activeRunsCard).getByText('2 live')).toBeInTheDocument()
    expect(within(activeRunsCard).getByText('1 in history')).toBeInTheDocument()
    expect(within(activeRunsCard).getByText(/Reconciliation gap: 1 live-only, 0 history-only\./)).toBeInTheDocument()
    expect(within(activeRunsCard).getByText(/stale active run/)).toBeInTheDocument()
    expect(within(activeRunsCard).getAllByText(/Oldest active run:/)).toHaveLength(1)

    const recentFailuresCard = screen.getByTestId('recent-failures-card')
    expect(within(recentFailuresCard).getByText('Recent Failures')).toBeInTheDocument()
    expect(within(recentFailuresCard).getByText('Feed lag exceeded threshold')).toBeInTheDocument()

    const successfulJobsCard = screen.getByTestId('last-successful-jobs-card')
    expect(within(successfulJobsCard).getByText('nightly-backfill')).toBeInTheDocument()
    expect(within(successfulJobsCard).getAllByText('healthy').length).toBeGreaterThan(0)
  })
})

import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { Search, TrendingUp } from 'lucide-react'

import { PageHeader } from '@/components/layout/page-header'
import { ChainTable } from '@/components/options/chain-table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ConsolePanel, HudBadge, HudSection, StatusLed } from '@/components/ui/hud'
import { Input } from '@/components/ui/input'
import { ApiClientError, apiClient } from '@/lib/api/client'
import type { OptionSnapshot, ResearchOpportunity, TradeDecisionStatus, TradeDecisionRiskStatus } from '@/lib/api/types'

type OptionTypeFilter = '' | 'call' | 'put'

const quickPickTickers = ['AAPL', 'MSFT', 'NVDA', 'TSLA']

const decisionStatusVariants: Record<TradeDecisionStatus, 'secondary' | 'outline' | 'success' | 'warning' | 'destructive'> = {
  candidate: 'outline',
  rejected: 'destructive',
  paper_ordered: 'warning',
  live_ordered: 'success',
  closed: 'secondary',
}

const riskStatusVariants: Record<TradeDecisionRiskStatus, 'success' | 'destructive'> = {
  approved: 'success',
  rejected: 'destructive',
}

function safeDateLabel(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

function safeMoneyLabel(value: unknown) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : Number.NaN
  if (!Number.isFinite(parsed)) return '—'
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(parsed)
}

function safeNumberLabel(value: unknown, maximumFractionDigits = 0) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : Number.NaN
  if (!Number.isFinite(parsed)) return '—'
  return new Intl.NumberFormat('en-US', {
    maximumFractionDigits,
  }).format(parsed)
}

function splitReasons(reasons?: string[] | null) {
  if (!Array.isArray(reasons) || reasons.length === 0) return []
  return reasons.map((reason) => reason.trim()).filter(Boolean)
}

function getUniqueExpiries(chainContracts: OptionSnapshot[]) {
  return Array.from(new Set(chainContracts.map((snapshot) => snapshot.contract.expiry).filter(Boolean))).sort()
}

function getApiStatus(error: unknown) {
  return error instanceof ApiClientError ? error.status : (error as { status?: number } | null | undefined)?.status
}

function isNotConfiguredError(error: unknown) {
  return getApiStatus(error) === 501
}

function optionScannerEmptyMessage(hasChainData: boolean, hasFilters: boolean) {
  if (!hasChainData) return 'No opportunities because no contracts were returned for this ticker.'
  if (hasFilters) return 'No opportunities matched the selected expiry/type filters or scanner defaults.'
  return 'No opportunities met the scanner defaults for this chain.'
}

function OpportunityRow({ opportunity }: { opportunity: ResearchOpportunity }) {
  const decision = opportunity.decision
  const reasons = splitReasons(opportunity.reasons ?? decision.risk_reasons)

  return (
    <tr className="border-b border-border last:border-0">
      <td className="px-3 py-3 align-top">
        <div className="space-y-1">
          <div className="font-mono text-xs text-muted-foreground break-all">{decision.instrument_key || '—'}</div>
          <div className="text-xs text-muted-foreground">Updated {safeDateLabel(decision.updated_at)}</div>
        </div>
      </td>
      <td className="px-3 py-3 align-top">
        <div className={decision.net_ev > 0 ? 'font-semibold text-emerald-500' : decision.net_ev < 0 ? 'font-semibold text-destructive' : 'font-semibold text-muted-foreground'}>
          {safeMoneyLabel(decision.net_ev)}
        </div>
      </td>
      <td className="px-3 py-3 align-top text-sm text-foreground">{safeNumberLabel(decision.approved_size, 0)}</td>
      <td className="px-3 py-3 align-top">
        <div className="flex flex-wrap gap-1.5">
          <Badge variant={riskStatusVariants[decision.risk_status] ?? 'secondary'}>{decision.risk_status}</Badge>
          <Badge variant={decisionStatusVariants[decision.status]}>{decision.status}</Badge>
          {opportunity.accepted == null ? null : <Badge variant={opportunity.accepted ? 'success' : 'destructive'}>{opportunity.accepted ? 'accepted' : 'rejected'}</Badge>}
        </div>
      </td>
      <td className="px-3 py-3 align-top text-sm text-muted-foreground">
        {reasons.length > 0 ? reasons.join(' · ') : 'No risk reasons recorded'}
      </td>
      <td className="px-3 py-3 align-top text-sm">
        <Link to="/journal" className="text-primary underline-offset-4 hover:underline">
          Journal
        </Link>
      </td>
    </tr>
  )
}

function OpportunityCard({ opportunity }: { opportunity: ResearchOpportunity }) {
  const decision = opportunity.decision
  const reasons = splitReasons(opportunity.reasons ?? decision.risk_reasons)

  return (
    <article className="border border-border bg-panel p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1">
          <div className="font-mono text-xs text-muted-foreground break-all">{decision.instrument_key || '—'}</div>
          <div className="flex flex-wrap gap-1.5">
            <Badge variant={riskStatusVariants[decision.risk_status] ?? 'secondary'}>{decision.risk_status}</Badge>
            <Badge variant={decisionStatusVariants[decision.status]}>{decision.status}</Badge>
          </div>
        </div>
        <div className={decision.net_ev > 0 ? 'text-right font-semibold text-emerald-500' : decision.net_ev < 0 ? 'text-right font-semibold text-destructive' : 'text-right font-semibold text-muted-foreground'}>
          {safeMoneyLabel(decision.net_ev)}
          <div className="mt-1 text-xs font-normal text-muted-foreground">Net EV</div>
        </div>
      </div>

      <div className="mt-4 grid gap-3 text-sm sm:grid-cols-2">
        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Approved size</div>
          <div className="mt-1 font-medium">{safeNumberLabel(decision.approved_size, 0)}</div>
        </div>
        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Risk status</div>
          <div className="mt-1 font-medium">{decision.risk_status}</div>
        </div>
        <div className="sm:col-span-2">
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted-foreground">Risk reasons</div>
          <div className="mt-1 text-muted-foreground">{reasons.length > 0 ? reasons.join(' · ') : 'No risk reasons recorded'}</div>
        </div>
      </div>

      <div className="mt-4 flex items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>Updated {safeDateLabel(decision.updated_at)}</span>
        <Link to="/journal" className="text-primary underline-offset-4 hover:underline">
          Journal
        </Link>
      </div>
    </article>
  )
}

export function OptionsPage() {
  const [searchParams] = useSearchParams()
  const [draftTicker, setDraftTicker] = useState('')
  const [draftExpiry, setDraftExpiry] = useState('')
  const [draftType, setDraftType] = useState<OptionTypeFilter>('')

  const [ticker, setTicker] = useState('')
  const [expiry, setExpiry] = useState('')
  const [optionType, setOptionType] = useState<OptionTypeFilter>('')

  const urlTicker = searchParams.get('ticker')?.trim().toUpperCase() ?? ''

  const { data, error: chainError, isLoading, isError, isFetched, refetch } = useQuery({
    queryKey: ['options-chain', ticker, expiry, optionType],
    queryFn: () =>
      apiClient.getOptionsChain(ticker, {
        expiry: expiry || undefined,
        type: optionType || undefined,
      }),
    enabled: Boolean(ticker),
  })

  const opportunitiesQuery = useQuery({
    queryKey: ['options-opportunities', ticker, expiry, optionType],
    queryFn: () =>
      apiClient.listOptionsOpportunities(ticker, {
        limit: 12,
        expiry: expiry || undefined,
        type: optionType || undefined,
      }),
    enabled: Boolean(ticker),
  })

  const chainContracts = data ?? []
  const chainExpiries = getUniqueExpiries(chainContracts)
  const hasLoadedExpiries = chainExpiries.length > 0
  const expiryOptions = draftExpiry && !chainExpiries.includes(draftExpiry) ? [...chainExpiries, draftExpiry] : chainExpiries

  function commitAndLoad(
    tickerValue: string,
    expiryValue = draftExpiry,
    typeValue: OptionTypeFilter = draftType,
  ) {
    const normalized = tickerValue.trim().toUpperCase()
    if (!normalized) return
    setDraftTicker(normalized)
    setTicker(normalized)
    setDraftExpiry(expiryValue)
    setExpiry(expiryValue)
    setDraftType(typeValue)
    setOptionType(typeValue)
  }

  function loadChain() {
    commitAndLoad(draftTicker, draftExpiry, draftType)
  }

  useEffect(() => {
    if (!urlTicker) return
    setDraftTicker(urlTicker)
    setTicker(urlTicker)
    setDraftExpiry('')
    setExpiry('')
    setDraftType('')
    setOptionType('')
  }, [urlTicker])

  function setSelectedExpiry(nextExpiry: string) {
    setDraftExpiry(nextExpiry)
    setExpiry(nextExpiry)
  }

  const opportunities = opportunitiesQuery.data?.data ?? []
  const hasScannerFilters = Boolean(expiry || optionType)
  const chainProviderUnavailable = isNotConfiguredError(chainError)
  const opportunitiesStatus = !ticker
    ? 'No ticker selected yet. Search a ticker above to enable the paper-first scanner.'
    : chainProviderUnavailable
      ? 'Options provider not configured on this deployment.'
    : opportunitiesQuery.isLoading
      ? 'Loading opportunities…'
      : opportunitiesQuery.isError
        ? isNotConfiguredError(opportunitiesQuery.error)
          ? 'Scanner not configured'
          : 'Unable to load opportunities'
        : opportunities.length === 0
          ? optionScannerEmptyMessage(chainContracts.length > 0, hasScannerFilters)
          : `${opportunities.length} opportunities`

  const opportunityScannerUnavailable = isNotConfiguredError(opportunitiesQuery.error)
  const selectedExpiryIsLoaded = Boolean(draftExpiry) && chainExpiries.includes(draftExpiry)

  return (
    <div className="space-y-5" data-testid="options-page">
      <PageHeader title="Options chain" description="Look up option chains by underlying ticker." />

      <ConsolePanel className="space-y-4 p-4">
        <HudSection label="Lookup" note="Enter a ticker and optional filters, then load the chain" />
        <div className="flex flex-wrap items-center justify-between gap-3">
          <HudBadge tone={ticker ? 'confirm' : 'caution'}>{ticker ? `${ticker} loaded` : 'scanner idle'}</HudBadge>
          <StatusLed state={ticker ? 'ok' : 'sync'} label={ticker ? 'Chain active' : 'Waiting for ticker'} />
        </div>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            loadChain()
          }}
        >
          <div className="grid gap-3 lg:grid-cols-[200px_170px_200px_160px_auto]">
            <Input
              placeholder="Ticker (e.g. AAPL)"
              value={draftTicker}
              onChange={(e) => setDraftTicker(e.target.value)}
              aria-label="Underlying ticker"
            />
            <Input
              type="date"
              value={draftExpiry}
              onChange={(e) => setDraftExpiry(e.target.value)}
              aria-label="Expiry date filter"
            />
            <div className="space-y-1">
              <div className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Loaded expiries</div>
              <select
                value={draftExpiry}
                onChange={(e) => setSelectedExpiry(e.target.value)}
                aria-label="Loaded expiries"
                disabled={!hasLoadedExpiries && !draftExpiry}
                className="flex h-9 w-full rounded-none border border-input bg-panel px-3 py-1 text-sm text-foreground transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-pulse disabled:opacity-60"
              >
                <option value="">All expiries</option>
                {!selectedExpiryIsLoaded && draftExpiry ? (
                  <option value={draftExpiry}>Selected: {draftExpiry}</option>
                ) : null}
                {expiryOptions.map((expiryOption) => (
                  <option key={expiryOption} value={expiryOption}>
                    {expiryOption}
                  </option>
                ))}
              </select>
            </div>
            <select
              value={draftType}
              onChange={(e) => setDraftType(e.target.value as OptionTypeFilter)}
              aria-label="Option type"
              className="flex h-9 w-full rounded-none border border-input bg-panel px-3 py-1 text-sm text-foreground transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-pulse"
            >
              <option value="">All types</option>
              <option value="call">Calls</option>
              <option value="put">Puts</option>
            </select>
            <Button type="submit" disabled={!draftTicker.trim()}>
              <Search className="size-4" />
              Load Chain
            </Button>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Quick picks</span>
            {quickPickTickers.map((symbol) => (
              <Button key={symbol} type="button" variant="outline" size="sm" onClick={() => setDraftTicker(symbol)}>
                {symbol}
              </Button>
            ))}
          </div>
        </form>
      </ConsolePanel>

      <ConsolePanel className="space-y-4 p-4">
        <HudSection label="Research opportunities" note={ticker ? `${ticker} scanner` : 'Search a ticker above to enable the paper-first scanner.'} />
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <span>{opportunitiesStatus}</span>
          {ticker ? <span>· Underlying {ticker}</span> : null}
          {expiry ? <span>· Expiry {expiry}</span> : null}
          {optionType ? <span>· {optionType.toUpperCase()}</span> : null}
        </div>

        {!ticker ? (
          <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="options-opportunities-no-ticker">
            <TrendingUp className="size-8 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">No ticker selected yet. Search a ticker above to enable the scanner.</p>
          </div>
        ) : chainProviderUnavailable ? (
          <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="options-opportunities-unavailable">
            <TrendingUp className="size-8 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">Options provider not configured on this deployment.</p>
          </div>
        ) : opportunitiesQuery.isLoading ? (
          <div className="space-y-3" data-testid="options-opportunities-loading">
            {Array.from({ length: 3 }).map((_, index) => (
              <div key={index} className="h-20 animate-pulse rounded-none border border-border bg-muted/40" />
            ))}
          </div>
        ) : opportunitiesQuery.isError ? (
          opportunityScannerUnavailable ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center">
              <TrendingUp className="size-8 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">Scanner not configured on this deployment.</p>
            </div>
          ) : (
            <div className="space-y-3 text-sm" data-testid="options-opportunities-error">
              <p className="text-muted-foreground">Unable to load opportunities.</p>
              <Button type="button" variant="outline" size="sm" onClick={() => void opportunitiesQuery.refetch()}>
                Retry
              </Button>
            </div>
          )
        ) : opportunities.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="options-opportunities-empty">
            <TrendingUp className="size-8 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">{opportunitiesStatus}</p>
          </div>
        ) : (
          <>
            <div className="hidden overflow-hidden border border-border md:block">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border bg-muted/40 font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
                    <th className="px-3 py-2 font-medium">Instrument</th>
                    <th className="px-3 py-2 font-medium">Net EV</th>
                    <th className="px-3 py-2 font-medium">Approved size</th>
                    <th className="px-3 py-2 font-medium">Risk / status</th>
                    <th className="px-3 py-2 font-medium">Risk reasons</th>
                    <th className="px-3 py-2 font-medium">Journal</th>
                  </tr>
                </thead>
                <tbody>
                  {opportunities.map((opportunity) => (
                    <OpportunityRow key={opportunity.decision.id} opportunity={opportunity} />
                  ))}
                </tbody>
              </table>
            </div>

            <div className="space-y-3 md:hidden">
              {opportunities.map((opportunity) => (
                <OpportunityCard key={opportunity.decision.id} opportunity={opportunity} />
              ))}
            </div>
          </>
        )}
      </ConsolePanel>

      <Card>
        <CardHeader>
          <CardTitle>
            {ticker ? `${ticker} options` : 'Options data'}
          </CardTitle>
          <CardDescription>
            {isLoading
              ? 'Loading chain...'
              : isError
                ? chainProviderUnavailable
                  ? 'Options provider not configured on this deployment.'
                  : 'Failed to load options chain'
                : !isFetched
                  ? 'Enter a ticker above to get started'
                  : `${chainContracts.length} contracts loaded`}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3" data-testid="options-loading">
              {Array.from({ length: 6 }).map((_, index) => (
                <div key={index} className="flex items-center gap-3 rounded-none border border-border p-3">
                  <div className="h-4 w-16 animate-pulse rounded-none bg-muted/40" />
                  <div className="h-4 w-20 animate-pulse rounded-none bg-muted/40" />
                  <div className="h-4 w-20 animate-pulse rounded-none bg-muted/40" />
                  <div className="ml-auto h-4 w-14 animate-pulse rounded-none bg-muted/40" />
                </div>
              ))}
            </div>
          ) : isError ? (
            <div className="space-y-3" data-testid="options-error">
              <p className="text-sm text-muted-foreground">
                {chainProviderUnavailable
                  ? 'Options provider not configured on this deployment.'
                  : 'Unable to load options chain. Ensure the API server is running.'}
              </p>
              {chainProviderUnavailable ? null : (
                <Button type="button" variant="outline" size="sm" onClick={() => void refetch()}>
                  Retry
                </Button>
              )}
            </div>
          ) : isFetched && chainContracts.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="options-empty-contracts">
              <TrendingUp className="size-8 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">No contracts returned for this ticker.</p>
              <p className="text-xs text-muted-foreground">
                The provider returned an empty chain, so the scanner has nothing to evaluate yet.
              </p>
            </div>
          ) : !isFetched ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="options-empty">
              <TrendingUp className="size-8 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">
                No ticker selected yet. Search for a ticker to view its options chain.
              </p>
            </div>
          ) : (
            <ChainTable data={data ?? []} />
          )}
        </CardContent>
      </Card>
    </div>
  )
}

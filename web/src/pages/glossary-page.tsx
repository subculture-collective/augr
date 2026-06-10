import { BookOpen, ChevronLeft, Link2, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

// ---- term data ----

export interface GlossaryTerm {
  term: string
  slug: string
  category: string
  definition: string
  detail: string
  formula?: {
    label: string
    expression: string
    note: string
  }
  example?: {
    label: string
    body: string
  }
  related: string[]
}

// Exported for glossary coverage tests.
// eslint-disable-next-line react-refresh/only-export-components
export const GLOSSARY_TERMS: GlossaryTerm[] = [
  {
    term: 'AMC',
    slug: 'amc',
    category: 'earnings',
    definition:
      'After Market Close — an earnings report released after the trading day ends. Prices may gap at the next open.',
    detail:
      'Used when a company reports after the bell. The market digests the numbers overnight, so the next session can open with a sharp repricing if the report surprises traders.',
    example: {
      label: 'Example',
      body: 'A 4:05 PM AMC earnings release can turn into a gap up or gap down at the next open as the market reacts to the report.',
    },
    related: ['bmo', 'gap', 'eps', 'sec-filing'],
  },
  {
    term: 'Automation',
    slug: 'automation',
    category: 'system',
    definition:
      'Scheduled or event-driven workflows that run strategies, send alerts, or perform maintenance tasks without manual intervention.',
    detail:
      'Automation keeps repetitive work off the operator’s desk. In Augr it can launch analyses, fan out alerts, and keep the pipeline moving without someone clicking every step.',
    example: {
      label: 'Example',
      body: 'A nightly automation can refresh the universe, rerun discovery, and notify the team if a new watchlist name crosses the threshold.',
    },
    related: ['discovery', 'pipeline-run', 'realtime', 'memory'],
  },
  {
    term: 'Backtest',
    slug: 'backtest',
    category: 'strategy',
    definition:
      'A simulated test of a strategy against historical price data to evaluate how it would have performed in the past.',
    detail:
      'Backtests answer the question: if this rule set had existed before, would it have worked? They are most useful when paired with paper trading so historical luck does not get mistaken for live durability.',
    formula: {
      label: 'Expectancy',
      expression: 'Expectancy = (win rate × average win) - (loss rate × average loss)',
      note: 'A positive expectancy suggests the strategy has edge before fees and slippage.',
    },
    example: {
      label: 'Example',
      body: 'Run the same signal logic across two years of price history, then compare average gain, drawdown, and hit rate before going live.',
    },
    related: ['paper-trading', 'strategy', 'signal', 'pnl'],
  },
  {
    term: 'BMO',
    slug: 'bmo',
    category: 'earnings',
    definition:
      'Before Market Open — an earnings report released before the trading day starts. Prices may gap at the open.',
    detail:
      'BMO releases give the market time to react before the opening bell. The opening print often encodes the first consensus on the report, which is why the gap matters as much as the headline numbers.',
    example: {
      label: 'Example',
      body: 'A 6:30 AM BMO report can reshape the opening auction, especially when EPS or revenue beats by a wide margin.',
    },
    related: ['amc', 'gap', 'eps', 'sec-filing'],
  },
  {
    term: 'Circuit Breaker',
    slug: 'circuit-breaker',
    category: 'risk',
    definition:
      'An automated control that pauses trading when specific risk thresholds are breached, preventing further losses until manually reviewed.',
    detail:
      'A circuit breaker is the system’s hard stop. It is less about optimizing returns and more about stopping a bad day from becoming a catastrophic one.',
    formula: {
      label: 'Rule of thumb',
      expression: 'If drawdown ≥ threshold, pause trading and require manual review.',
      note: 'The trigger can be tied to loss, latency, error rate, or another safety threshold.',
    },
    related: ['kill-switch', 'reliability', 'stop-loss', 'take-profit'],
  },
  {
    term: 'Discovery',
    slug: 'discovery',
    category: 'system',
    definition:
      'An AI-powered scan that analyzes the universe of tracked securities and surfaces the most promising opportunities based on configured criteria.',
    detail:
      'Discovery is the first filter in the loop: scan the universe, score what stands out, and surface the names worth deeper review. The output usually feeds watchlists, signals, and alerts.',
    example: {
      label: 'Example',
      body: 'Discovery scans the full universe, boosts names with strong catalysts, and sends the highest-scoring tickers into the watchlist queue.',
    },
    related: ['universe', 'watch-score', 'watchlist', 'signal'],
  },
  {
    term: 'EPS',
    slug: 'eps',
    category: 'earnings',
    definition:
      "Earnings Per Share — a company's net profit divided by the number of outstanding shares. A key metric for comparing earnings reports against analyst estimates.",
    detail:
      'EPS compresses company profit into a per-share number that traders can compare across quarters. A beat or miss versus estimates is often the first thing the market reacts to in an earnings release.',
    formula: {
      label: 'Formula',
      expression: 'EPS = (net income - preferred dividends) / weighted average diluted shares',
      note: 'If actual EPS is above estimate, the report is usually described as a beat.',
    },
    example: {
      label: 'Example',
      body: 'If net income is $120M and diluted shares are 60M, EPS is $2.00. A consensus estimate of $1.70 would make that a beat.',
    },
    related: ['amc', 'bmo', 'sec-filing', 'sentiment', 'signal'],
  },
  {
    term: 'Fill Price',
    slug: 'fill-price',
    category: 'orders',
    definition:
      'The actual price at which an order was executed, which may differ from the limit or market price requested.',
    detail:
      'Fill price is the real execution price, not the ideal one. It is where slippage becomes visible and where execution quality turns into measurable P&L.',
    formula: {
      label: 'Slippage',
      expression: 'Slippage = fill price - expected price',
      note: 'Positive or negative slippage depends on the direction of the trade.',
    },
    related: ['pnl', 'position', 'long', 'short'],
  },
  {
    term: 'Gap',
    slug: 'gap',
    category: 'price',
    definition:
      "A price difference between a stock's prior close and the next open, caused by overnight news or earnings releases.",
    detail:
      'Gaps are the market’s overnight verdict. They often reflect earnings, macro headlines, or news that arrives after the previous session has closed.',
    formula: {
      label: 'Gap %',
      expression: 'Gap % = (open - prior close) / prior close × 100',
      note: 'A positive result is a gap up; a negative result is a gap down.',
    },
    example: {
      label: 'Example',
      body: 'If a stock closes at $50 and opens at $55, the gap is +10.0%.',
    },
    related: ['amc', 'bmo', 'eps', 'sec-filing'],
  },
  {
    term: 'Kill Switch',
    slug: 'kill-switch',
    category: 'risk',
    definition:
      'An emergency control that immediately halts all trading activity system-wide. Requires manual reactivation to resume.',
    detail:
      'The kill switch is the final safety layer. It does not care why things went wrong; it just stops the engine before the system can compound the mistake.',
    related: ['circuit-breaker', 'reliability', 'automation'],
  },
  {
    term: 'Long',
    slug: 'long',
    category: 'positions',
    definition:
      'A position that profits when the underlying security rises in price. The standard direction for most equity trades.',
    detail:
      'Going long means buying first and hoping price rises later. It is the default direction for many equity strategies and the reference point for stop loss and take profit math.',
    formula: {
      label: 'Long P&L',
      expression: 'P&L = (exit price - entry price) × shares - fees',
      note: 'A higher exit price increases profit for a long position.',
    },
    related: ['position', 'pnl', 'stop-loss', 'take-profit', 'short'],
  },
  {
    term: 'Memory',
    slug: 'memory',
    category: 'system',
    definition:
      'Stored knowledge — facts, summaries, and decisions — that agents reference across sessions to inform future analysis.',
    detail:
      'Memory is the connective tissue between sessions. It keeps prior judgments, notes, and context available so the system does not start from zero every time.',
    example: {
      label: 'Example',
      body: 'A prior note about a name’s earnings season behavior can be reused the next time discovery surfaces the same ticker.',
    },
    related: ['realtime', 'pipeline-run', 'automation', 'discovery'],
  },
  {
    term: 'Options Chain',
    slug: 'options-chain',
    category: 'options',
    definition:
      'A listing of all available option contracts (calls and puts) for a given security, organized by expiry date and strike price.',
    detail:
      'The chain is how traders inspect strikes, expiries, and premiums in one place. It is the map that shows where leverage, hedging, and volatility are priced.',
    example: {
      label: 'Example',
      body: 'A chain for AAPL might show weekly calls and puts across multiple strikes and expiries, making it easy to compare liquidity and premium.',
    },
    related: ['ticker', 'long', 'short', 'stop-loss'],
  },
  {
    term: 'P&L',
    slug: 'pnl',
    category: 'positions',
    definition:
      'Profit and Loss — the difference between realized or unrealized revenue and the cost of entering a position. Displayed as both a dollar amount and percentage.',
    detail:
      'P&L is the scorecard for a position or portfolio. It can be realized when a trade closes or unrealized while the position is still open.',
    formula: {
      label: 'Formula',
      expression: 'P&L = realized gains + unrealized gains - fees',
      note: 'For a long position, rising prices improve P&L; for a short position, falling prices do.',
    },
    example: {
      label: 'Example',
      body: 'If you buy 100 shares at $10 and sell at $12, the gross P&L is $200 before fees.',
    },
    related: ['fill-price', 'long', 'position', 'short', 'stop-loss'],
  },
  {
    term: 'Paper Trading',
    slug: 'paper-trading',
    category: 'strategy',
    definition:
      'Simulated trading with no real money at risk. Used to validate strategy behavior before live deployment.',
    detail:
      'Paper trading sits between backtests and live trading. It keeps the strategy in a real-time environment while removing capital risk, which makes it ideal for proving execution logic.',
    related: ['backtest', 'strategy', 'signal', 'automation'],
  },
  {
    term: 'Pipeline Run',
    slug: 'pipeline-run',
    category: 'system',
    definition:
      "A single execution of a strategy's analysis pipeline, encompassing all agent phases from data ingestion through final signal generation.",
    detail:
      'A pipeline run is the atomic unit of work for the system. It turns inputs into outputs through a chain of stages, and it is often what realtime streams back to the operator.',
    example: {
      label: 'Example',
      body: 'One run might ingest SEC filings, score the universe, and emit a final signal with a confidence value and audit trail.',
    },
    related: ['realtime', 'automation', 'discovery', 'signal', 'memory'],
  },
  {
    term: 'Position',
    slug: 'position',
    category: 'positions',
    definition:
      'An open trade holding shares or contracts. Shows current price, unrealized P&L, and stop/take-profit levels.',
    detail:
      'A position is the live state of a trade. It becomes the place where entry price, current price, and risk controls meet the market.',
    related: ['long', 'short', 'pnl', 'stop-loss', 'take-profit'],
  },
  {
    term: 'Realtime',
    slug: 'realtime',
    category: 'system',
    definition:
      'A live event feed that streams pipeline runs and agent activity as they happen via WebSocket, with conversation context for each event.',
    detail:
      'Realtime is the live nerve system of the app. It keeps the operator informed while work is happening, not after the fact.',
    example: {
      label: 'Example',
      body: 'When a pipeline run starts, realtime can surface the event, then append updates as analysis finishes and signals are emitted.',
    },
    related: ['pipeline-run', 'automation', 'memory', 'reliability'],
  },
  {
    term: 'Reliability',
    slug: 'reliability',
    category: 'system',
    definition:
      'System health metrics tracking uptime, latency percentiles, and error rates across pipeline components.',
    detail:
      'Reliability describes whether the system can be trusted to keep working under load. The most useful metrics are the ones that show when failure is creeping in before users feel it.',
    formula: {
      label: 'Uptime view',
      expression: 'Reliability ≈ 1 - error rate',
      note: 'In practice, latency, retries, and timeouts also matter.',
    },
    related: ['realtime', 'kill-switch', 'circuit-breaker', 'automation'],
  },
  {
    term: 'SEC Filing',
    slug: 'sec-filing',
    category: 'fundamentals',
    definition:
      'Documents required by the Securities and Exchange Commission, including 10-K annual reports, 10-Q quarterly reports, and 8-K material event disclosures. Can be analyzed by the AI pipeline for trading signals.',
    detail:
      'SEC filings are the canonical source for corporate disclosures. They are often the raw material for earnings analysis, sentiment extraction, and pipeline scoring.',
    example: {
      label: 'Example',
      body: 'A 10-Q can be analyzed for guidance changes, while an 8-K may reveal a material event that changes the trade setup.',
    },
    related: ['eps', 'sentiment', 'amc', 'bmo', 'signal'],
  },
  {
    term: 'Sentiment',
    slug: 'sentiment',
    category: 'signals',
    definition:
      'An analyst assessment of whether a signal or filing is bullish (positive), bearish (negative), or neutral. Used to classify pipeline outputs.',
    detail:
      'Sentiment turns qualitative language into a directional label. It is a compact way to summarize whether the tone behind a filing, article, or event leans positive or negative.',
    example: {
      label: 'Example',
      body: 'A filing that raises guidance may be marked bullish, while a downgrade-heavy report may be marked bearish.',
    },
    related: ['signal', 'sec-filing', 'watch-score', 'realtime'],
  },
  {
    term: 'Short',
    slug: 'short',
    category: 'positions',
    definition:
      'A position that profits when the underlying security falls in price. Involves borrowing shares to sell, then buying them back at a lower price.',
    detail:
      'Short positions flip the long logic upside down. Entry happens by selling borrowed shares first, and profit comes from buying them back cheaper later.',
    formula: {
      label: 'Short P&L',
      expression: 'P&L = (entry price - exit price) × shares - fees',
      note: 'A lower exit price increases profit for a short position.',
    },
    related: ['position', 'pnl', 'stop-loss', 'take-profit', 'long'],
  },
  {
    term: 'Signal',
    slug: 'signal',
    category: 'signals',
    definition:
      'A trading recommendation generated at the end of a pipeline run: buy, sell, or hold. Accompanied by confidence score and agent reasoning.',
    detail:
      'A signal is the system’s final answer after the data, scoring, and reasoning steps are done. It should read like a recommendation with context, not a black box verdict.',
    formula: {
      label: 'Conceptual score',
      expression: 'Signal strength = confidence × setup quality × catalyst weight',
      note: 'The exact blend can vary by strategy and source data.',
    },
    related: ['pipeline-run', 'discovery', 'sentiment', 'watch-score', 'strategy'],
  },
  {
    term: 'Stop Loss',
    slug: 'stop-loss',
    category: 'risk',
    definition:
      'A price level at which a losing position is automatically closed to cap downside. Set per-position based on strategy risk parameters.',
    detail:
      'Stop loss turns risk tolerance into a concrete price. It keeps one bad move from becoming a large one by forcing an exit at a predefined point.',
    formula: {
      label: 'Long exit rule',
      expression: 'If price ≤ stop loss, close the position',
      note: 'For shorts, the stop sits above entry instead of below it.',
    },
    example: {
      label: 'Example',
      body: 'If a long entry is $25 and the stop loss is $23.50, the trade exits automatically if price touches $23.50.',
    },
    related: ['take-profit', 'position', 'pnl', 'long', 'short'],
  },
  {
    term: 'Strategy',
    slug: 'strategy',
    category: 'strategy',
    definition:
      'A configured set of agents, tickers, schedule, and risk parameters that define how the system analyzes and trades a security.',
    detail:
      'A strategy bundles the rules that decide what to scan, when to run, how to rank opportunities, and how much risk to allow. It is the container that backtests and live runs refer to.',
    related: ['backtest', 'paper-trading', 'signal', 'discovery', 'watchlist'],
  },
  {
    term: 'Take Profit',
    slug: 'take-profit',
    category: 'risk',
    definition:
      'A price level at which a profitable position is automatically closed to lock in gains. Works alongside stop loss for symmetric risk management.',
    detail:
      'Take profit defines the upside exit before emotion can overstay the trade. It is the mirror image of stop loss and usually lives beside it in the same risk plan.',
    formula: {
      label: 'Long exit rule',
      expression: 'If price ≥ take profit, close the position',
      note: 'For shorts, the target sits below entry instead of above it.',
    },
    related: ['stop-loss', 'position', 'pnl', 'long', 'short'],
  },
  {
    term: 'Ticker',
    slug: 'ticker',
    category: 'price',
    definition:
      'A unique symbol identifying a publicly traded security (e.g., AAPL for Apple Inc.). Used across the system to reference stocks, ETFs, and other instruments.',
    detail:
      'The ticker is the shortest name a market gives a security. It is the join key that links price data, filings, options, positions, and alerts.',
    related: ['universe', 'options-chain', 'sec-filing', 'watchlist'],
  },
  {
    term: 'Universe',
    slug: 'universe',
    category: 'system',
    definition:
      'The full set of securities tracked and scored by the system. Securities are ranked by watch score to surface the most relevant candidates.',
    detail:
      'The universe is the full search space. Discovery and scoring turn that large set into a much smaller list of names worth attention.',
    related: ['discovery', 'watch-score', 'watchlist', 'ticker'],
  },
  {
    term: 'Watch Score',
    slug: 'watch-score',
    category: 'signals',
    definition:
      'A composite ranking score indicating how much attention a ticker deserves. Higher scores reflect stronger technical and fundamental signals.',
    detail:
      'Watch score is the ranking layer that helps Augr decide what deserves a closer look. It merges multiple signals into one sortable number.',
    formula: {
      label: 'Composite score',
      expression: 'Watch score = technical strength + fundamental strength + catalyst weight',
      note: 'The components are typically normalized before scoring so different signals can be compared.',
    },
    example: {
      label: 'Example',
      body: 'A name with strong price action, a positive filing, and an upcoming catalyst may outrank a quieter ticker with no fresh event.',
    },
    related: ['discovery', 'universe', 'signal', 'watchlist', 'sentiment'],
  },
  {
    term: 'Watchlist',
    slug: 'watchlist',
    category: 'system',
    definition:
      'The active subset of the universe currently under close monitoring. Populated by the Discovery scan and scored tickers above configured thresholds.',
    detail:
      'The watchlist is the operator’s short list. It turns the universe into something manageable by keeping only the names that have earned attention.',
    related: ['discovery', 'watch-score', 'universe', 'realtime'],
  },
]

const TERM_BY_SLUG = new Map(GLOSSARY_TERMS.map((term) => [term.slug, term]))

// ---- category labels ----

const CATEGORY_LABELS: Record<string, string> = {
  earnings: 'Earnings',
  fundamentals: 'Fundamentals',
  options: 'Options',
  orders: 'Orders',
  positions: 'Positions',
  price: 'Price',
  risk: 'Risk',
  signals: 'Signals',
  strategy: 'Strategy',
  system: 'System',
}

function relatedTerms(term: GlossaryTerm): GlossaryTerm[] {
  return term.related.map((slug) => TERM_BY_SLUG.get(slug)).filter((value): value is GlossaryTerm => Boolean(value))
}

function termSearchText(term: GlossaryTerm): string {
  return [
    term.term,
    term.definition,
    term.detail,
    term.category,
    term.formula?.label,
    term.formula?.expression,
    term.formula?.note,
    term.example?.label,
    term.example?.body,
    ...term.related.map((slug) => TERM_BY_SLUG.get(slug)?.term ?? slug),
  ]
    .filter(Boolean)
    .join(' ')
    .toLowerCase()
}

function TermChipLink({ slug }: { slug: string }) {
  const term = TERM_BY_SLUG.get(slug)
  if (!term) return null

  return (
    <Button asChild variant="outline" size="dense">
      <Link to={`/glossary/${term.slug}`}>
        <Link2 className="size-3" />
        {term.term}
      </Link>
    </Button>
  )
}

function GlossaryIndex() {
  const [query, setQuery] = useState('')

  const filtered = useMemo(() => {
    const q = query.toLowerCase().trim()
    if (!q) return GLOSSARY_TERMS
    return GLOSSARY_TERMS.filter((term) => termSearchText(term).includes(q))
  }, [query])

  const groups = useMemo(() => {
    const map = new Map<string, GlossaryTerm[]>()
    for (const term of filtered) {
      const letter = term.term[0].toUpperCase()
      if (!map.has(letter)) map.set(letter, [])
      map.get(letter)!.push(term)
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b))
  }, [filtered])

  return (
    <div className="space-y-4" data-testid="glossary-page">
      <PageHeader
        eyebrow="Reference"
        title="Glossary"
        description="Wiki-style definitions for terms used throughout Augr"
      />

      <div className="relative max-w-md">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search terms, formulas, examples…"
          className="pl-8 text-sm"
          data-testid="glossary-search"
        />
      </div>

      {groups.length === 0 ? (
        <p className="text-sm text-muted-foreground" data-testid="glossary-empty">
          No terms match "{query}".
        </p>
      ) : (
        <div className="space-y-8">
          {groups.map(([letter, terms]) => (
            <section key={letter} aria-labelledby={`glossary-letter-${letter}`}>
              <h2
                id={`glossary-letter-${letter}`}
                className="mb-3 font-mono text-[11px] font-medium uppercase tracking-[0.18em] text-muted-foreground"
              >
                {letter}
              </h2>
              <div className="space-y-3">
                {terms.map((term) => (
                  <Card
                    key={term.slug}
                    id={`term-${term.slug}`}
                    className="border-border/70 bg-card/95"
                    data-testid={`glossary-term-${term.slug}`}
                  >
                    <CardHeader className="space-y-3 pb-3">
                      <div className="flex flex-wrap items-start justify-between gap-3">
                        <div className="min-w-0 space-y-1">
                          <CardTitle className="text-base">
                            <Link to={`/glossary/${term.slug}`} className="transition-colors hover:text-primary">
                              {term.term}
                            </Link>
                          </CardTitle>
                          <p className="text-sm leading-relaxed text-muted-foreground">{term.definition}</p>
                        </div>
                        <Badge variant="secondary" className="text-[10px]">
                          {CATEGORY_LABELS[term.category] ?? term.category}
                        </Badge>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        <Button asChild variant="outline" size="dense">
                          <Link to={`/glossary/${term.slug}`}>Open detail</Link>
                        </Button>
                        {term.related.slice(0, 3).map((slug) => (
                          <TermChipLink key={slug} slug={slug} />
                        ))}
                      </div>
                    </CardHeader>
                  </Card>
                ))}
              </div>
            </section>
          ))}
        </div>
      )}

      <p className="flex items-center gap-1.5 pt-2 text-xs text-muted-foreground">
        <BookOpen className="size-3.5" />
        {GLOSSARY_TERMS.length} terms defined
      </p>
    </div>
  )
}

function GlossaryDetail({ term }: { term: GlossaryTerm }) {
  const related = relatedTerms(term)

  return (
    <div id="top" className="space-y-4">
      <PageHeader
        eyebrow="Reference"
        title={term.term}
        description={term.definition}
        meta={<Badge variant="secondary">{CATEGORY_LABELS[term.category] ?? term.category}</Badge>}
        actions={
          <Button asChild variant="outline" size="sm">
            <Link to="/glossary">
              <ChevronLeft className="size-3.5" />
              Back to index
            </Link>
          </Button>
        }
      />

      <div className="flex flex-wrap gap-2 text-xs">
        <a href="#definition" className="rounded-none border border-border bg-panel px-2.5 py-1 text-ink-dim transition-colors hover:border-pulse hover:text-ink">
          Definition
        </a>
        {term.formula ? (
          <a href="#formula" className="rounded-none border border-border bg-panel px-2.5 py-1 text-ink-dim transition-colors hover:border-pulse hover:text-ink">
            Formula
          </a>
        ) : null}
        {term.example ? (
          <a href="#example" className="rounded-none border border-border bg-panel px-2.5 py-1 text-ink-dim transition-colors hover:border-pulse hover:text-ink">
            Example
          </a>
        ) : null}
        <a href="#related" className="rounded-none border border-border bg-panel px-2.5 py-1 text-ink-dim transition-colors hover:border-pulse hover:text-ink">
          Related terms
        </a>
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.25fr)_minmax(280px,0.75fr)]">
        <div className="space-y-4">
          <section id="definition" className="scroll-mt-24">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">Definition</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3 text-sm leading-relaxed text-muted-foreground">
                <p>{term.detail}</p>
              </CardContent>
            </Card>
          </section>

          {term.formula ? (
            <section id="formula" className="scroll-mt-24">
              <Card>
                <CardHeader>
                  <CardTitle className="text-base">{term.formula.label}</CardTitle>
                </CardHeader>
                <CardContent className="space-y-2">
                  <code className="block rounded-md border border-border bg-muted/10 px-3 py-2 font-mono text-sm text-foreground">
                    {term.formula.expression}
                  </code>
                  <p className="text-sm leading-relaxed text-muted-foreground">{term.formula.note}</p>
                </CardContent>
              </Card>
            </section>
          ) : null}

          {term.example ? (
            <section id="example" className="scroll-mt-24">
              <Card>
                <CardHeader>
                  <CardTitle className="text-base">{term.example.label}</CardTitle>
                </CardHeader>
                <CardContent>
                  <p className="text-sm leading-relaxed text-muted-foreground">{term.example.body}</p>
                </CardContent>
              </Card>
            </section>
          ) : null}
        </div>

        <aside className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Wiki links</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-sm text-muted-foreground">
              <p>Jump between related terms without leaving the glossary.</p>
              <div className="flex flex-wrap gap-2">
                {related.length > 0 ? related.map((item) => <TermChipLink key={item.slug} slug={item.slug} />) : <span>No related terms.</span>}
              </div>
            </CardContent>
          </Card>

          <section id="related" className="scroll-mt-24">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">Related terms</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {related.length > 0 ? (
                  related.map((item) => (
                    <div key={item.slug} className="rounded-lg border border-border bg-card/80 p-3">
                      <div className="flex flex-wrap items-start justify-between gap-2">
                        <div className="space-y-1">
                          <Link to={`/glossary/${item.slug}`} className="font-medium transition-colors hover:text-primary">
                            {item.term}
                          </Link>
                          <p className="text-xs uppercase tracking-[0.14em] text-muted-foreground">
                            {CATEGORY_LABELS[item.category] ?? item.category}
                          </p>
                        </div>
                        <Button asChild variant="outline" size="dense">
                          <Link to={`/glossary/${item.slug}`}>Open</Link>
                        </Button>
                      </div>
                    </div>
                  ))
                ) : (
                  <p className="text-sm text-muted-foreground">No related terms to show.</p>
                )}
              </CardContent>
            </Card>
          </section>
        </aside>
      </div>

      <div className="pt-2 text-xs text-muted-foreground">
        <a href="#top" className="transition-colors hover:text-foreground">
          Back to top
        </a>
      </div>
    </div>
  )
}

function GlossaryMissing({ slug }: { slug: string }) {
  return (
    <div className="space-y-4">
      <PageHeader
        eyebrow="Reference"
        title="Glossary term not found"
        description={`No glossary entry exists for "${slug}".`}
        actions={
          <Button asChild variant="outline" size="sm">
            <Link to="/glossary">
              <ChevronLeft className="size-3.5" />
              Back to index
            </Link>
          </Button>
        }
      />
    </div>
  )
}

// ---- component ----

export function GlossaryPage() {
  const { slug } = useParams<{ slug?: string }>()

  if (slug) {
    const term = TERM_BY_SLUG.get(slug)
    if (!term) return <GlossaryMissing slug={slug} />
    return <GlossaryDetail term={term} />
  }

  return <GlossaryIndex />
}

---
title: Final Combined Automated Trading Synthesis
created: 2026-06-08
tags: [automated-trading, augr, synthesis, polymarket, options, combined]
status: final-combined
source_syntheses:
  - "000 Inbox/augr_trading_research_obsidian_vault/Augr Trading Research/"
  - "001 Projects/Threads/Automated Trading/Synthesis/"
links:
  - "[[Automated Trading Synthesis]]"
  - "[[Core Theory Frameworks]]"
  - "[[Microstructure and Execution]]"
  - "[[Data Pipelines]]"
  - "[[AI Agent Architecture]]"
  - "[[Risk Controls and Guardrails]]"
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[Augr Implementation Plan]]"
  - "[[Blocking Questions and Challenges]]"
  - "[[Stretch Features Roadmap]]"
---

# Final Combined Automated Trading Synthesis

## Executive conclusion

Both syntheses reach the same central conclusion: the durable automated-trading edge in the source set is not a single prediction model, wallet, bot, or prompt. It is an **edge-processing platform**:

```text
Raw data → Normalized market state → Fair value / probability → Net edge → Risk sizing → Execution → Journal → Calibration → Regime control
```

Augr should use that platform to support two distinct but shared-infrastructure trading flows:

1. **Stocks/options:** a pricing-and-risk-first flow built around option-chain ingestion, Black-Scholes/Greeks, implied-vs-realized volatility, defined-risk structures, broker constraints, and paper trading before any live options execution.
2. **Polymarket:** a prediction-market flow built around Gamma/CLOB market discovery, binary EV, order-book microstructure, wallet/order-flow research, event/weather/news calibration, Polymarket-specific risk gates, and paper/live parity.

The final recommendation is conservative: build deterministic data, risk, execution, and journaling rails first; add agents as research and classification tools second; only later consider tiny live pilots. Do **not** begin with blind copy-trading, unmanaged live LLM execution, market-making, or sweeper bots.

## Comparison of the two syntheses

### What they have in common

The inbox synthesis and the project-folder synthesis agree on the major architecture and risk posture:

- **Both reject “magic bot” framing.** Each describes Augr as an edge-processing system rather than a raw prediction engine.
- **Both treat expected value as the shared abstraction.** For Polymarket, the core comparison is calibrated probability versus executable YES/NO price. For options, the equivalent is theoretical value versus executable bid/ask after friction.
- **Both recommend fractional Kelly only as an upper bound.** Full Kelly is too aggressive under model uncertainty, correlation, liquidity gaps, and fat-tailed outcomes.
- **Both make execution quality first-class.** Limit orders, maker/taker status, spreads, slippage, queue priority, partial fills, stale books, and fill quality can create or erase small edges.
- **Both keep LLMs out of the hot execution path.** Agents can summarize, classify, propose parameter changes, produce research notes, and generate code/config diffs for review. They should not handle private keys, silently mutate live configs, increase caps, or place live orders directly.
- **Both split the final plan into stocks/options and Polymarket flows.** Shared data/risk/journal infrastructure is reused, but each market type has its own data, execution, and risk semantics.
- **Both emphasize paper/live parity and journaling.** Strategy outputs must be measurable by PnL, Brier score, realized EV, fill quality, drawdown, latency, and rejected-trade reasons.
- **Both give official docs priority over social-thread claims.** Community writeups are useful for strategy ideas, but official API docs and reproducible code should override stale or unverifiable implementation details.

### Structural differences

The **inbox synthesis** is an Obsidian-ready research vault. Its strength is navigability and implementation clarity. It is organized around reusable notes: top-level synthesis, core theory, microstructure, data pipelines, AI agent architecture, risk controls, two flow-specific notes, delivery planning, blockers, stretch features, a source map, a glossary, and trading primitives. It uses `[[wiki links]]`, frontmatter, compact formulas, clean module names, and concise “do not implement in phase one” lists. It reads like a working implementation pack.

The **project-folder synthesis** is a deeper research report. Its strength is breadth, source verification, and repo/API evaluation. It explicitly inventories all 17 raw Markdown files, assigns source reliability tiers, documents inaccessible repos, summarizes first-order GitHub/API resources, maps the findings to current Augr docs, and gives a more detailed Augr implementation plan with exact blockers, acceptance criteria, and dependency posture. It reads like an audit-backed report.

### Unique strengths of the inbox synthesis

The inbox version contributes several strong organizing ideas that should be preserved:

- a compact architecture skeleton: `MarketDataAdapter → FeatureBuilder → StrategySignal → NetEdgeEvaluator → RiskDecisionService → ExecutionAdapter → FillNormalizer → PositionLedger → Journal + CalibrationLoop`;
- a clear shared object model: `Market`, `Instrument`, `Quote`, `OrderBook`, `Signal`, `Opportunity`, `RiskDecision`, `OrderIntent`, `Order`, `Fill`, `Position`, `TradeJournalEntry`, `RiskSnapshot`;
- a clean separation of flow-specific modules for Polymarket and stocks/options;
- initial configuration examples for Polymarket risk settings and options Greeks limits;
- a strong kill-switch taxonomy: global, strategy, venue, asset category, wallet;
- a practical strategy ranking that puts the trade journal/replay engine before higher-risk strategies;
- concise, Obsidian-friendly delivery documents for blockers and stretch features.

### Unique strengths of the project-folder synthesis

The project-folder version adds important depth and corrections:

- the raw source inventory is explicit: 17 Markdown files, no local repo checkouts, no datasets, no binary/code archives in the raw source directory;
- the source reliability tiers are more precise: official docs/SDKs and reproducible repos first, open-source patterns second, social-thread claims third;
- it identifies current external repo/API status, including that `Polymarket/py-clob-client` is deprecated/no longer functional and `Polymarket/py-sdk` is the current official SDK reference;
- it documents inaccessible or unreliable links: `dylanpersonguy/Polymarket-Trading-Bot` and `RaphaelKrutLandau/polymarket-copy-trading-bot` returned 404, while Kreo/PredictParity lacked public implementation docs;
- it identifies `KaustubhPatange/polymarket-trade-engine` as the best detailed reference for Polymarket order lifecycle, callbacks, FOK/GTC/GTD behavior, persistence, emergency exits, and simulation/live separation;
- it identifies `warproxxx/poly_data`, `Jon-Becker/prediction-market-analysis`, `NYTEMODEONLY/polyterm`, `pselamy/polymarket-insider-tracker`, `ent0n29/polybot`, `brodyautomates/polymarket-pipeline`, `alteregoeth-ai/weatherbot`, `suislanchez/polymarket-kalshi-weather-bot`, and `pmxt-dev/pmxt` as component references rather than systems to copy wholesale;
- it maps the plan to current Augr docs, including Augr’s Go architecture, analysis/research/trader/risk/execution pipeline, provider/broker configuration, Polymarket risk variables, live-trading feature flag, and known doc/runtime conflicts around Polymarket and options support;
- it is more explicit about weather-market station mapping, event/news materiality classification, wallet-score dimensions, Polymarket data schemas, and API dependency posture.

### Accuracy decisions used in this combined synthesis

Where the two syntheses differ in specificity, this combined version uses the more current or better-evidenced detail:

1. **Polymarket SDK choice:** prefer official Polymarket docs and `Polymarket/py-sdk` semantics. Treat `py-clob-client` as deprecated/historical.
2. **Runtime implementation:** because Augr is documented as Go, prefer a native Go adapter from official docs unless a sidecar is justified. Use `pmxt` as an optional bridge/design reference, not an assumed dependency.
3. **Unavailable repos:** references to inaccessible copy-trading or execution repos should not become implementation dependencies.
4. **Social-thread profit claims:** preserve strategy ideas, but treat profit numbers and trader attributions as unverified until reproduced from data.
5. **Polymarket support in Augr:** docs indicate support is more complete than older notes implied, but the implementation truth must be verified before building on it.
6. **Options support in Augr:** options appear in API/scaffolding references, but the main runtime does not look clearly first-class. The options flow should therefore start in paper mode with explicit broker and risk support.

## Scope and source map

The source set consists of 17 Markdown files:

- `$1M Math.md`
- `$40M Math.md`
- `5 Types of Bots.md`
- `6 Bots Up-Down Markets.md`
- `14,000 Polymarket Wallets.md`
- `Blockchain Surfer.md`
- `Copy Trading Guide.md`
- `Game Theory on Polymarket.md`
- `Github Repositories.md`
- `Hermes + Polymarket.md`
- `Hermes + Polymarket Weather.md`
- `Lunar Researcher Bot.md`
- `Markov Chains - Nuclear Algorithm.md`
- `Prediction Market Data.md`
- `Self-Calibrating Weather Bot.md`
- `Sweeper Bot.md`
- `Trillion Dollar Equation.md`

These fall into five practical clusters:

1. **Quant/math frameworks:** EV, Kelly, Bayesian updating, Markov chains, Monte Carlo, combinatorial constraints, Black-Scholes, Greeks.
2. **Polymarket bot strategy:** mispricing snipers, maker/spread harvesting, cross-market arb, crypto Up/Down repricing lag, near-resolution/sweeper behavior, copy/whale tracking.
3. **Data and wallet analytics:** historical trade ingestion, wallet scoring, copy-lag simulation, fresh-wallet anomaly detection, Parquet/DuckDB research workflows.
4. **Agents, weather, and event/news systems:** Hermes-style persistent agents, self-calibrating weather bots, event/news classifiers, nightly review loops.
5. **Augr integration:** existing Augr architecture, broker/data provider surfaces, Polymarket and options gaps, risk controls, implementation roadmap.

Source trust should be tiered:

1. **Highest trust:** current official API/exchange/broker docs, current Augr docs/code, reproducible active repos, empirical datasets.
2. **Medium trust:** open-source repos that demonstrate useful patterns but may not be production-ready.
3. **Lowest trust:** community posts and social threads. These are valuable as idea generators, not as proof of profitability or current API behavior.

## Unified core thesis

The central engineering problem is not “how can Augr predict outcomes?” It is “how can Augr safely transform uncertain evidence into measurable, risk-bounded, executable opportunities?”

The unified architecture should be:

```text
RawSource
  ↓
RawArchive
  ↓
Normalizer
  ↓
FeatureStore
  ↓
StrategySignal
  ↓
NetEdgeEvaluator
  ↓
SizingPolicy
  ↓
RiskDecisionService
  ↓
ExecutionAdapter
  ↓
FillNormalizer
  ↓
PositionLedger
  ↓
TradeJournal + CalibrationLoop + RegimeController
```

This skeleton should support both flows. The differences belong in market-specific adapters, signal modules, execution semantics, and risk gates.

### Shared platform services

Augr should share the following services across stocks/options and Polymarket:

- `MarketDataService`
- `RawArchive`
- `FeatureStore`
- `StrategyRegistry`
- `SignalRegistry`
- `NetEdgeEvaluator`
- `SizingPolicy`
- `RiskDecisionService`
- `ExecutionRouter`
- `FillNormalizer`
- `PositionLedger`
- `TradeJournal`
- `ReplayEngine`
- `CalibrationStore`
- `RegimeScheduler`
- `AlertingService`
- `KillSwitchService`
- `SecretsBoundary`

### Shared objects

Core objects should include:

```text
Market
Instrument
Quote
OrderBook
Signal
Opportunity
RiskDecision
OrderIntent
Order
Fill
Position
RiskSnapshot
TradeJournalEntry
CalibrationSnapshot
ExternalEvidence
```

The most important design constraint is that strategies emit **signals** or **order intents**, not final live orders. Risk and execution services decide whether intent becomes action.

## Quant and theory framework

### Expected value

For a binary Polymarket-style contract paying $1, bought at price `q` with true probability estimate `p`:

```text
EV = p × (1 - q) - (1 - p) × q
```

For quick ranking, this reduces to:

```text
Edge = p - q
```

That simplified edge is not enough for execution. Production net EV must subtract:

- bid/ask spread;
- taker fees;
- slippage;
- partial-fill risk;
- adverse selection;
- settlement delay and capital lockup;
- model uncertainty.

For options, the equivalent framing is:

```text
Option edge = theoretical_value - executable_price - commission - slippage - model_haircut
```

For buys, executable price is usually ask or a conservative fill estimate. For sells, executable price is bid. Midpoint-only backtests should not be trusted.

### Fractional Kelly with hard caps

Kelly is useful as a sizing reference, not a command. For binary contracts:

```text
f* = (p × b - q) / b
q = 1 - p
b = net odds received per unit risked
```

At Polymarket price `price`, gross odds can be represented as:

```text
b = (1 / price) - 1
```

Augr should use quarter Kelly or smaller by default, then cap by liquidity, strategy, category, portfolio, daily-loss, and market-specific risk limits:

```text
final_size = min(
  fractional_kelly_size,
  liquidity_cap,
  strategy_cap,
  category_cap,
  market_type_cap,
  daily_loss_cap_remaining,
  portfolio_risk_cap_remaining
)
```

### Bayesian updating

Bayesian updating is most useful when a new event changes the probability distribution: earnings, macro releases, weather updates, politics, sports injuries, crypto exchange outages, court rulings, or breaking news.

The practical pattern is:

1. start with a prior from market price, model, or historical base rate;
2. classify the evidence and estimate a likelihood ratio;
3. update the probability;
4. trade only if the posterior edge survives execution and risk gates.

LLMs can help classify direction and materiality. They should not be treated as calibrated probability engines unless their outputs have been measured and calibrated.

### Markov chains and Monte Carlo

The Markov sources are useful for state persistence in short-window or regime-driven markets. A useful implementation pattern is:

1. discretize state: price band, momentum, gap-to-reference, volatility regime, order-book imbalance, time remaining;
2. build a transition matrix from historical data;
3. estimate state persistence or terminal probability;
4. enter only when model probability exceeds executable market price by a minimum edge.

The repeated threshold `p(j*, j*) ≥ 0.87` should be treated as a backtest hypothesis, not a universal rule.

Monte Carlo complements Markov models when exact enumeration is impractical. It can estimate event probabilities, options payoff distributions, weather-threshold probabilities, and drawdown scenarios.

### Black-Scholes, Greeks, and volatility

The `Trillion Dollar Equation` source maps most directly to the stocks/options flow. Black-Scholes is a benchmark, not reality. It gives Augr a disciplined way to decompose option value into:

- underlying price;
- strike;
- time to expiration;
- risk-free rate;
- volatility.

Options automation must also track Greeks:

- **Delta:** directional exposure;
- **Gamma:** convexity and rebalance risk;
- **Vega:** implied-volatility exposure;
- **Theta:** time decay.

Greeks should become hard constraints in the options risk adapter.

### Combinatorial and cross-market constraints

The `$40M Math` source points toward a valid advanced idea: related markets impose constraints. Examples include mutually exclusive event groups, YES/NO parity, neg-risk groups, geographic/political dependency chains, cross-timeframe crypto markets, and cross-venue equivalents.

The source mentions Bregman projection and Frank-Wolfe optimization. Those ideas belong in a research lane, not phase one. Before live use, Augr needs reliable market dependency classification, multi-leg execution controls, partial-fill failure handling, and solver infrastructure.

### Regime filters

The `Blockchain Surfer` source is especially useful because it treats strategy operation as regime control. Augr should record and gate performance by:

- UTC hour;
- weekday/weekend;
- market category;
- liquidity band;
- spread band;
- volatility band;
- time to resolution/expiration;
- maker/taker status;
- order type;
- consecutive-loss clusters.

Some losing strategies may be invertible under stable conditions, but inversion should remain a research feature until validated by sample size, replay, and asymmetric-tail-risk analysis.

## Flow 1: Stocks/options

### Objective

Build a pricing-and-risk-first stocks/options module that identifies liquid, defined-risk opportunities from fair-value gaps, volatility mispricing, and event/regime context.

The options flow should not begin as a directional YOLO system. It should begin as a fair-value, volatility, and Greeks engine.

### Required modules

```text
EquityUniverseSelector
OptionsChainIngestor
VolatilityEstimator
BlackScholesPricer
GreeksCalculator
SpreadBuilder
OptionsRiskAdapter
BrokerExecutionAdapter
FillNormalizer
PnLAttribution
```

### Data requirements

The stocks/options flow needs:

- underlying quotes and OHLCV;
- options chains;
- bid/ask and size;
- open interest and volume;
- implied volatility;
- realized volatility windows;
- risk-free rates/dividends where relevant;
- corporate actions;
- earnings and event calendars;
- broker positions and margin state.

Augr docs already mention provider surfaces such as Polygon, Alpha Vantage, Finnhub, NewsAPI, FMP, Yahoo, and Binance, but the implementation must verify which of these are wired and entitled for real-time options data.

### Initial screening process

1. Filter liquid underlyings.
2. Fetch option chains.
3. Remove contracts with poor spread, low open interest, stale quotes, or poor size.
4. Calculate theoretical value, implied volatility, realized volatility, and Greeks.
5. Compare executable bid/ask to model value.
6. Construct defined-risk expressions.
7. Stress test the position.
8. Submit only paper orders until broker and assignment semantics are proven.

### Preferred phase-one strategies

- **Defined-risk vertical spreads:** controlled directional or probability views.
- **Fair-value debit trades:** only when theoretical edge exceeds spread and theta burden.
- **Volatility-screened premium selling:** defined-risk only; no naked short options.
- **Calendar spreads:** useful for term-structure mispricing, but later than verticals because modeling is harder.

### Greek and portfolio constraints

Initial constraints should include:

```yaml
options:
  max_abs_delta_pct_equity: 0.20
  max_gamma_notional_pct_equity: 0.05
  max_vega_pct_equity: 0.10
  max_daily_theta_pct_equity: 0.02
  max_expiry_bucket_pct_equity: 0.20
  max_single_underlying_pct_equity: 0.10
  prohibit_uncovered_short_options: true
```

### PnL attribution

Every closed options trade should attribute PnL to:

- delta;
- gamma/convexity;
- vega;
- theta;
- execution/slippage;
- residual/model error.

This tells Augr whether a strategy made money from its intended edge or from accidental exposure.

### Phase-one prohibitions

Do not implement these until the options platform is mature:

- naked short-volatility books;
- uncontrolled same-week-expiry gamma strategies;
- trades sized from full Kelly;
- midpoint-only backtests;
- broker execution without hard risk checks;
- LLM-placed options orders.

## Flow 2: Polymarket

### Objective

Build a current-docs-compliant Polymarket adapter and strategy layer for Augr that supports market discovery, CLOB order-book ingestion, calibrated probability strategies, paper/live parity, wallet-safe execution, and isolated advanced latency strategies later.

### Required modules

```text
PolymarketMarketDiscovery
PolymarketOrderBookStream
PolymarketAccountState
PolymarketOrderExecutor
PolymarketFillNormalizer
PolymarketResolutionTracker
PolymarketRiskAdapter
PolymarketStrategyRegistry
```

More granular project modules should include:

| Module | Purpose | Priority |
|---|---|---|
| `GammaDiscoveryProvider` | Active/closed market discovery, tags, outcomes, volume, liquidity | MVP |
| `ClobOrderBookProvider` | Best bid/ask, depth, spread, live updates | MVP |
| `BinaryEVSignal` | Probability-price edge calculation | MVP |
| `ShortWindowCryptoSignal` | BTC/ETH/SOL Up/Down state model | MVP |
| `PolymarketRiskGate` | Liquidity, spread, exposure, resolution, jurisdiction gates | MVP |
| `PolymarketPaperBroker` | Paper fills against book snapshots | MVP |
| `PolymarketExecutionBroker` | Live signed orders, fills, cancels, redemption | After validation |
| `WalletSignalProvider` | Wallet/whale/smart-money analytics | Phase 2 |
| `ConstraintArbScanner` | YES/NO, neg-risk, related-market, cross-venue constraints | Phase 2 |
| `NewsEventClassifier` | Event-news direction/materiality | Phase 2 |
| `WeatherSignalProvider` | Open-Meteo/Visual Crossing/METAR domain strategy | Phase 2/3 |
| `MarketMaker` | Two-sided quoting and inventory skew | Stretch |
| `SweeperScanner` | Near-resolution manual alert/research scanner | Stretch |

### Data adapters

Use Gamma-style metadata for market discovery:

- event ID;
- market ID;
- condition ID;
- token IDs;
- question;
- outcomes;
- category;
- close/resolution time;
- status;
- volume and liquidity.

Use CLOB data for:

- best bid/ask;
- midpoint;
- spread;
- depth by price level;
- open orders;
- order placement;
- cancellation;
- fills.

Use account/data endpoints or normalized fill records for:

- balances;
- positions;
- open orders;
- fills;
- realized/unrealized PnL;
- resolution/redemption state.

For historical research, build or adapt a pipeline inspired by `poly_data` and `prediction-market-analysis`. Avoid deprecated subgraph assumptions. Store heavy offline data in Parquet/DuckDB if Postgres becomes too slow.

### Polymarket strategy lanes

#### Lane 1 — Short-window crypto Up/Down paper bot

This is the recommended first Polymarket strategy family because market structure is objective and repeatable.

Inputs:

- current underlying price versus price-to-beat;
- time remaining;
- Polymarket UP/DOWN order books;
- order-book imbalance;
- exchange price feeds;
- momentum and volatility;
- Markov state persistence.

Risk gates:

- max price;
- min liquidity;
- max spread;
- max trade size;
- session loss;
- pause after consecutive losses;
- no near-resolution entry unless the strategy explicitly supports that latency/tail-risk class.

#### Lane 2 — Binary EV and constraint scanner

This lane scans for transparent mispricing:

- YES/NO parity;
- complete-set constraints;
- neg-risk groups;
- related-market constraints;
- cross-timeframe lag;
- cross-venue candidate matches as alerts only.

Execution should start as alert/paper only until market grouping, venue equivalence, and multi-leg fill assumptions are proven.

#### Lane 3 — Calibrated event/weather forecasting

Weather and other objective-data markets are useful because they provide a natural calibration loop.

For weather:

1. discover active weather markets;
2. parse city/station/date/temperature bucket;
3. fetch ensemble forecasts and real observations;
4. estimate probability from the ensemble;
5. adjust for station/source error;
6. compare to executable book prices;
7. paper trade;
8. store outcomes and update calibration.

The key warning is station mapping: Polymarket weather markets may resolve against airport/station data, not city-center coordinates. A 3-8°F coordinate error can flip narrow temperature-bucket markets.

For event/news:

```text
Breaking news → Market match → Direction/materiality classification → Probability update → EV gate → Risk gate → Paper/live decision → Calibration
```

Use LLMs for classification and evidence summaries, not direct sizing or order placement.

#### Lane 4 — Wallet intelligence

Use wallet data for:

- market discovery;
- category specialization inference;
- suspicious-flow alerts;
- post-trade research;
- copy-lag simulations.

Avoid blind copy-trading. A profitable source wallet can still be uncopyable if Augr gets worse fills, arrives late, misses exits, copies a hedged position, or becomes the wallet’s exit liquidity.

#### Lane 5 — Near-resolution/sweeper strategies

Sweeper bots buy near-certain winners before settlement converges to $1. This requires exact resolution timing, trusted reference feeds, low-latency CLOB order placement, queue priority awareness, and tight tail-risk caps.

Recommended status: manual alert/research only until compliance and latency infrastructure are proven.

### Polymarket execution semantics

`polymarket-trade-engine` is the strongest detailed execution reference. Augr should borrow its design patterns:

- one lifecycle per market/round;
- explicit INIT/RUNNING/STOPPING/DONE states;
- order callbacks;
- state persistence and crash recovery;
- open-order reconciliation;
- emergency cancel/sell;
- fee-aware filled-share accounting;
- structured per-market logs;
- simulation/live separation.

Execution rules to encode:

- GTC/GTD rest on book and act as maker;
- FOK/FAK act as taker and require immediate execution;
- actual filled shares, not requested shares, drive follow-up logic;
- stale orders must be cancelled;
- strategy callbacks cannot be trusted unless persisted and recoverable after restart.

### Polymarket dependency posture

| Need | Preferred source | Alternative | Avoid |
|---|---|---|---|
| Polymarket semantics | Official docs + `Polymarket/py-sdk` semantics | `polymarket-cli` smoke tests | Deprecated `py-clob-client` for new code |
| Augr runtime | Native Go adapter from official docs | `pmxt` sidecar if cross-venue support is needed | Shelling out to CLI for core trading |
| Historical trades | Own pipeline inspired by `poly_data` | `prediction-market-analysis` dataset | Deprecated subgraph-only tooling |
| Order lifecycle | `polymarket-trade-engine` design | custom Augr design | direct LLM execution |
| Operator tooling | Augr CLI/TUI with `polyterm` ideas | run `polyterm` as companion | custodial unknown bots |
| Weather signals | Open-Meteo + station metadata + calibration | Visual Crossing enrichment, METAR validation | city-center coordinates |
| News signals | deterministic ingestion + LLM classification | manual alerts | LLM raw probability as sole signal |

## Data, wallet analytics, and copy-trading

### Data principle

Augr should separate raw venue data from normalized research objects:

```text
RawSource → RawArchive → Normalizer → FeatureStore → StrategyInput → Journal
```

No strategy should consume raw live API responses without schema validation and an archival path.

### Polymarket historical data model

Suggested tables/events:

```text
polymarket_market_snapshots
- market_id
- condition_id
- slug
- question
- outcomes
- clob_token_ids
- active
- closed
- end_date
- volume
- liquidity
- fetched_at

polymarket_orderbook_snapshots
- market_id
- token_id
- best_bid
- best_ask
- bid_depth_top_n
- ask_depth_top_n
- spread
- midpoint
- fetched_at

polymarket_trades
- tx_hash
- log_index
- market_id
- token_id
- maker
- taker
- side
- price
- token_amount
- usd_amount
- fee
- block_number
- timestamp

polymarket_wallet_scores
- wallet_address
- sample_size
- pnl_total
- roi
- win_rate
- max_drawdown
- category_mix
- avg_entry_liquidity
- copy_latency_sensitivity
- anomaly_score
- updated_at
```

### Wallet scoring

A simple first pass groups trades by maker, counts trades, estimates win rate, sums PnL, filters by sample size and win rate, and ranks by total PnL. That is not enough for trading.

Augr should also score:

- sample size;
- ROI and max drawdown;
- category mix;
- entry liquidity;
- average entry/exit price;
- hold time;
- early-exit behavior;
- copy-latency sensitivity;
- funding patterns;
- concentration risk;
- possible wash/sybil behavior;
- whether the wallet is directional, market-making, hedging, or transferring.

### Safe copy-trading stages

1. **Observe only:** track wallet signals and record hypothetical copy fills.
2. **Shadow trade:** simulate entry and exit with latency assumptions.
3. **Alert mode:** notify operator when a wallet passes filters.
4. **Tiny live pilot:** only with manual approval, hard caps, and stop rules.
5. **Automated copy:** only after enough out-of-sample copy-fill evidence.

## Agents, weather, and news

### Safe agent roles

Agents are useful for:

- market briefs;
- evidence collection;
- event/news classification;
- market dependency labeling;
- daily journal summaries;
- parameter-change proposals;
- code/config diffs for review;
- documentation maintenance.

Agents must not:

- reveal, store, or manipulate private keys;
- approve contracts independently;
- switch to live trading;
- bypass kill switches;
- increase risk caps without review;
- rewrite live strategy configs silently;
- place unbounded orders.

### Safe self-learning loop

```text
Trade journal closes day
  ↓
Metrics and calibration are computed
  ↓
Agent reviews journal and proposes changes
  ↓
Backtest/replay validates proposal
  ↓
Risk policy checks proposal
  ↓
Human or deterministic canary gate approves
  ↓
Next session uses approved config
```

Every agent output should store prompt version, model, evidence references, confidence, and approval status.

### Weather/event implementation notes

Weather systems should combine Open-Meteo, METAR/AviationWeather, and Visual Crossing where appropriate. Open-Meteo is the simplest default; Visual Crossing is useful for richer history and actuals; METAR matters when airport stations drive resolution.

Event/news systems should classify direction and materiality rather than blindly generate probabilities. The trading layer should translate event labels into probability updates using calibrated mappings.

## Risk controls and governance

### Risk philosophy

Augr should assume every model is sometimes wrong, every API eventually fails, and every strategy periodically enters a hostile regime. Risk control must be separate from strategy logic.

### Capital controls

- max position size by strategy;
- max position size by market/instrument;
- max aggregate exposure by category;
- max daily/weekly loss;
- max drawdown before pause;
- max correlated exposure;
- fractional Kelly cap;
- liquidity-based cap;
- market-type-specific caps.

### Execution controls

- max spread;
- minimum depth;
- max slippage;
- order timeout;
- stale quote check;
- partial-fill handling;
- cancel-on-disconnect;
- post-only enforcement for maker strategies;
- no blind retry loops without price refresh.

### Regime controls

Pause or disable when:

- consecutive losses exceed threshold;
- rolling win rate drops below floor;
- fill rate collapses;
- slippage exceeds baseline;
- volatility exceeds calibrated range;
- liquidity disappears;
- API latency spikes;
- market data sources conflict;
- external events invalidate the model assumption.

### Polymarket-specific risks

- stale API or documentation assumptions;
- wrong collateral/token flow;
- wallet/private-key exposure;
- wrong resolution source;
- near-resolution reversal;
- queue loss;
- CLOB partial fills;
- changing fees/rewards;
- on-chain settlement delays;
- market cancellation or ambiguity;
- jurisdiction and ToS constraints.

### Options-specific risks

- theta bleed;
- implied-volatility crush;
- jump risk;
- gamma risk near expiry;
- assignment/pin risk;
- margin call;
- liquidity gaps;
- exercise/settlement mismatch;
- corporate actions;
- model miscalibration.

### Security controls

- dedicated trading wallets/accounts;
- minimal funded balances;
- no unlimited approvals unless justified, monitored, and revocable;
- dependency audit;
- lockfile review;
- no secrets in logs or prompts;
- read-only research mode by default;
- strict separation of paper and live environments;
- restricted CI/CD secret access;
- prompt/config change audit trail.

### Kill switches

Implement and persist:

```text
GLOBAL_KILL_SWITCH
STRATEGY_KILL_SWITCH
VENUE_KILL_SWITCH
ASSET_CATEGORY_KILL_SWITCH
WALLET_KILL_SWITCH
MARKET_TYPE_KILL_SWITCH
```

Every live process must check kill-switch state before creating, modifying, or retrying orders.

## Augr implementation roadmap

### Phase 0 — Safety and architecture audit

Applies to both flows.

Tasks:

- verify the current Augr runtime, packages, and deployment model;
- verify whether Polymarket support is retail API, CLOB, or hybrid;
- verify whether options are first-class runtime, scaffold-only, or absent;
- map current data providers, broker adapters, strategy abstractions, tests, CI, secrets, scheduler, queue/event bus, and storage;
- confirm paper trading support;
- confirm persistent kill switch behavior;
- add or verify live-trading feature flags.

Deliverables:

- `AUGR_ARCHITECTURE_AUDIT.md`;
- integration map;
- missing-service list;
- live-trading safety checklist.

Acceptance criteria:

- a developer can run one stock paper strategy and one Polymarket paper strategy without live credentials;
- every decision includes evidence, risk decision, and audit log;
- live execution cannot be enabled by strategy code alone.

### Phase 1 — Shared quant/risk foundation

Build:

- binary EV;
- fee-aware EV;
- fractional Kelly;
- confidence haircut;
- Brier/log-loss metrics;
- calibration buckets;
- drawdown/session-loss tracking;
- normalized signal contract;
- normalized risk-decision contract;
- trade journal and replay engine.

### Phase 2A — Stocks/options paper flow

Build:

- options chain ingestion;
- theoretical pricing;
- Greeks calculator;
- realized volatility estimator;
- implied-vs-realized volatility scanner;
- defined-risk spread builder;
- options portfolio risk aggregation;
- paper broker adapter.

Initial strategy:

```text
Liquid option chain → theoretical price → executable price check → Greek limits → defined-risk expression → paper order
```

Acceptance criteria:

- ranked options opportunities can be produced;
- price, IV, realized vol, delta, gamma, vega, theta can be calculated or verified;
- trades can be rejected for spread, liquidity, theta, or portfolio risk;
- PnL attribution exists after close;
- no live options order can be submitted without explicit options-live support and feature flags.

### Phase 2B — Polymarket paper flow

Build:

- official-docs-compliant market discovery;
- CLOB order-book adapter;
- WebSocket book updates where useful;
- account/position adapter;
- paper execution wrapper;
- fill normalizer;
- resolution tracker;
- Polymarket-specific risk adapter.

Initial strategy priority:

1. short-window crypto Up/Down paper strategy;
2. binary EV and constraint scanner;
3. wallet/intelligence observe-shadow-alert module;
4. weather/event paper module once structured market parsing is safe.

Acceptance criteria:

- Augr can discover active markets, fetch books, calculate spread/depth/midpoint, simulate orders, normalize fills, and journal market metadata;
- every Polymarket order records token ID, side/outcome mapping, order type, estimated fee, filled shares, fill quality, and resolution/redemption state;
- live Polymarket mode requires credentials, `ENABLE_LIVE_TRADING=true`, market-type halt disabled, and risk gates passed.

### Phase 3 — Agentic research loop

Build an agent workflow that:

- reads daily journals;
- summarizes PnL and failure modes;
- classifies events and market descriptions;
- proposes parameter changes;
- writes Obsidian notes;
- prepares code/config diffs;
- requires approval before promotion.

### Phase 4 — Monitoring and operator UX

Dashboards/alerts should show:

- live strategy status;
- open positions/orders;
- fill quality;
- maker/taker PnL;
- signal calibration;
- Brier score by strategy;
- PnL/drawdown;
- API latency/error rate;
- kill-switch state;
- market-type halt state;
- wallet/news/weather alerts.

Use existing Augr notification channels where configured, such as Telegram, Discord, n8n, and PagerDuty. Keep signal alerts, decision alerts, and operational incidents separate.

### Live pilot gate

Only consider tiny live pilots when all are true:

- sufficient paper sample size;
- realized EV by edge bucket remains positive after conservative slippage;
- drawdown and loss limits are stable;
- fill quality matches assumptions;
- kill switches and venue halts are tested;
- legal/jurisdiction questions are answered;
- credentials are isolated from agents;
- operator has reviewed the strategy, risk policy, and incident runbook.

## Blocking questions and challenges

### Repository and architecture

1. What is the exact Augr runtime and framework at implementation time?
2. Which data adapters and broker adapters already exist?
3. Does Augr already have a trade journal, ledger, replay engine, or paper broker?
4. What database is used, and can it handle time-series/order-book workloads?
5. Is there a queue/event bus?
6. What abstractions already exist for strategies, signals, orders, fills, positions, and risk?

### Legal and compliance

1. Who is allowed to trade Polymarket through Augr?
2. What Polymarket jurisdiction/ToS constraints apply?
3. Are stock/options strategies subject to PDT, wash-sale, suitability, margin, or broker-specific constraints?
4. Will Augr trade only owner capital, or could it ever touch third-party capital?

### Broker/API

1. Which broker supports live options, if any?
2. Are real-time options data entitlements available?
3. Are Greeks provided or computed internally?
4. Which Polymarket path is the implementation truth: retail API, CLOB, or both?
5. What wallet/signature model is used: EOA, proxy, deposit wallet, Gnosis Safe?
6. How are approvals, funder/proxy addresses, redemption, and stuck orders handled?
7. What are current rate limits, fee schedules, tick sizes, and minimum order sizes?

### Data quality and backtesting

1. Can Augr reliably map Polymarket market text to structured rules/outcomes?
2. Are weather station mappings verified against actual resolution rules?
3. How much order-book history is required for validation?
4. Can options backtests use quote-level bid/ask data?
5. How will survivorship bias, market closures, cancellations, and ambiguous resolutions be represented?
6. How will stale/missing/conflicting data halt trading?

### Strategy validation

1. What minimum paper-trade sample size is required before live pilot?
2. What edge decay is acceptable between signal timestamp and fill?
3. Which metrics disable a strategy: Brier score, drawdown, realized EV, fill quality, latency, or all?
4. How will copy-trade performance account for worse fills than the source wallet?
5. How will capacity limits be measured for each strategy?

### Operations and security

1. Where are private keys stored, and can agents ever read them? Recommended answer: no.
2. Who can deploy live strategy configs?
3. Who can change risk caps?
4. How are live-trading feature flags protected from agent modification?
5. Does kill-switch state survive restart and deployment?
6. What happens on API outage, WebSocket disconnect, partial fill, journal write failure, or strategy order flood?
7. How are dependencies, prompts, and model versions audited?

## Nice-to-have and stretch features

Prioritize these only after the shared paper platform is stable:

1. **Category and regime scheduler:** enable/disable by hour, category, spread, depth, volatility, and recent performance.
2. **Maker-quality scorecard:** measure whether maker-first Polymarket strategies capture spread after adverse selection and queue loss.
3. **Cross-flow risk cockpit:** one capital-at-risk view across stocks/options and Polymarket.
4. **Wallet intelligence dashboard:** smart-wallet discovery, category specialization, entry/exit timing, copy-lag simulation.
5. **Agent-safe MCP/research tools:** allow read-only market/wallet/orderbook queries without exposing keys.
6. **Options volatility surface explorer:** visualize skew, term structure, IV rank, realized vol, and theoretical mispricing.
7. **Options strategy builder:** generate defined-risk structures with scenario PnL and Greek constraints.
8. **Strategy replay workbench:** replay Polymarket books, stock/options bars, news events, and Augr decisions.
9. **Self-calibrating thresholds:** adapt EV thresholds, Kelly fractions, and pause rules only through review-gated changes.
10. **Resolution-risk/oracle monitor:** flag ambiguous Polymarket markets, UMA/Chainlink/dispute risks, and stale resolution assumptions.
11. **Cross-venue prediction-market abstraction:** compare Polymarket/Kalshi/Limitless prices; useful but hard due to market matching and settlement differences.
12. **Market-making and inventory skew engine:** advanced only; public references warn naive market-making is crowded and likely unprofitable.
13. **Near-resolution latency engine:** specialized sweeper infrastructure with precise clocks, reference feeds, queue-aware orders, and tiny tail-risk budgets.
14. **Multi-agent scenario simulation:** use persona/scenario models to stress-test subjective event markets, but do not trade directly from them.

Avoid until mature:

- LLM-only trade decisions;
- automatic live config mutation;
- unlimited wallet approvals;
- naked short options;
- full-Kelly sizing;
- direct copy-trading without latency simulation;
- midpoint-only options backtests.

## Reusable trading primitives

These snippets are implementation sketches, not production-ready code.

### Binary EV

```python
def binary_ev(p_true: float, price: float) -> float:
    """EV per $1-payout share before fees/slippage."""
    return p_true * (1.0 - price) - (1.0 - p_true) * price
```

### Fractional Kelly with cap

```python
def fractional_kelly(p: float, price: float, fraction: float = 0.25, cap: float = 0.05) -> float:
    if price <= 0 or price >= 1:
        return 0.0
    b = (1.0 / price) - 1.0
    q = 1.0 - p
    full = (p * b - q) / b
    return max(0.0, min(full * fraction, cap))
```

### Bayesian update

```python
def bayes_update(prior: float, likelihood_event_if_true: float, likelihood_event_if_false: float) -> float:
    numerator = likelihood_event_if_true * prior
    denominator = numerator + likelihood_event_if_false * (1.0 - prior)
    if denominator == 0:
        return prior
    return numerator / denominator
```

### Markov persistence gate

```python
def should_enter_state(P, current_state, market_price, tau=0.87, eps=0.05):
    fair = P[current_state][current_state]
    return fair >= tau and (fair - market_price) >= eps
```

### Generic risk gate

```python
def risk_gate(opportunity, portfolio, config):
    reasons = []

    if opportunity["net_edge"] < config["min_net_edge"]:
        reasons.append("edge_below_minimum")
    if opportunity["spread"] > config["max_spread"]:
        reasons.append("spread_too_wide")
    if opportunity["depth_usd"] < config["min_depth_usd"]:
        reasons.append("depth_too_low")
    if portfolio["daily_loss"] <= -config["max_daily_loss"]:
        reasons.append("daily_loss_limit_hit")
    if portfolio.get("consecutive_losses", 0) >= config.get("consecutive_loss_pause", 3):
        reasons.append("loss_cluster_pause")

    return len(reasons) == 0, reasons
```

### Event/news classifier output contract

```json
{
  "market_id": "...",
  "event_id": "...",
  "direction": "bullish_yes | bearish_yes | neutral | irrelevant",
  "materiality": 0.0,
  "evidence": ["url or source id"],
  "reason": "short cited explanation",
  "latency_ms": 0,
  "model": "...",
  "prompt_version": "event-classifier-v1"
}
```

## Final integrated recommendation

The combined synthesis supports a clear build order:

1. **Audit Augr and lock down safety.** Verify actual code/runtime support for Polymarket and options, confirm paper mode, and harden kill switches and secrets boundaries.
2. **Build shared EV/risk/journal primitives.** These are the foundation for both flows and every strategy.
3. **Start two paper-only tracks in parallel.**
   - Stocks/options: option-chain valuation, Greeks, defined-risk scanner, conservative paper fills.
   - Polymarket: Gamma/CLOB discovery, short-window crypto binary EV paper strategy, Polymarket-specific risk gates.
4. **Add data intelligence after core execution works.** Wallet analytics, news/event classification, weather-market calibration, and cross-market constraints should feed signals, not bypass risk.
5. **Use agents for research and review.** Keep execution, sizing, secrets, and kill switches deterministic.
6. **Gate live trading heavily.** Only tiny live pilots should be considered after out-of-sample paper validation, legal review, stable operations, and operator approval.

Augr’s advantage should be disciplined orchestration and calibration across market types, not unbounded autonomy.

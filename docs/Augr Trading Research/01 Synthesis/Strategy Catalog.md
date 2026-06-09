---
title: Strategy Catalog
created: 2026-06-08
tags: [strategies, polymarket, options, augr]
status: packaged
links:
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[Risk Controls and Guardrails]]"
---

# Strategy Catalog

## Polymarket strategy families

### Maker/spread harvesting

**Idea:** Post limit orders where behavioral taker flow overpays for immediacy or hope.

**Applies to:** [[Polymarket Flow]]

**Best conditions:** liquid markets, stable spreads, high taker activity, repeatable flow.

**Risks:** adverse selection, stale orders, queue loss, regime shifts.

### Longshot-bias harvesting

**Idea:** Low-probability affirmative outcomes may be overpriced; high-probability outcomes may be underpriced.

**Applies to:** [[Polymarket Flow]] and analogously to options lottery demand.

**Implementation:** avoid buying low-probability YES contracts as taker; consider maker-side or NO-side structures where allowed and liquid.

**Risks:** tail events, correlation, borrow/shorting analog constraints, poor liquidity.

### Repricing/fair-value model

**Idea:** Build a fair probability from external data and compare against market price.

**Examples:** weather, crypto Up/Down, macro events, sports if model speed is adequate.

**Risks:** stale data, wrong resolution source, overconfident probabilities, slippage.

### Cross-timeframe/multi-market lag

**Idea:** Related markets should reprice together but sometimes lag.

**Examples:** 5-minute vs 15-minute crypto Up/Down, related elections/geographic markets.

**Risks:** dependency model error, partial-fill risk, hedges not behaving as expected.

### Near-resolution/sweeper

**Idea:** Buy effectively resolved winning inventory below final payout.

**Best conditions:** outcome is nearly certain, queue position is early, execution is fast.

**Risks:** huge tail losses relative to tiny per-fill gain.

### Copy/whale-informed trading

**Idea:** Use successful wallets for discovery and filtering.

**Recommended Augr use:** research signal, not direct execution by default.

## Stocks/options strategy families

### Fair-value option buying

**Idea:** Buy options below theoretical value.

**Requires:** volatility estimate, model price, Greeks, liquidity filter, exit plan.

**Risks:** theta decay, volatility crush, model error.

### Volatility mean reversion

**Idea:** Compare implied volatility against realized volatility and historical IV distribution.

**Use cases:** premium selling, defined-risk spreads, calendars.

**Risks:** short-vol tail losses, event jumps.

### Defined-risk spreads

**Idea:** Express directional or volatility views with bounded loss.

**Preferred phase-one expressions:** verticals, debit/credit spreads, calendars with defined allocation.

**Risks:** assignment, pin risk, poor fill quality.

### Greek-neutral relative value

**Idea:** Construct trades where delta is bounded and PnL source is volatility, skew, carry, or convergence.

**Status:** later phase after pricing, Greeks, and margin integration are robust.

## Strategy enablement ranking for Augr

| Priority | Strategy | Flow | Reason |
|---:|---|---|---|
| 1 | Trade journal + replay | Both | Required for every other feature |
| 2 | Options fair-value and Greek scanner | Stocks/options | High explainability, controlled live risk |
| 3 | Polymarket maker-first scanner | Polymarket | Aligns with microstructure evidence |
| 4 | Weather-style calibrated forecasting | Polymarket | Clear data loop and calibration path |
| 5 | Regime scheduler | Both | Improves all strategies |
| 6 | Wallet/whale research layer | Polymarket | Good discovery, lower direct-trade trust |
| 7 | Near-resolution sweeper | Polymarket | Potential edge but latency/tail-risk heavy |
| 8 | Solver-based combinatorial arb | Polymarket | Powerful but high infrastructure burden |

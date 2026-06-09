---
title: Polymarket Flow
created: 2026-06-08
tags: [polymarket, prediction-markets, clob, augr]
status: packaged
links:
  - "[[Automated Trading Synthesis]]"
  - "[[Microstructure and Execution]]"
  - "[[Data Pipelines]]"
  - "[[Strategy Catalog]]"
  - "[[Risk Controls and Guardrails]]"
  - "[[Augr Implementation Plan]]"
  - "[[Glossary]]"
---

# Polymarket Flow

## Flow objective

Build a current-docs-compliant Polymarket adapter and strategy layer for Augr that supports:

- market discovery
- order-book ingestion
- maker-first execution
- calibrated probability strategies
- journaled backtesting and replay
- wallet-safe live execution
- isolated advanced latency strategies later

## Required modules

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

## Data adapters

### Discovery adapter

Use official market/event metadata to normalize:

- event ID
- market ID
- token IDs
- question
- outcomes
- category
- close/resolution time
- activity status
- liquidity and volume

### Order-book adapter

Normalize:

- best bid
- best ask
- midpoint
- spread
- depth by level
- timestamp
- market status

### Account adapter

Normalize:

- balances
- positions
- open orders
- fills
- realized/unrealized PnL

## Strategy lanes

### Lane 1 — Maker-first structural edge

Default phase-one Polymarket strategy lane.

Rules:

- use limit orders
- prefer post-only
- require minimum depth
- require max spread
- record maker/taker status
- cancel stale orders
- analyze performance by category and hour

### Lane 2 — Calibrated event forecasting

Best first target: weather-style markets or other markets with objective external data and clear resolution rules.

Architecture:

```text
External data source → forecast snapshot → probability model → EV gate → Kelly cap → limit order → outcome archive → calibration
```

### Lane 3 — Crypto Up/Down microstructure

Candidate strategies:

- Markov persistence
- repricing lag
- cross-timeframe lag
- two-sided hedged structures
- near-resolution certainty checks

Status: backtest first. These are latency-sensitive and vulnerable to overfitting.

### Lane 4 — Near-resolution / sweeper

Advanced lane. Requires:

- exact resolution timing
- reference-price feed
- low-latency CLOB order placement
- queue priority awareness
- tail-risk cap
- strong monitoring

Should be separate from slower event-trading bots.

### Lane 5 — Wallet intelligence

Use successful wallets for:

- market discovery
- strategy categorization
- suspicious-flow alerts
- post-trade research

Avoid direct blind copy-trading unless execution delay and slippage are proven acceptable.

## Polymarket-specific net edge formula

```python
def polymarket_net_ev(p_true, ask, exit_haircut=0.0, fee=0.0, slippage=0.0):
    gross_ev = p_true * (1 - ask) - (1 - p_true) * ask
    return gross_ev - exit_haircut - fee - slippage
```

## Initial config proposal

```yaml
polymarket:
  mode: paper
  order_mode: maker_first
  default_order_type: post_only
  min_edge: 0.05
  max_spread: 0.03
  min_depth_usd: 5000
  kelly_fraction: 0.25
  max_position_pct_bankroll: 0.05
  max_category_exposure_pct: 0.20
  consecutive_loss_pause: 3
  stale_order_seconds: 30
  official_docs_required_for_contracts: true
```

## Data to journal

- market question
- category
- outcome/token
- resolution timestamp
- model probability
- market price used
- bid/ask/spread/depth
- EV before and after friction
- order type
- maker/taker status
- fill quality
- exit reason
- realized outcome
- PnL
- regime tags

## Do not implement in phase one

- stale contract approval logic from old guides
- blind copy-trading
- unlimited wallet approvals without monitoring
- LLM-directed live order placement
- solver-based multi-leg arbitrage without partial-fill controls
- near-resolution strategies without latency and tail-risk infrastructure

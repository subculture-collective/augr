---
title: Data Pipelines
created: 2026-06-08
tags: [data, pipelines, parquet, backtesting]
status: packaged
links:
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[AI Agent Architecture]]"
  - "[[Risk Controls and Guardrails]]"
---

# Data Pipelines

## Canonical data principle

Augr should separate raw venue data from normalized research objects.

```text
RawSource → RawArchive → Normalizer → FeatureStore → StrategyInput → Journal
```

Never let a strategy consume a live API response directly without a normalized schema and archival path.

## Polymarket data sources

### Market discovery

Use Gamma-style market/event metadata for:

- event title
- question
- market category
- token IDs
- resolution date
- outcomes
- volume
- liquidity
- status

### CLOB/order book

Use CLOB data for:

- best bid/ask
- spread
- midpoint
- depth
- open orders
- order placement
- cancellation
- fills

Use WebSocket streams for live order-book changes when execution latency matters.

### Data API/account data

Use user/trade/position endpoints for:

- account trades
- position tracking
- open positions
- closed PnL
- holders and activity

### On-chain validation

For backtests and post-trade analytics, prefer authoritative on-chain or venue fill records over heuristic public-feed reconstruction when possible.

## Polymarket external datasets and repos

The source set references:

- `warproxxx/poly_data` — historical trades and wallet analysis.
- `Jon-Becker/prediction-market-analysis` — Polymarket/Kalshi data collection and analysis framework.
- `Polymarket/py-clob-client` — official Python CLOB client.
- `Polymarket/agents` — agent-based prediction-market framework.
- `pmxt` — unified prediction-market API concept.
- `polyterm` and insider/wallet tracking tools — useful for research, but audit before execution.

## Stocks/options data sources

Needed for the options flow:

- underlying quote stream
- options chains
- bid/ask and sizes
- open interest
- historical underlying prices
- implied volatility
- realized volatility
- rates/dividends where relevant
- corporate action calendar
- earnings/event calendar
- broker positions and margin

## Storage schema

Recommended tables/events:

```text
markets
instruments
order_books
quotes
signals
order_intents
orders
fills
positions
risk_snapshots
strategy_runs
calibration_snapshots
external_evidence
```

## Trade journal

Every trade should journal:

- strategy ID
- market/instrument ID
- signal features
- fair value or probability
- executable price
- spread and depth
- expected EV
- Kelly suggestion
- final size and caps applied
- order type
- maker/taker status
- fill price
- exit reason
- realized PnL
- post-trade notes

This journal is the foundation for [[AI Agent Architecture]], regime filtering, backtesting, calibration, and risk reviews.

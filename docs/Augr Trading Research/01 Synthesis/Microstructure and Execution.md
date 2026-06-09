---
title: Microstructure and Execution
created: 2026-06-08
tags: [microstructure, clob, execution, latency]
status: packaged
links:
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[Risk Controls and Guardrails]]"
  - "[[Glossary]]"
---

# Microstructure and Execution

## Core principle

Small edges are execution-sensitive. A model with a 3% theoretical edge can become negative after spread, fees, slippage, latency, queue loss, and exit uncertainty.

Execution must be treated as a risk model, not just an API call.

## Maker vs taker

A recurring finding across the prediction-market sources is that maker/taker status is a major driver of realized results.

- **Maker**: posts liquidity, waits for fills, may collect spread or rewards.
- **Taker**: crosses the spread, gets immediate fill, pays for urgency.

For Augr:

```text
Default mode: maker-first
Taker mode: allowed only for time-sensitive, high-edge opportunities
Every fill: store maker/taker status
Every strategy report: show PnL split by maker/taker
```

## CLOB behavior

[[CLOB]] strategies need:

- best bid/ask
- midpoint
- spread
- order-book depth by price level
- fill probability
- queue position approximation
- cancellation and stale-order logic
- rate-limit handling
- partial-fill handling

## Queue priority

The sweeper-bot materials emphasize queue timing. If multiple bots place the same bid, the earlier order generally has higher fill priority. For near-resolution strategies, this can matter more than the model itself.

Augr should isolate queue-race strategies from slower fair-value strategies because they require different infrastructure and latency budgets.

## Near-resolution behavior

Near-resolution and sweeper strategies operate when outcome certainty is high and market prices have not fully converged to final payout.

Risks:

- last-second reversal
- wrong resolution source
- clock mismatch
- stale reference exchange price
- API delay
- order not actually live
- capital lockup at near-$1 prices

Recommended status: optional advanced lane after core Polymarket adapter and journal are stable.

## Copy trading latency

The copy-trading guides are useful for settings but risky as a strategy foundation. If a copied wallet's edge depends on fast fills, delayed copying may convert the follower into exit liquidity.

For Augr, wallet tracking is more useful for:

- market discovery
- category specialization inference
- whale activity alerts
- post-trade research

It should not automatically mirror trades unless latency and market impact are measured.

## Limit order discipline

Most source strategies prefer limit orders. Augr should enforce:

- max spread
- min depth
- max slippage
- post-only where maker status is required
- cancel-after timers
- stale quote cancellation
- no blind retry loops

## Options execution

For stocks/options, microstructure risks include:

- wide bid/ask spreads
- poor open interest
- hidden liquidity
- stale option quotes
- assignment risk
- hard-to-borrow issues
- margin changes
- volatility gaps

The options flow should use executable bid/ask prices, not midpoint-only backtests.

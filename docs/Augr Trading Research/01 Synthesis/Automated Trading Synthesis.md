---
title: Automated Trading Synthesis
created: 2026-06-08
tags: [automated-trading, augr, synthesis]
status: packaged
links:
  - "[[Core Theory Frameworks]]"
  - "[[Microstructure and Execution]]"
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[Augr Implementation Plan]]"
  - "[[Glossary]]"
---

# Automated Trading Synthesis

## Executive synthesis

The provided materials converge on one practical conclusion: the most useful trading automation is not a prediction machine. It is an **edge-processing machine**.

Augr should treat each trade as a sequence of measurable gates:

1. Estimate fair value or true probability.
2. Compare that estimate to executable market price, not display price.
3. Subtract spread, fees, slippage, queue risk, time decay, and exit uncertainty.
4. Size with fractional [[Kelly criterion]] or stricter portfolio caps.
5. Execute only when the net edge survives all controls.
6. Journal everything for calibration.
7. Disable strategies when the regime stops matching assumptions.

This framework applies directly to both [[Polymarket Flow]] and [[Stocks and Options Flow]]. The asset class changes, but the decision logic does not.

## Key insight clusters

### 1. Edge is usually structural, not mystical

Across Polymarket and options, the strongest ideas are structural:

- Longshot buyers systematically overpay for lottery-like outcomes.
- Makers often capture spread and behavioral flow.
- Near-resolution markets sometimes leave tiny deterministic yield for fast queues.
- Options markets encode uncertainty through implied volatility and Greeks.
- Directional opinions matter less than whether price is wrong enough to survive friction.

### 2. Execution quality can erase or create edge

Many community bot writeups describe impressive nominal signals. The more reliable lesson is that poor execution destroys small edges. Strategies with 1% to 5% expected edge cannot tolerate uncontrolled market orders, wide spreads, stale books, or slow copy-trading.

For Augr, every strategy should emit an `OrderIntent`, but a separate execution-risk layer should decide whether that intent is allowed to become an order.

### 3. Regime filters are first-class strategy components

Time-of-day, weekday/weekend behavior, liquidity conditions, volatility regime, category, and near-resolution timing can determine whether a signal works. The [[Blockchain Surfer]] source emphasizes that a losing strategy may be valid under the wrong regime rather than simply invalid.

Augr should store strategy performance by:

- asset or market category
- UTC hour
- weekday/weekend
- time to resolution or expiration
- liquidity band
- spread band
- volatility band
- order type
- maker/taker status

### 4. LLMs belong in research and control loops, not the hot execution path

The Hermes, Claude, and agent materials are useful for:

- summarizing journals
- proposing parameter changes
- generating code reviews
- classifying market descriptions
- researching news or external evidence
- creating playbooks and diagnostics

They are weaker as millisecond execution engines. Augr should allow LLMs to propose changes but not to silently modify live execution logic.

### 5. A shared platform beats two isolated bots

The two flows should share:

- canonical event model
- trade journal
- risk service
- backtest/replay engine
- signal registry
- strategy scheduler
- secrets management
- alerting and kill switches

Then each flow can specialize:

- [[Polymarket Flow]] specializes in CLOB, pUSD collateral, event metadata, order books, prediction-market probabilities, and settlement logic.
- [[Stocks and Options Flow]] specializes in option chains, Greeks, implied volatility, realized volatility, and broker/margin constraints.

## Trust hierarchy

When sources conflict, use this priority order:

1. Current official exchange/API documentation.
2. Peer-reviewed or academic/empirical research.
3. Public GitHub repositories with active maintenance and reproducible code.
4. Community articles, social threads, and trading anecdotes.

This matters because some uploaded guides describe legacy Polymarket wallet and collateral flows that may be stale. Augr should not embed old funding, approval, or contract assumptions without checking official docs at implementation time.

## Recommended architecture pattern

```text
MarketDataAdapter
  ↓
FeatureBuilder
  ↓
StrategySignal
  ↓
NetEdgeEvaluator
  ↓
RiskDecisionService
  ↓
ExecutionAdapter
  ↓
FillNormalizer
  ↓
PositionLedger
  ↓
Journal + CalibrationLoop
```

The same skeleton should support [[Polymarket Flow]] and [[Stocks and Options Flow]].

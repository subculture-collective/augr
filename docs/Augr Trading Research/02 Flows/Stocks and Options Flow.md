---
title: Stocks and Options Flow
created: 2026-06-08
tags: [stocks, options, black-scholes, greeks, augr]
status: packaged
links:
  - "[[Automated Trading Synthesis]]"
  - "[[Core Theory Frameworks]]"
  - "[[Risk Controls and Guardrails]]"
  - "[[Augr Implementation Plan]]"
  - "[[Glossary]]"
---

# Stocks and Options Flow

## Flow objective

Build a pricing-and-risk-first stocks/options module for Augr that can identify liquid, defined-risk opportunities from fair-value gaps and volatility mispricing.

## Core premise

The options flow should not start as a directional YOLO system. It should start as a fair-value, volatility, and Greeks engine.

The key question is:

```text
Is the option or spread mispriced enough, at executable prices, after transaction cost and risk constraints?
```

## Required modules

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

## Screening process

1. Filter liquid underlyings.
2. Fetch option chains.
3. Remove contracts with poor spread, low open interest, or stale quotes.
4. Calculate theoretical value and Greeks.
5. Compare implied volatility with realized volatility.
6. Construct defined-risk expressions.
7. Stress test position.
8. Submit order only if edge survives executable pricing.

## Preferred phase-one strategies

### Defined-risk vertical spreads

Good first instrument for controlled directional or probability views.

### Calendar spreads

Useful for term-structure mispricing, but requires stronger modeling.

### Volatility-screened premium selling

Use only with defined risk. Avoid naked short options in phase one.

### Fair-value debit trades

Only when theoretical edge exceeds spread and theta burden.

## Greek limits

Initial portfolio constraints:

```yaml
options:
  max_abs_delta_pct_equity: 0.20
  max_gamma_notional_pct_equity: 0.05
  max_vega_pct_equity: 0.10
  max_daily_theta_pct_equity: 0.02
  max_expiry_bucket_pct_equity: 0.20
  max_single_underlying_pct_equity: 0.10
```

## Option edge formula

```python
def option_edge(model_price, executable_price, commission=0.0, slippage=0.0):
    return model_price - executable_price - commission - slippage
```

## PnL attribution

Every closed trade should estimate:

- delta PnL
- gamma/convexity PnL
- vega PnL
- theta PnL
- execution/slippage PnL
- residual/model error

This tells Augr whether the strategy actually made money from its intended edge.

## Model-risk warning

[[Black-Scholes]] is a benchmark, not reality. It assumes cleaner behavior than markets provide. Augr must stress:

- volatility spikes
- jumps
- liquidity disappearance
- correlation shifts
- gap opens
- early assignment where applicable
- margin changes

## Do not implement in phase one

- naked short-volatility books
- uncontrolled same-week expiry gamma strategies
- trades sized from full Kelly
- midpoint-only backtests
- broker execution without hard risk checks
- LLM-placed options orders

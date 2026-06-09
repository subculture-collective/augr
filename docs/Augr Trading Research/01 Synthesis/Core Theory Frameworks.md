---
title: Core Theory Frameworks
created: 2026-06-08
tags: [theory, ev, kelly, bayes, markov, black-scholes]
status: packaged
links:
  - "[[Automated Trading Synthesis]]"
  - "[[Trading Primitives]]"
  - "[[Glossary]]"
---

# Core Theory Frameworks

## Expected value

Expected value is the core trade filter for both flows.

For a binary prediction market YES contract bought at price `q` with true probability estimate `p`:

```text
EV = p × (1 - q) - (1 - p) × q
```

A trade should pass only if net EV remains positive after spread, fee, slippage, exit haircut, and operational risk.

For options, the equivalent is:

```text
Edge = theoretical_value - executable_market_price - friction
```

## Kelly criterion

[[Kelly criterion]] sizes a trade from edge and odds. In production, Augr should use it as an upper bound, not a command.

Recommended production rule:

```text
final_size = min(
  fractional_kelly_size,
  liquidity_cap,
  strategy_cap,
  category_cap,
  daily_loss_cap_remaining,
  portfolio_risk_cap_remaining
)
```

Use quarter Kelly or smaller by default. Full Kelly is too aggressive for uncertain model estimates, correlated positions, liquidity gaps, and fat tails.

## Bayesian updating

Bayesian updating is useful when new information arrives and prior probability must move without panic or stubbornness.

```text
posterior = P(E|H) × P(H) / P(E)
```

In practice, use likelihood ratios:

```text
posterior_odds = prior_odds × likelihood_ratio
```

Use cases:

- macro event markets
- election and politics markets
- weather forecast updates
- options event risk
- news-driven repricing

## Markov chains

[[Markov chain]] models discretize state and estimate transition probabilities.

Example for Polymarket crypto Up/Down:

1. Bucket underlying price momentum or contract price into states.
2. Build a transition matrix from historical state changes.
3. Estimate persistence or next-state probability.
4. Trade only when model probability exceeds market-implied probability by a minimum edge.

Markov-based strategies in the source set repeatedly use a persistence threshold around `p(j*, j*) ≥ 0.87`. Treat this as a hypothesis to backtest, not a universal constant.

## Monte Carlo simulation

[[Monte Carlo]] converts uncertain future paths into estimated outcome probabilities.

Use cases:

- prediction-market event paths
- options payoff distributions
- portfolio drawdown simulation
- weather forecast uncertainty
- scenario stress testing

Monte Carlo is especially valuable when exact enumeration is hard but sampling is cheap.

## Black-Scholes-Merton

[[Black-Scholes]] is a fair-value anchor for European options. It prices uncertainty from:

- spot price
- strike
- time to expiration
- risk-free rate
- volatility

The key practical insight is not that Black-Scholes is perfectly true. It is that it gives Augr a disciplined benchmark for detecting option mispricing and decomposing risk through Greeks.

## Greeks

The stocks/options flow should track:

- [[Delta]] — directional exposure
- [[Gamma]] — delta convexity and rebalance risk
- [[Vega]] — implied-volatility exposure
- [[Theta]] — time decay

Greeks should become hard constraints in the [[Risk Controls and Guardrails]] layer.

## Game theory and maker/taker equilibrium

The prediction-market materials repeatedly frame markets as a maker/taker game:

- takers pay immediacy cost
- makers earn spread and queue priority
- emotional takers overpay for affirmative longshots
- patient makers harvest mispricing without necessarily forecasting better

For Augr, this means execution role is part of alpha. Strategy results should always record whether a trade was maker or taker.

## Bregman projection and Frank-Wolfe

The `$40M Math` source describes advanced arbitrage as a projection problem over an arbitrage-free manifold, using [[Bregman projection]] and [[Frank-Wolfe]] to make large constraint spaces tractable.

Recommended Augr status: **research lane, not phase one**.

Potential use cases:

- tournament markets
- mutually exclusive event groups
- dependent political/geographic markets
- cross-market logical consistency checks

Before live use, Augr needs:

- reliable market dependency classification
- solver infrastructure
- order-book-aware position sizing
- multi-leg execution controls
- partial-fill failure handling

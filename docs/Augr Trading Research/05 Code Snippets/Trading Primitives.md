---
title: Trading Primitives
created: 2026-06-08
tags: [code, snippets, trading, ev, kelly, options, polymarket]
status: packaged
links:
  - "[[Core Theory Frameworks]]"
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[Risk Controls and Guardrails]]"
---

# Trading Primitives

## Binary expected value

```python
def binary_ev(p_true: float, price: float) -> float:
    """Expected value for buying a binary contract at price."""
    return p_true * (1.0 - price) - (1.0 - p_true) * price
```

## Net Polymarket EV

```python
def polymarket_net_ev(
    p_true: float,
    ask: float,
    fee: float = 0.0,
    slippage: float = 0.0,
    exit_haircut: float = 0.0,
) -> float:
    gross = binary_ev(p_true, ask)
    return gross - fee - slippage - exit_haircut
```

## Fractional Kelly for binary contracts

```python
def fractional_kelly(price: float, p_true: float, fraction: float = 0.25) -> float:
    """Return fraction of bankroll to risk, capped at zero for no edge."""
    if price <= 0 or price >= 1:
        return 0.0
    b = (1.0 - price) / price
    q = 1.0 - p_true
    full_kelly = (p_true * b - q) / b
    return max(0.0, full_kelly * fraction)
```

## Production size cap

```python
def final_position_size(
    bankroll: float,
    kelly_fraction: float,
    max_position_pct: float,
    liquidity_cap_usd: float,
    strategy_cap_usd: float,
    daily_loss_cap_remaining: float,
) -> float:
    proposed = bankroll * kelly_fraction
    return max(0.0, min(
        proposed,
        bankroll * max_position_pct,
        liquidity_cap_usd,
        strategy_cap_usd,
        daily_loss_cap_remaining,
    ))
```

## Bayesian update

```python
def bayes_update(prior: float, p_e_given_h: float, p_e_given_not_h: float) -> float:
    numerator = p_e_given_h * prior
    denominator = numerator + p_e_given_not_h * (1.0 - prior)
    if denominator == 0:
        return prior
    return numerator / denominator
```

## Transition matrix

```python
import numpy as np

def build_transition_matrix(prices, n_states=10):
    states = np.clip((np.array(prices) * n_states).astype(int), 0, n_states - 1)
    matrix = np.zeros((n_states, n_states))
    for i in range(len(states) - 1):
        matrix[states[i], states[i + 1]] += 1
    row_sums = matrix.sum(axis=1, keepdims=True)
    row_sums[row_sums == 0] = 1
    return matrix / row_sums
```

## Markov entry gate

```python
def markov_should_enter(P, current_state, market_price, tau=0.87, min_gap=0.05):
    next_state = int(np.argmax(P[current_state]))
    p_hat = float(P[current_state][next_state])
    persistence = float(P[next_state][next_state])
    gap = p_hat - market_price
    return gap >= min_gap and persistence >= tau, {
        "next_state": next_state,
        "p_hat": p_hat,
        "persistence": persistence,
        "gap": gap,
    }
```

## Option edge

```python
def option_edge(model_price, executable_price, commission=0.0, slippage=0.0):
    return model_price - executable_price - commission - slippage
```

## Generic risk gate

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

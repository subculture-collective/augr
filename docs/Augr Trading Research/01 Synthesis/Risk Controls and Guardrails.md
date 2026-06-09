---
title: Risk Controls and Guardrails
created: 2026-06-08
tags: [risk, guardrails, security, capital]
status: packaged
links:
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[AI Agent Architecture]]"
  - "[[Blocking Questions and Challenges]]"
---

# Risk Controls and Guardrails

## Risk philosophy

Augr should assume every model is wrong sometimes, every API fails eventually, and every strategy enters hostile regimes periodically.

Risk control should be separate from strategy logic.

## Capital controls

Minimum controls:

- max position size by strategy
- max position size by market/instrument
- max aggregate exposure by category
- max daily loss
- max weekly loss
- max drawdown before global pause
- max correlated exposure
- fractional Kelly cap
- liquidity-based cap

## Execution controls

- max spread
- minimum depth
- max slippage
- order timeout
- stale quote check
- partial-fill handling
- cancel-on-disconnect
- post-only enforcement for maker strategies
- no retry loops without price refresh

## Regime controls

Pause or disable when:

- consecutive losses exceed threshold
- rolling win rate drops below floor
- fill rate collapses
- slippage exceeds baseline
- volatility exceeds calibrated range
- liquidity disappears
- API latency spikes
- market data source conflicts
- external event invalidates model assumption

## Strategy inversion checks

The source materials suggest that some consistently losing strategies may be inverted if losses cluster under stable conditions.

Augr should support inversion research, but not automatic live inversion. Required checks:

- sample size threshold
- stable loss clustering
- opposite-side liquidity
- no asymmetric tail risk
- replay validation

## Polymarket-specific risks

- stale API or documentation assumptions
- wrong collateral/token flow
- wallet/private key exposure
- wrong resolution source
- near-resolution reversal
- queue loss
- CLOB partial fills
- fees/rewards changing by category
- on-chain settlement delays
- market cancellation or ambiguity

## Options-specific risks

- theta bleed
- implied volatility crush
- jump risk
- gamma risk near expiry
- assignment/pin risk
- margin call
- liquidity gap
- exercise/settlement mismatch
- corporate actions
- model miscalibration

## Security controls

- dedicated trading wallets/accounts
- minimal funded balances
- no unlimited approvals unless justified and monitored
- dependency audit
- lockfile review
- no secrets in logs or prompts
- read-only research mode by default
- separate paper/live environments
- revoke unused approvals
- restricted CI/CD secret access

## Kill switches

Implement:

```text
GLOBAL_KILL_SWITCH
STRATEGY_KILL_SWITCH
VENUE_KILL_SWITCH
ASSET_CATEGORY_KILL_SWITCH
WALLET_KILL_SWITCH
```

Every live process should check the kill-switch state before creating or modifying orders.

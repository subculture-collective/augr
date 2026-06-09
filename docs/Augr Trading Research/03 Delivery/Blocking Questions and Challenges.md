---
title: Blocking Questions and Challenges
created: 2026-06-08
tags: [questions, risks, integration, augr]
status: packaged
links:
  - "[[Augr Implementation Plan]]"
  - "[[Risk Controls and Guardrails]]"
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
---

# Blocking Questions and Challenges

## Repository and architecture

1. What is Augr's current runtime and framework?
2. Does Augr already have market data adapters?
3. Does Augr already have a trade journal or ledger?
4. Does Augr already support paper trading?
5. What database is used, and is it suitable for time-series/order-book data?
6. Is there a queue/event bus?
7. Are there existing abstractions for strategies, orders, fills, and positions?

## Stocks/options flow

1. Which broker will Augr use?
2. Are options market-data entitlements available?
3. Are real-time Greeks provided, or must Augr calculate them?
4. What margin model applies?
5. What option strategies are permitted by the account?
6. How will assignment and exercise events be handled?
7. What compliance constraints apply to automated options trading?
8. Are short options allowed? If yes, under what caps?

## Polymarket flow

1. Which wallet model will Augr use?
2. How will secrets/private keys be stored?
3. What collateral flow is current at implementation time?
4. Which official Polymarket client libraries are stable enough?
5. What are current rate limits and fee schedules?
6. What are the exact contract addresses at deployment time?
7. Can Augr safely detect market resolution and cancellation states?
8. What latency class can the infrastructure realistically support?

## Data and backtesting

1. Does Augr need historical Polymarket data locally?
2. How much order-book history is required for strategy validation?
3. Can options backtests use quote-level bid/ask data?
4. How will survivorship bias be avoided?
5. How will market closures, cancellations, and ambiguous resolutions be represented?

## Security

1. Who can deploy live strategy configs?
2. Who can change risk caps?
3. Where are secrets stored?
4. Are dependencies audited?
5. How are wallet approvals monitored and revoked?
6. Are paper and live environments strictly separated?

## Operational risks

1. What happens on API outage?
2. What happens on websocket disconnect?
3. What happens on partial fill?
4. What happens if price moves between signal and order placement?
5. What happens if the risk service is unavailable?
6. What happens if the journal write fails?
7. What happens if a strategy loops or floods orders?

## Tail-risk scenarios

- sudden volatility spike
- market/event cancellation
- wrong oracle or resolution source
- correlated losses across strategies
- model probability overconfidence
- liquidity vanishing
- wallet compromise
- broker margin change
- option assignment surprise
- near-resolution reversal
- stale official-doc assumptions

## Integration challenge summary

The biggest challenge is not coding a single strategy. It is building trustworthy infrastructure so that strategies can fail without destroying the system.

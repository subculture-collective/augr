---
title: Stretch Features Roadmap
created: 2026-06-08
tags: [roadmap, stretch, features, augr]
status: packaged
links:
  - "[[Augr Implementation Plan]]"
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[AI Agent Architecture]]"
---

# Stretch Features Roadmap

## Near-term stretch features

### Category and regime scheduler

**Value:** High. Enables/disables strategies by hour, weekday/weekend, market category, spread, depth, and volatility regime.

**Feasibility:** Medium. Requires journaled historical performance and scheduling controls.

### Maker-quality scorecard

**Value:** High for Polymarket. Measures whether maker-first strategies actually capture spread after adverse selection and queue loss.

**Feasibility:** Medium.

### Agent-generated daily research brief

**Value:** High. Converts journal data into actionable review notes.

**Feasibility:** Low-to-medium, if read-only.

### Source calibration lab

**Value:** High for weather/event markets.

**Feasibility:** Medium. Requires forecast snapshots and resolved outcomes.

## Medium-term features

### Cross-flow risk cockpit

Unify stocks/options and Polymarket into one capital-at-risk view.

Metrics:

- total capital at risk
- expected shortfall
- max loss by strategy
- correlation assumptions
- strategy health
- venue health

### Wallet intelligence dashboard

Track smart wallets and whales for discovery, not automatic copying.

Features:

- category specialization
- win rate by category
- entry/exit timing
- average hold time
- copy-lag simulation

### Options volatility surface explorer

Visualize skew, term structure, IV rank, realized vol, and theoretical mispricing.

## Long-term/high-effort features

### Solver-based combinatorial arbitrage

Use dependency classification, integer programming, and projection methods to find complex arbitrage across logically related markets.

**Value:** Potentially high.

**Feasibility:** High effort. Needs robust dependency classification and multi-leg execution risk control.

### Near-resolution latency engine

Specialized infrastructure for sweeper and near-resolution strategies.

Needs:

- reference feed
- precise clock sync
- queue-aware orders
- fast cancellation
- tiny tail-risk budgets

### Delta-hedged options volatility engine

Use dynamic hedging to trade volatility rather than direction.

Needs:

- very reliable broker execution
- transaction-cost model
- gamma/vega stress testing
- strong risk automation

## Features to avoid until mature

- LLM-only trade decisions
- automatic live config mutation
- unlimited wallet approvals
- naked short options
- full-Kelly sizing
- copy-trading without latency simulation
- midpoint-only options backtests

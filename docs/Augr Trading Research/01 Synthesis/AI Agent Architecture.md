---
title: AI Agent Architecture
created: 2026-06-08
tags: [ai, llm, agents, hermes, claude, augr]
status: packaged
links:
  - "[[Data Pipelines]]"
  - "[[Risk Controls and Guardrails]]"
  - "[[Augr Implementation Plan]]"
---

# AI Agent Architecture

## Role of agents in Augr

The materials about Hermes, Claude, and self-learning bots are most useful as a blueprint for **research automation** and **adaptive review**, not unmanaged live execution.

Recommended use cases:

- summarize daily trade journal
- detect strategy drift
- explain drawdowns
- propose parameter changes
- classify market text and dependencies
- generate research notes
- search source materials
- prepare code diffs for review
- maintain Obsidian documentation

Avoid placing LLMs directly in the hot path for:

- millisecond order execution
- wallet/private key handling
- blind live code mutation
- final risk approval
- unsupervised size escalation

## Safe self-learning loop

```text
Trade journal closes day
  ↓
Agent reviews journal and metrics
  ↓
Agent proposes parameter changes
  ↓
Backtest/replay validates changes
  ↓
Risk policy checks changes
  ↓
Human or deterministic gate approves
  ↓
Next session uses approved config
```

## Agent outputs should be structured

Agents should return JSON or Markdown with required fields:

```json
{
  "strategy_id": "weather_calibrated_v1",
  "change_type": "parameter_adjustment",
  "proposed_change": {"min_ev": 0.07},
  "evidence": ["losses clustered below EV 0.07"],
  "backtest_required": true,
  "risk_impact": "reduces trade count, increases selectivity",
  "approval_required": true
}
```

## Agent memory

The Hermes materials emphasize persistent memory and reusable skills. For Augr, keep memory explicit:

- strategy playbooks
- known failure modes
- market-specific quirks
- validated source priorities
- current API assumptions
- risk policy history

## RAG/research pipeline

Useful retrieval sources:

- official API docs
- source markdown files
- trade journals
- backtest reports
- strategy configs
- issue trackers and code diffs
- academic papers

Agent retrieval should label source quality and recency.

## Governance

Agent output can inform; it should not silently decide.

Recommended gates:

- no private key visibility
- no production config writes without approval
- no size cap increases without backtest and review
- no enabling disabled strategy without risk review
- no changes to wallet, broker, or contract addresses without official-doc verification

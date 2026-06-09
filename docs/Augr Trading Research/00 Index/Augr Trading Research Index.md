---
title: Augr Trading Research Index
created: 2026-06-08
tags: [augr, trading, index, obsidian]
status: packaged
links:
  - "[[Automated Trading Synthesis]]"
  - "[[Core Theory Frameworks]]"
  - "[[Microstructure and Execution]]"
  - "[[Strategy Catalog]]"
  - "[[Data Pipelines]]"
  - "[[AI Agent Architecture]]"
  - "[[Risk Controls and Guardrails]]"
  - "[[Polymarket Flow]]"
  - "[[Stocks and Options Flow]]"
  - "[[Augr Implementation Plan]]"
  - "[[Blocking Questions and Challenges]]"
  - "[[Stretch Features Roadmap]]"
  - "[[Glossary]]"
  - "[[Source Map]]"
---

# Augr Trading Research Index

## Navigation

### Executive notes

- [[Automated Trading Synthesis]] — the top-level synthesis across all provided materials.
- [[Core Theory Frameworks]] — EV, Kelly, Bayes, Markov chains, Monte Carlo, Black-Scholes, Bregman projection, Frank-Wolfe, and game theory.
- [[Microstructure and Execution]] — CLOB behavior, maker/taker edge, limit orders, queue priority, latency, and near-resolution behavior.
- [[Strategy Catalog]] — strategy families from the source set, organized by usefulness to Augr.
- [[Risk Controls and Guardrails]] — capital, strategy, venue, model, operational, and security controls.

### Flow-specific notes

- [[Polymarket Flow]] — prediction-market execution, strategy design, walleting, CLOB data, and Polymarket-specific risks.
- [[Stocks and Options Flow]] — options fair-value pricing, volatility modeling, Greeks, and defined-risk trading design.

### Delivery

- [[Augr Implementation Plan]] — integrated plan for Augr with separate stocks/options and Polymarket tracks.
- [[Blocking Questions and Challenges]] — unresolved questions and risks before integration.
- [[Stretch Features Roadmap]] — optional enhancements and future roadmap.

### Reference

- [[Glossary]]
- [[Source Map]]
- [[Trading Primitives]]

## Core thesis

The durable edge across the materials is not “one magic model.” It is a production loop:

```text
Data → Fair value / probability → Net edge → Risk sizing → Execution quality → Journal → Calibration → Regime control
```

The Augr system should become a shared trading platform with two distinct live strategy flows:

1. **Stocks/options** — fair value, volatility, Greeks, and defined-risk expressions.
2. **Polymarket** — prediction-market microstructure, calibrated probability, maker-first execution, and event-specific data pipelines.

Both flows should share common services: [[Data Pipelines]], [[Risk Controls and Guardrails]], [[Trading Primitives]], a canonical trade journal, and deterministic execution controls.

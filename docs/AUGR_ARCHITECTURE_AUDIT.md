# Augr architecture

This is a maintained map of the implemented system, not a qualification claim.

## System boundary

Augr is a Go service with a React operator UI. PostgreSQL/TimescaleDB is the
canonical store for application state and evidence; Redis is used for cache and
runtime coordination. `cmd/tradingagent` owns process assembly. Most business
logic lives under `internal`, while SQL persistence implementations live under
`internal/repository/postgres`.

## Request and execution flow

1. Authenticated REST or CLI requests enter `internal/api`, or the scheduler
   selects a due strategy or automation job.
2. Runtime wiring in `cmd/tradingagent/runtime.go` resolves the canonical
   account, providers, repositories, safety controls, and execution adapters.
3. The strategy runner gathers observations, runs analyst and debate phases,
   creates a plan, and obtains a final risk-controlled signal.
4. The execution layer applies hard gates before routing to paper or an
   explicitly authorized venue adapter.
5. Runs, source observations, decisions, orders, trades, positions, ledger
   events, and audit records are persisted independently.
6. Reconciliation and projection jobs compare internal state with upstream
   venues and refresh operator read models.

Smoke mode uses a deterministic runner. Other environments wire the real
runner for both scheduled and authenticated manual execution.

## Major components

| Component | Primary location | Responsibility |
| --- | --- | --- |
| API and auth | `internal/api` | REST routes, JWT/API keys, WebSockets, validation |
| Runtime assembly | `cmd/tradingagent` | provider, scheduler, broker, and repository wiring |
| Strategy pipeline | `internal/agent` | analysis, debate, planning, model calls, run state |
| Data layer | `internal/data` | provider registry, rate limits, cache, history, options |
| Automation | `internal/automation` | scans, refreshes, reconciliation, reviews, reports |
| Execution | `internal/execution` | order managers and paper/venue adapters |
| Risk | `internal/risk` | kill switches, circuit breaker, sizing and exposure gates |
| Portfolio | `internal/portfolio` | opportunity selection and allocator behavior |
| Persistence | `internal/repository/postgres` | PostgreSQL repositories and transactional contracts |
| UI | `web/src` | authenticated operator workflows and readbacks |

## Providers and venues

The registry includes Polygon, Alpha Vantage, Finnhub, Financial Modeling Prep,
Yahoo, Alpaca, Tradier, Binance, Kalshi, Polymarket, NewsAPI, Stocktwits,
Reddit, and Bluesky-oriented adapters. Availability is configuration- and
capability-specific; the presence of an adapter does not mean a deployment has
credentials, entitlement, fresh data, or execution authorization.

LLM adapters include OpenCode, OpenAI, Anthropic, Google, OpenRouter, xAI, and
an Ollama-compatible endpoint. Model selection and fallback are configuration,
not hard-coded promises about provider availability.

## Safety model

- Live trading defaults off and requires account, environment, venue, and
  runtime authorization to agree.
- Global and market kill switches, circuit breakers, risk limits, and broker
  modes can stop execution after a strategy has produced a signal.
- Canonical account binding scopes reads and writes. Paper-scored and
  paper-stress evidence are intentionally distinct.
- Source events and financial postings are append-oriented so later
  normalization does not erase provenance.
- Scheduler and reconciliation failures remain visible as job outcomes; process
  health cannot substitute for them.

## Data and schema

Migrations under `migrations/` are the schema source of truth. They cover the
application model, historical observations, provider governance, accounts,
financial ledgers, instrument identity, strategy evidence, and operational
read models. The runtime refuses to start against the wrong schema version.

Database contracts with isolation, locking, migration, or accounting semantics
belong in the integration tier and must run against a disposable migrated
database.

## Operational evidence

The repository contains repeatable verification and recovery tooling, but
generated evidence is stored outside the source tree. A release is not
qualified by unit tests, container health, scheduler registration, or a manual
trigger alone. Natural scheduled observations, provider coverage,
reconciliation, rollback evidence, and the applicable soak ledger remain
separate operational gates.

See [Runbooks](runbooks/README.md) for procedures and [Testing](testing.md) for
the executable evidence tiers.

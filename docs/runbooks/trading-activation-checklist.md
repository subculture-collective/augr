---
title: "Trading activation checklist"
description: "Ordered operator steps that turn a deployed Augr instance from healthy-but-idle into one that produces paper runs and paper orders."
status: "canonical"
updated: "2026-09-23"
tags: [operations, activation, paper, scheduler, discovery]
---

# Trading activation checklist

A healthy process, healthy containers, and a full automation schedule do not
produce trades on their own. On 2026-09-23 the nuc deployment had all three
and still had no pipeline run in 30 days. This checklist lists every gate that
had to be opened, in order, with the evidence that confirms each one.

Run every SQL statement against the database named in the container's
`APP_DATABASE_URL`, not the older `tradingagent` database in the same
container. Print environment values by name only; `APP_DATABASE_URL` and
`POLYMARKET_PASSPHRASE` contain secrets that a key/secret/token grep misses.

## 1. Deploy a release that contains the preflight fixes

Confirm the running commit against `main`:

```sh
docker exec augr-app-1 env | grep -E '^APP_(VERSION|BUILD_COMMIT)='
git log --oneline <build-commit>..origin/main | wc -l
```

If the count is non-zero, follow [Rolling restart](rolling-restart.md) after
migrations. The fixes shipped after release `1022b401` include the SPY ETF
fundamentals contract, preflight rejection alerting, `/readyz`, allocator
ownership changes, and the order reconciliation job.

## 2. Set the environment gates

Add or correct these keys in the deployed `.env`, then recreate the app
container. Names only; values live in the secret store.

| Key | Required value | Why |
| --- | --- | --- |
| `PORTFOLIO_ALLOCATOR_MODE` | `paper` | Unset means shadow: the allocator records decisions and never submits, and promoted strategies are diverted away from direct execution. |
| `DISCOVERY_EVALUATION_SCOPE_ID` | UUID of a persisted paper evaluation scope | Unset marks every strategy-creating job unavailable at startup. See step 4. |
| `LLM_FALLBACK_PROVIDER` | a provider other than `LLM_DEFAULT_PROVIDER` | `opencode` falling back to `opencode` retries the same broken backend. |
| `LLM_CALL_TIMEOUT`, `LLM_DEBATE_TIMEOUT` | at most `5m` | A full run is 21 to 23 sequential calls; 30-minute timeouts exceed the 2-hour job budget. |
| `ADMIN_API_KEY` | set | Kill-switch deactivation and breaker reset return 503 without it. |
| `KALSHI_DEMO` | `false` for real market data | The demo host's first page is zero-volume markets; the screener rejects all of them. Live orders still need `KALSHI_API_KEY_ID` and `KALSHI_PRIVATE_KEY_PEM_B64`. |
| `ENABLE_POLYMARKET_AUTOMATION` | `true` only if Polymarket is wanted | The compose file previously hard-coded `false`. |
| `BINANCE_API_KEY`, `BINANCE_API_SECRET` | testnet-issued keys while `BINANCE_PAPER_MODE=true` | Paper mode targets `testnet.binance.vision`; production keys fail every signed call. |

Verify after restart:

```sh
docker exec augr-app-1 wget -qO- http://127.0.0.1:8080/readyz
docker logs augr-app-1 2>&1 | grep -E 'allocator in shadow mode|discovery jobs unavailable|ADMIN_API_KEY unset'
```

The readiness call must return 200 and the grep must print nothing.

## 3. Replace the corporate-fundamentals SPY strategy

The existing strategy `canonical-cutover-stock` is rejected at 10:00 ET every
weekday with reason `fundamentals_incomplete` because it requires three of
five corporate metrics that no provider returns for an ETF. Follow
[SPY ETF evidence](spy-etf-evidence.md): create a new paper SPY strategy whose
config sets `fundamentals_contract` to `spy-ssga-etf-v1`, keep the
`canonical_signal_selection` block from the old strategy, and pause the old
one. The scheduler now reloads schedules every minute, so no restart is
needed.

Confirm on the next weekday after 10:00 ET:

```sql
SELECT created_at, event_kind, metadata->>'reason_code'
FROM agent_events
WHERE event_kind LIKE 'strategy.preparation%'
ORDER BY created_at DESC LIMIT 5;
SELECT status, signal, started_at FROM pipeline_runs ORDER BY started_at DESC LIMIT 5;
```

A pipeline run row must exist. A rejection now also logs at WARN, increments
the preparation-rejected metric, and sends an alert.

## 4. Seed discovery readiness

Strategy discovery, overnight backtests, generation, and options discovery
stay unavailable until all of these exist for the canonical account:

- one `dataset_manifests` row with a `dataset_quality_results` row, built per
  [Dataset evidence](dataset-evidence.md);
- one `paper_evaluation_scopes` row binding that manifest and quality result to
  the account's capital binding;
- `DISCOVERY_EVALUATION_SCOPE_ID` set to that scope.

Confirm:

```sql
SELECT count(*) FROM dataset_manifests;
SELECT count(*) FROM paper_evaluation_scopes;
```

and `GET /api/v1/automation/status` shows `discovery_run`, `ticker_discovery`,
`overnight_backtest`, and `overnight_generate` as scheduled rather than
unavailable.

## 5. Confirm the order path end to end

After the first BUY signal:

```sql
SELECT id, status, broker, external_id, created_at FROM orders ORDER BY created_at DESC LIMIT 5;
SELECT job_name, status, started_at FROM automation_job_runs
WHERE job_name = 'order_reconcile' ORDER BY started_at DESC LIMIT 3;
```

An Alpaca paper order first appears as `submitted`; the `order_reconcile` job
resolves it to `filled` or `cancelled` within five minutes. Compare with the
Alpaca paper account's order list.

## 6. Rotate secrets that were printed

During the 2026-09-23 audit two secret values were echoed into an agent
transcript: `POLYMARKET_PASSPHRASE` and the database password embedded in
`APP_DATABASE_URL` (role `augr_app_runtime`). Rotate both in the secret store,
update `.env`, and recreate the app container. Rotating the database password
requires `ALTER ROLE augr_app_runtime PASSWORD ...` on `augr-postgres-1` before
the restart.

## 7. Items that still need venue verification

These cannot be confirmed from this repository and must be checked against
current venue documentation before any live enablement:

- Kalshi order creation body and endpoint ([Kalshi live readiness](kalshi-live-readiness.md)).
- Polymarket `api.polymarket.us` authentication scheme and order state names.
- Alpaca options approval level for the paper account (level 3 was confirmed
  on 2026-09-23).

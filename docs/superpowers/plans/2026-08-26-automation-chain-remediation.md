# Automation Chain Remediation Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore truthful current-market and overnight automation state while preserving historical evidence and fail-closed discovery scope requirements.

**Architecture:** Separate current-market ticker scope from nightly history scope. Represent dependency skips as blocked, reuse degraded outcomes for partial overnight coverage, and omit discovery deployment until immutable dataset/scope execution exists. Production cleanup pauses only proven stale strategies and keeps all rows.

**Tech Stack:** Go, PostgreSQL, React/TypeScript, Vitest, Docker Compose.

---

### Task 1: Restore current-market scope

**Files:**
- Modify: `internal/automation/jobs_market.go`
- Modify: `internal/automation/jobs_overnight.go`
- Modify: `internal/automation/operational_tickers.go`
- Test: `internal/automation/jobs_market_test.go`
- Test: `internal/automation/jobs_overnight_schedule_test.go`
- Test: `internal/automation/operational_tickers_test.go`

- [ ] **Step 1:** Add a failing test proving the shared selector accepts a caller limit; current refresh selects open stock positions, active stock strategies, and at most 50 watchlist names while history/deep may pass 250.
- [ ] **Step 2:** Run `go test ./internal/automation -run 'TestCurrentData|TestOperational'` and require the new test to fail.
- [ ] **Step 3:** Add `const currentDataWatchlistLimit = 50` and pass it to operational selection only from `currentDataRefresh`; keep `HistoryRefreshWatchlistLimit` in history/deep.
- [ ] **Step 4:** Update `historyRefresh` to pass `HistoryRefreshWatchlistLimit` explicitly and test a non-default configured limit.
- [ ] **Step 5:** Add deterministic fake-provider timing proving the current scope's batch count completes before the next hot cadence and hot receives the exact persisted fresh selection.
- [ ] **Step 6:** Run `go test -race ./internal/automation`.

### Task 2: Represent dependency blocking truthfully

**Files:**
- Modify: `internal/api/automation_handlers.go`
- Modify: `internal/automation/orchestrator.go`
- Modify: `internal/repository/postgres/job_run.go`
- Modify: `web/src/features/automation/automationCutover.ts`
- Modify: `web/src/features/automation/AutomationPage.tsx`
- Modify: `web/src/features/automation/AutomationDetailPage.tsx`
- Modify: `web/src/shared/api/schemas.ts`
- Modify: `web/src/shared/types/domain.ts`
- Test: `internal/api/automation_handlers_test.go`
- Test: `web/src/App.test.tsx`

- [ ] **Step 1:** Persist dependency-skip reason in `JobRun.Detail` and add restart-hydration tests for `skipped` with a retained failure streak.
- [ ] **Step 2:** Add backend/frontend tests where latest result is `skipped` with detail `dependency current_data_refresh still running` and consecutive failures is five; expected state is blocked, not failing or healthy.
- [ ] **Step 3:** Add `blocked_jobs` to automation health and make `healthy=false` when blocked.
- [ ] **Step 4:** Add frontend `blocked` operational state and display the persisted dependency reason with warning styling.
- [ ] **Step 5:** Run `go test ./internal/api` and `npm test -- --run` from `web/`.

### Task 3: Degrade partial overnight sweeps

**Files:**
- Modify: `internal/automation/jobs_overnight.go`
- Test: `internal/automation/jobs_overnight_schedule_test.go`
- Test: `internal/automation/orchestrator_test.go`

- [ ] **Step 1:** Add completion-policy tests for 105/107 degraded, below-80% error, zero-output error, and 100% success.
- [ ] **Step 2:** Count supported strategies before per-strategy work and write `coverage_bps` to the summary.
- [ ] **Step 3:** Return `Degradedf` for >=80% partial findings; retain true errors for systemic failures and low coverage.
- [ ] **Step 4:** Prove degraded overnight sweep satisfies downstream dependency checks.
- [ ] **Step 5:** Run `go test -race ./internal/automation`.

### Task 4: Gate discovery deployment readiness

**Files:**
- Modify: `internal/automation/orchestrator.go`
- Modify: `internal/automation/jobs_premarket.go`
- Modify: `internal/automation/jobs_overnight.go`
- Modify: `internal/automation/overnight_backtest_chunker.go`
- Modify: `internal/repository/postgres/overnight_backtest_run.go`
- Modify: `internal/repository/postgres/paper_evaluation_scope.go`
- Modify: `internal/api/discovery_handlers.go`
- Modify: `internal/api/automation_handlers.go`
- Modify: `internal/api/server.go`
- Modify: `cmd/tradingagent/runtime.go`
- Test: `internal/automation/jobs_premarket_test.go`
- Test: `internal/automation/jobs_overnight_schedule_test.go`
- Test: `internal/automation/overnight_backtest_chunker_test.go`
- Test: `internal/repository/postgres/overnight_backtest_run_test.go`
- Test: `internal/api/discovery_handlers_test.go`
- Test: `internal/api/automation_handlers_test.go`
- Test: `internal/repository/postgres/paper_evaluation_scope_test.go`
- Test: `cmd/tradingagent/runtime_test.go`

- [ ] **Step 1:** Add an authoritative read-only readiness method that returns false with `historical data loader is not bound to the scope's immutable dataset manifest`; prove that a valid scope row alone cannot make it true.
- [ ] **Step 2:** Add registration tests requiring readiness=true and each job's normal dependencies; gate `discovery_run`, `ticker_discovery`, `overnight_backtest`, `overnight_generate`, and `options_discovery` atomically while leaving history/sweep and event-market jobs registered.
- [ ] **Step 3:** Evaluate readiness during runtime assembly; do not add an unsafe config override or silently force dry-run.
- [ ] **Step 4:** When readiness is false, terminally reconcile active overnight backtest checkpoints with the stable reason before job registration. Test idempotency and the exact running-to-failed transition.
- [ ] **Step 4a:** Replace generic checkpoint updates with terminal-monotonic `SaveIfRunning`; zero updated rows return a closed-run sentinel and never rewrite reconciliation.
- [ ] **Step 4b:** Commit prepared strategy create/reuse effects and checkpoint completed/done transition in one row-locked PostgreSQL transaction. Test both race orders, rollback, reuse, and zero partial effects.
- [ ] **Step 5:** Expose omitted job names/reason through automation health `unavailable_jobs`; do not include them in runnable status.
- [ ] **Step 6:** Gate `POST /discovery/run` with a typed immutable-binding lock: return 423 only for that type; empty/unknown false or evaluation error returns 503 before any discovery call.
- [ ] **Step 7:** Run focused runtime/automation/API tests and verify all five jobs are absent in production-like deps while Kalshi/Polymarket discovery remains.

### Task 5: Validate and release

**Files:**
- Modify only files required by release checks.

- [ ] **Step 1:** Run `go test -race ./...`.
- [ ] **Step 2:** Run `golangci-lint run`, `go vet ./...`, and `git diff --check`.
- [ ] **Step 3:** Run `npm test -- --run`, `npm run lint`, and `npm run build` from `web/`.
- [ ] **Step 4:** Commit focused changes and run `APP_DATABASE_URL=validation-placeholder ./scripts/release-gate.sh` on the clean exact commit.
- [ ] **Step 5:** Capture a timestamped custom-format production dump under `/srv/repos/patrickfanella/augr/backups/`, verify `pg_restore --list`, and record predeployment schema/count/fingerprint evidence.
- [ ] **Step 6:** Transfer the exact commit by Git bundle, build immutable app/web images, and deploy only after zero running pipeline/automation rows; do not remove/recreate PostgreSQL or its volume.

### Task 6: Production cleanup and canaries

**Files:**
- No source changes expected.

- [ ] **Step 1:** Re-read and verify active paper stock IDs `3577efc8-3807-46c3-a16e-6e320d36c366` (`IPOD`) and `ce09b84c-e603-4b20-81b5-aa38b4e951f1` (`WTO`), then call authenticated `POST /api/v1/strategies/{id}/pause`; verify no other strategy row changed.
- [ ] **Step 2:** Trigger `universe_refresh`, wait for its durable terminal row, and inspect refreshed active/watchlist counts.
- [ ] **Step 3:** In a valid market window trigger current refresh, wait for its durable success/degraded row, then trigger hot and wait for same-cycle completion.
- [ ] **Step 4:** Require same-day history refresh success/degraded before triggering overnight sweep; wait for the sweep terminal row.
- [ ] **Step 5:** Compare postdeployment schema/count/fingerprint evidence to baseline; verify exact images/revisions, schema 107 clean, health, safety controls, temp keys zero, source clean, and retained backup.

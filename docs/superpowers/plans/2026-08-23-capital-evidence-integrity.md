# Capital Evidence Integrity Implementation Plan

> **For agentic workers:** Execute task-by-task. Use a fresh subagent per task, review each result, then continue. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make risk, portfolio, and promotion surfaces fail closed until backed by account-scoped, reconciled, point-in-time valuation evidence.

**Architecture:** Legacy data stays immutable and quarantined. New evidence uses existing accounts, canonical instruments, ledger marks, execution lifecycle, and rebuildable projections. Migrate all consumers to one scoped projection; do not retain multiple authoritative formulas.

**Tech Stack:** Go, PostgreSQL, React/TypeScript, Vitest.

---

## Current production status

- 10 open Kalshi positions (opened 2026-07-26 through 2026-07-28); 0 have either `current_price` or `unrealized_pnl`.
- Decision journal: stock 0 approved / 4 rejected (2026-08-06 through 2026-08-17); Kalshi 13 approved / 171 rejected. The stock warning is historical journal data, not current stock exposure proof.
- One `paper_scored/default` promotion-evidence account exists. Legacy positions, trades, orders, decisions, backtests, and reports lack its `account_id`.
- Account capital flows and canonical ledger records are scoped. Legacy portfolio/P&L is not promotion evidence.

## Files

- `internal/risk/status_projection.go`, `internal/api/risk_cockpit_handlers.go` — active risk vs historical journal.
- `internal/api/portfolio_handlers.go`, `internal/repository/postgres/position.go` — remove incomplete legacy calculations.
- `internal/api/portfolio_allocator_handlers.go`, `internal/portfolio/diagnostics.go` — effective evidence eligibility.
- `internal/data/kalshi/provider.go`, `internal/execution/kalshi/snapshot.go`, `internal/ledger/mark.go`, `internal/repository/postgres/projection.go` — source-qualified marks.
- `internal/automation/orchestrator.go`, new `internal/automation/jobs_kalshi_marking.go` — mark scheduling.
- `internal/automation/report_worker.go`, `internal/repository/postgres/{report_artifact,backtest_config,backtest_run}.go` — paper evidence scope.
- `web/src/features/{risk,portfolio,cockpit,overhaul}/` — unavailable/reason-coded state.

### Task 1: Contain misleading output

**Files:** modify `internal/risk/status_projection.go`, `internal/api/risk_cockpit_handlers.go`, `internal/api/portfolio_allocator_handlers.go`, `internal/portfolio/diagnostics.go`, Risk/Portfolio/Cockpit UI; test `internal/risk/cockpit_test.go` and API tests.

- [ ] Write failing test: four historical rejected stock decisions yield no active warning, but render a historical count.

```go
require.Empty(t, got.ActiveWarnings)
require.Equal(t, 4, got.HistoricalDecisionCounts[domain.MarketTypeStock].Rejected)
```

- [ ] Query current decisions by explicit operating window and account scope. Pass historical counts separately. Only current decisions can produce active warnings; never delete journal rows.
- [ ] Make promotion effective eligibility fail closed:

```go
return profile.PromotionEligible && input.ResultsIsolated && input.MarkCoverageComplete && input.ReconciliationPassed && !input.ResultsStale
```

- [ ] Return machine reason codes: `legacy_unscoped`, `marks_incomplete`, `marks_stale`, `reconciliation_missing`. UI shows historical rejections as informational, not active risk.
- [ ] Run `go test ./internal/risk ./internal/api ./internal/portfolio` and focused web tests. Commit `fix: separate historical risk evidence`.

### Task 2: Scope all new paper evaluation evidence

**Files:** create `migrations/000106_paper_evaluation_scopes.{up,down}.sql`; modify report/backtest repositories, report worker, report handlers; add migration/repository/API tests.

- [ ] Write migration tests: legacy null scope survives; same strategy in two accounts cannot collide; invalid account/evidence combinations reject.
- [ ] Create immutable scope parent:

```sql
CREATE TABLE paper_evaluation_scopes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES accounts(id),
  manifest_sha256 text NOT NULL,
  quality_sha256 text NOT NULL,
  simulation_policy_sha256 text NOT NULL,
  capital_policy_sha256 text NOT NULL,
  evaluation_start timestamptz NOT NULL,
  evaluation_end timestamptz NOT NULL CHECK (evaluation_end > evaluation_start),
  canonical_bytes bytea NOT NULL,
  canonical_sha256 text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);
```

- [ ] Add nullable `scope_id` FKs to legacy config/run/report tables. Null is permanently labelled `legacy_unscoped`; do not assign old rows to the default account.
- [ ] Propagate one scope `config -> run -> report artifact`; reject missing/mismatched scope. Replace completed-artifact upsert with idempotent insert accepting byte-identical retry only.
- [ ] Require account/scope on promotion-facing report reads; allow legacy only through an explicit legacy endpoint/filter.
- [ ] Test cross-account reads, hash mismatch, scope mismatch, byte-identical retry, and attempted mutation. Run repository/automation/API tests. Commit `feat: scope paper evaluation evidence`.

### Task 3: Bind new execution data to canonical identity

**Files:** modify `internal/domain/position.go`, financial lifecycle/settlement repositories and new migration; test lifecycle and prediction settlement.

- [ ] Write tests that reject new execution evidence without immutable account, instrument, venue contract, side, lot/cost basis, and source-event identity.
- [ ] Add additive links only for new lifecycle/ledger records. Do not rewrite legacy positions or infer market type from current strategy rows.
- [ ] Make canonical settlement atomic with catalog result evidence, cash, fees/rebates, and effective/observed time. Preserve existing Kalshi result authority and idempotency gate.
- [ ] Test failed identity resolution cannot mark, close a lot, or produce promotion evidence. Run `go test ./internal/execution/prediction ./internal/repository/postgres`. Commit `feat: bind execution evidence to accounts`.

### Task 4: Add conservative Kalshi marking

**Files:** create `internal/automation/jobs_kalshi_marking.go` and test; modify provider, snapshot, ledger marks, projection repo, and orchestrator.

- [ ] Test side conventions: long YES -> executable YES bid; long NO -> executable NO bid. Missing, zero, crossed, stale, halted, or identity-mismatched books yield unavailable reasons and no price.
- [ ] Implement typed input:

```go
type KalshiMarkInput struct {
 AccountID uuid.UUID; InstrumentID uuid.UUID; VenueContractID uuid.UUID
 Side domain.PositionSide; Ticker string; Quote kalshi.Snapshot
 ObservedAt time.Time; MaxAge time.Duration
}
```

- [ ] Validate identity, status, side, bid, currency, and freshness; emit `ledger.MarkObservation` with raw quote and convention metadata.
- [ ] Persist with `ProjectionRepository.RecordMarkObservation` and rebuild the as-of projection. Do not update legacy `positions.current_price` as a shortcut.
- [ ] Register a separate job after snapshot/catalog availability. Keep reconciliation read-only and settlement catalog-result based.
- [ ] Test freshness, idempotency, conflicts, all sides, and job failure isolation. Run Kalshi/ledger/projection/automation tests. Commit `feat: record conservative Kalshi marks`.

### Task 5: Replace portfolio/risk calculations together

**Files:** modify `internal/api/portfolio_handlers.go`, risk projection/handler, legacy position repository, and Portfolio/Risk/Cockpit/Capital UI; add API/integration/UI tests.

- [ ] Test one account-scoped as-of projection: exact lots/cost/fee/settlement totals, absolute notional with multiplier, one snapshot boundary, no cross-account rows, and null P&L on failed gates.
- [ ] Add response contract:

```go
type PortfolioValuation struct {
 AccountID uuid.UUID; AsOf time.Time; MarkCoverageComplete bool
 ReconciliationPassed bool; TotalPnL, UnrealizedPnL, RealizedPnL *decimal.Decimal
 UnavailableReasons []string
}
```

- [ ] Remove authority from newest-100 pagination, entry-price substitution, partial-open realized-P/L omission, and separate count/value reads. Read from one repeatable-read/as-of projection.
- [ ] Migrate every UI surface at once. Display one `as_of` and identical unavailable reasons; legacy data gets an explicitly historical tab only.
- [ ] Run API/risk/repository tests, then `cd web && npm test -- --run && npm run build`. Commit `feat: project account scoped portfolio value`.

### Task 6: Controlled cutover and promotion gate

**Files:** diagnostics/promotion/evaluation readers, `web/src/features/overhaul/OverhaulPage.tsx`, new runbook, test suites.

- [ ] Build read-only cutover report: scoped canonical lots, quarantined legacy rows, fresh/stale/unavailable marks, reconciliation, and promotion-block reasons.
- [ ] Before reader cutover require: zero scope mismatches, zero missing canonical links, complete fresh marks, and successful account-level venue/ledger reconciliation.
- [ ] Route promotion only to immutable scored-account evidence. Never merge legacy rows by date, `is_paper`, strategy, or default-account assumption.
- [ ] Document alerts: scope mismatch, stale/missing marks, reconciliation mismatch -> P&L unavailable + promotion blocked.
- [ ] Run `go test ./internal/...`, web tests/build, and `./scripts/release-gate.sh`. Commit `feat: gate promotion on reconciled evidence`.

## Backfill rule

Backfill only rows with a complete proof chain: one account, canonical instrument/contract, immutable source evidence, dataset/policy hashes. Record original ID, migration version, proof digest, and time. Everything else remains `legacy_unscoped` forever.

## Acceptance criteria

1. Historical rejections never create active exposure warnings.
2. P&L is fully scoped, fresh, cost-complete, reconciled, reproducible — or null with reasons.
3. New paper reports cannot cross account/evidence boundaries or mutate after completion.
4. Kalshi marks are side-aware, source-qualified, immutable, and fresh.
5. Risk, Portfolio, Cockpit, and Capital agree on one as-of projection.
6. Legacy history is never promotion/profitability evidence.

# Canonical account cutover implementation plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Bind all operational paper execution to the configured canonical account, move account reads to server-enforced routes, and cut over to a fresh PostgreSQL database without copying strategies or operational history.

**Architecture:** Use two new immutable migrations. Exercise the 107 to 108 to 109 compatibility sequence only in disposable rehearsal databases. Production gets a fresh database initialized at migration metadata version 0 and migrated from 0 through 109; the old `tradingagent` database receives no migration, deployment, canary write, or other mutation beyond approved safety controls and shutdown finalization. Runtime v1 binds the account from `PROJECTION_ACCOUNT_ID` before it constructs any scoped writer. Raw venue evidence commits first. A transaction coordinator then commits the accepted fill, common lifecycle, source event link, canonical instrument facts, economic normalization, ledger payload, and a durable projection-outbox row. The runtime pool writes each mark-observation batch and its outbox row in one transaction. A worker claims and updates outbox work through the runtime pool. The projection pool only reads ledger, instrument, and mark inputs and calls `persist_canonical_projection_checkpoint`; it cannot insert marks or mutate outbox rows.

**Tech Stack:** Go 1.25.8+, PostgreSQL 17/TimescaleDB, pgx v5, golang-migrate, React 19, TypeScript, TanStack Query, Zod, Vitest, MSW, Docker Compose, and Bash.

---

## Boundaries

- Do not edit an applied migration. Add migrations 108 and 109.
- Do not enable write guards or add `NOT NULL` constraints in migration 108.
- Do not backfill legacy operational rows. Existing `NULL account_id` rows remain read-only and absent from canonical APIs.
- Do not add account columns that migration 88 already owns. `copy_subscriptions.origin_type`, `copy_trade_intents.origin_type`, and their UUID `origin_id` columns remain unchanged.
- Do not copy strategies, runs, decisions, orders, positions, trades, reports, copy records, or other operational history to the new database.
- Keep `tradingagent` migration-free. The only permitted pre-cutover writes are existing safety controls that disable automation and execution admission, plus terminal-state writes made by work already running during SIGTERM shutdown. Rollback retargets exactly four DB variables and uses `deploy/docker-compose.nuc.rollback.yml`.
- Never deploy a bridge, exact-108 binary, migration 108, migration 109, or writer canary against the old production `tradingagent` database. Compatibility work runs only against disposable databases created by `scripts/verify-account-cutover.sh`.
- Keep live execution disabled. The v1 execution account must be active and either `paper_scored` or `paper_stress`.
- Do not put account scope in `context.Context`.
- Do not add a standalone fills API or page. This plan does not add a fill handler, route, endpoint function, query key, or frontend route.
- Do not add an authorization migration in v1. The optional auth schema would be its own migration after 109, but the fixed-account decision below does not need one.
- Every task ends in a compiling commit. Do not split an interface change from its callers.
- Use `PROJECTION_ACCOUNT_ID` as the fixed v1 account for both reads and writes. Do not add `EXECUTION_ACCOUNT_ID` or another required environment key.
- Run every NUC database command through `docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER"`. Connect over the local PostgreSQL container socket. Do not use a host DSN, TLS, `docker run`, a second Compose tools service, or ad hoc migration mounts.

## Task 0: land the reviewed plan before implementation

**Files:**
- Modify: `docs/superpowers/plans/2026-08-27-canonical-account-cutover.md`
- Modify: `.gitignore`

- [ ] Finish review of this plan before Task 1 starts. Run `git diff --check`.
- [ ] Before any clean-tree assertion, add the exact line `.blacktower/handoff.md` to the committed `.gitignore`. Keep the existing `.blacktower/deepwork/` entries. Run `grep -qxF '.blacktower/handoff.md' .gitignore`, `git check-ignore -q .blacktower/handoff.md`, and `git check-ignore -q .blacktower/deepwork/`.
- [ ] Prove no Blacktower artifact is tracked: `test -z "$(git ls-files .blacktower)"`. Prove the only intended edits are the ignore rule and this untracked plan: `test "$( { git diff --name-only; git ls-files --others --exclude-standard; } | sort -u)" = "$(printf '%s\n' .gitignore docs/superpowers/plans/2026-08-27-canonical-account-cutover.md | sort)"`. Prove ignored handoff state does not enter status: `test -z "$(git ls-files --others --exclude-standard .blacktower/handoff.md)"`.
- [ ] Commit the ignore rule and reviewed plan together: `git add .gitignore docs/superpowers/plans/2026-08-27-canonical-account-cutover.md && git commit -m 'docs(plan): resolve cutover blockers'`. Immediately require `test -z "$(git status --porcelain=v1 --untracked-files=all)"` before Task 1.
- [ ] Do not run a clean-tree gate yet. The exact clean-tree gate runs after all implementation, plan, verifier, and release-tool commits in Task 14.

## Disposable compatibility rehearsal

The current runtime requires an exact schema match at version 107. It rejects version 108 as `ahead`. Rehearse this sequence only in disposable databases. Do not deploy any artifact in this sequence to production and do not point any step at `tradingagent`:

1. Create a disposable schema-107 database and run the Task 3 bridge binary against it. The bridge accepts only versions 107 and 108 and keeps the schema-107 write shape.
2. Apply migration 108 to that disposable database. Exercise one old-shape paper fixture and prove that every new expansion column stays `NULL`.
3. Run the Task 3 exact-108 schema-gate binary. It rejects 107 and 109.
4. Run Tasks 4 through 9 writer fixtures against disposable schema 108. Every fixture writes complete account scope while migration-109 guards do not exist.
5. Run the Task 10 enforcement-ready binary, which accepts versions 108 and 109, against the disposable schema-108 database.
6. Pause fixture dispatch, drain fixture work, run the old-writer fixture against 108 once more, and apply migration 109 to the disposable database.
7. Run the Task 10 exact-109 binary against that disposable database. Production uses this exact-109 release artifact only after the fresh production database has initialized `schema_migrations` at 0 and migrated from 0 through 109.

The bridge does not make arbitrary ahead schemas valid. Implement an explicit accepted set:

```go
type SchemaCompatibility struct {
	Minimum int
	Maximum int
}

func (c SchemaCompatibility) Accepts(version int) bool {
	return version >= c.Minimum && version <= c.Maximum
}
```

## Phase gates

1. **Phase A:** Tasks 1 through 10 pass in source tests and disposable databases. The exact-109 artifact is ready, and every operational writer produces one account graph.
2. **Phase B:** Tasks 11 and 12 pass. Every operational API and frontend request uses the fixed server account.
3. **Phase C:** Tasks 13 and 14 are committed. The working tree is clean before `scripts/release-gate.sh` runs. The updater, migration runner, verifier, runbook, pinned app and web images, and release-gate wiring all come from the exact Phase-C commit.
4. **Phase D:** An operator records approval before any production command in Tasks 15 through 17 runs. No command mutates `tradingagent`.
5. **Phase E:** The fresh database passes owner, role, signing, seed, zero-history, and canary checks before the four environment values change.

## Phase A: schema and scoped writers

### Task 1: Add a valid execution scope

**Files:**
- Create: `internal/execution/scope.go`
- Create: `internal/execution/scope_test.go`
- Create: `internal/domain/pipeline_ref.go`
- Create: `internal/domain/pipeline_ref_test.go`
- Modify: `internal/domain/pipeline.go`
- Modify: `internal/domain/agent.go`
- Modify: `internal/domain/events.go`
- Modify: `internal/domain/trade_decision.go`
- Modify: `internal/domain/order.go`
- Modify: `internal/domain/position.go`
- Modify: `internal/domain/trade.go`
- Modify: `internal/domain/opportunity.go`
- Modify: `internal/domain/allocation_decision.go`
- Modify: `internal/domain/replay.go`
- Modify: `internal/domain/copy_trading.go`

- [ ] Add failing constructor and accessor tests. Cover a strategy pipeline run, a copy-origin rebalance run with no strategy, portfolio rebalance, risk reduction, operator, settlement, and reconciliation.
- [ ] Reuse `ledger.ExecutionOriginType` and its existing constants from `internal/ledger/economic_normalization.go`. Do not create a second origin enum.
- [ ] Put `PipelineRunRef` in `internal/domain/pipeline_ref.go`, not `internal/execution` or `internal/repository`. Both execution and repository packages may import `internal/domain`; neither imports the other for this value. Keep `ExecutionScope` fields private so callers cannot construct contradictory combinations. Do not use pointer UUID fields.

```go
type PipelineRunRef struct {
	ID        uuid.UUID
	TradeDate time.Time
}

type ExecutionScope struct {
	accountID       uuid.UUID
	environment     domain.AccountEnvironment
	originType      ledger.ExecutionOriginType
	originID        string
	pipelineRun     domain.PipelineRunRef
	hasPipelineRun  bool
	copyOriginRunID uuid.UUID
}

func NewStrategyExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, strategyVersionID uuid.UUID, run domain.PipelineRunRef) (ExecutionScope, error)
func NewCopyExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, subscriptionID, copyOriginRunID uuid.UUID) (ExecutionScope, error)
func NewNonRunExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, originType ledger.ExecutionOriginType, originID string) (ExecutionScope, error)
func (s ExecutionScope) AccountID() uuid.UUID
func (s ExecutionScope) Environment() domain.AccountEnvironment
func (s ExecutionScope) Origin() (ledger.ExecutionOriginType, string)
func (s ExecutionScope) PipelineRun() (domain.PipelineRunRef, bool)
func (s ExecutionScope) CopyOriginRunID() uuid.UUID
```

- [ ] Require a nonzero strategy-version UUID and complete `(id, trade_date)` for strategy scope. Require nonzero subscription and `copy_origin_rebalance_runs.id` for copy scope. Reject a pipeline run on copy scope and reject a copy run on strategy scope.
- [ ] For non-run work, require the existing non-strategy origin types. Produce scheduled operator, settlement, and reconciliation IDs with `economicid.DeterministicUUID(domain, components...)`; never call `uuid.New()` for retry identity.
- [ ] Add nullable account and scope fields only to domain records that decode legacy `NULL` rows. Constructors for new records accept `ExecutionScope` and always expose nonzero account identity.
- [ ] Run `go test ./internal/execution ./internal/domain ./internal/ledger ./internal/economicid`.
- [ ] Commit: `git add internal/execution/scope.go internal/execution/scope_test.go internal/domain/pipeline_ref.go internal/domain/pipeline_ref_test.go internal/domain/pipeline.go internal/domain/agent.go internal/domain/events.go internal/domain/trade_decision.go internal/domain/order.go internal/domain/position.go internal/domain/trade.go internal/domain/opportunity.go internal/domain/allocation_decision.go internal/domain/replay.go internal/domain/copy_trading.go && git commit -m 'feat(execution): define canonical scope'`.

### Task 2: Bind the configured execution account and environment

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/validate.go`
- Modify: `internal/config/validate_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `.env.example`
- Modify: `scripts/verify-prod-build.sh`
- Modify: `cmd/tradingagent/prod_build_verification_test.go`

- [ ] Rename the internal config concept to `CanonicalAccountID string`, but load it from the existing `PROJECTION_ACCOUNT_ID`. Do not read or document `EXECUTION_ACCOUNT_ID`. Parse `PROJECTION_ACCOUNT_ID` once during runtime construction and use the result for projection reads and every operational write.
- [ ] Change `.env.example` to describe `PROJECTION_ACCOUNT_ID` as the required fixed v1 account for canonical reads and writes. Add `PROJECTION_ACCOUNT_ID=00000000-0000-4000-8000-000000000064` to the isolated app environment generated by `scripts/verify-prod-build.sh`. Update `cmd/tradingagent/prod_build_verification_test.go` to require that exact line. This proves a fresh migrated seed starts without a new environment key.
- [ ] Load the account before constructing the scheduler, WebSocket hub, recorder, projection worker, broker, `OrderManager`, copy executor, portfolio processor, settlement worker, restart reconciler, risk worker, or any account-scoped PostgreSQL repository. Require an active `paper_scored` or `paper_stress` account and a matching paper-evaluation profile.
- [ ] Store the validated account ID and `domain.AccountEnvironment` in one immutable runtime dependency bundle. Constructors for stock, options, Kalshi, Polymarket, copy, settlement, restart, risk, projection, and automation receive that bundle. They do not reread environment variables and do not infer account or environment from a row created by the caller.
- [ ] Test missing, malformed, inactive, live, environment-mismatched, and valid account configuration. Tests prove that no scoped repository or worker constructor runs after a binding failure.
- [ ] Run `go test ./internal/config ./cmd/tradingagent -run 'Test.*ExecutionAccount'` and `go build ./cmd/tradingagent`.
- [ ] Commit: `git add .env.example scripts/verify-prod-build.sh cmd/tradingagent/prod_build_verification_test.go internal/config/config.go internal/config/validate.go internal/config/validate_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go && git commit -m 'feat(runtime): bind canonical account'`.

### Task 3: Build the rehearsal bridge and nullable expansion

**Files:**
- Create: `migrations/000108_canonical_account_expansion.up.sql`
- Create: `migrations/000108_canonical_account_expansion.down.sql`
- Create: `migrations/000108_canonical_account_expansion_test.go`
- Modify: `internal/repository/postgres/schema_version.go`
- Modify: `internal/repository/postgres/schema_version_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `cmd/tradingagent/schema_version_sync_test.go`
- Modify: `cmd/augr-economic/main.go`
- Modify: `cmd/augr-evidence/main.go`

- [ ] Change all three binaries from exact equality to an explicit compatibility range. The rehearsal bridge artifact accepts 107 and 108 only. Tests reject 106 and 109. Never deploy this bridge to production.
- [ ] Add nullable `account_id` and `environment` to operational tables. Add nullable `pipeline_run_trade_date DATE` beside every `pipeline_run_id` that participates in the canonical graph. Add nullable `strategies.execution_strategy_version_id UUID REFERENCES strategy_versions(id) ON DELETE RESTRICT` for the fresh-strategy binding in Task 4.
- [ ] Add nullable `origin_type` and `origin_id` only where those columns do not exist. For new text origin IDs, use `TEXT`. Do not alter migration-88 UUID origins.

```sql
ALTER TABLE orders
	ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
	ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live')),
	ADD COLUMN origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
	ADD COLUMN origin_id TEXT,
	ADD COLUMN pipeline_run_trade_date DATE,
	ADD COLUMN copy_origin_rebalance_run_id UUID REFERENCES copy_origin_rebalance_runs(id) ON DELETE RESTRICT;

ALTER TABLE copy_subscriptions
	ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
	ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live'));

ALTER TABLE copy_trade_intents
	ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
	ADD COLUMN environment TEXT CHECK (environment IN ('paper_scored','paper_stress','shadow','live'));

ALTER TABLE execution_intents
	ADD COLUMN copy_origin_rebalance_run_id UUID REFERENCES copy_origin_rebalance_runs(id) ON DELETE RESTRICT;

ALTER TABLE execution_orders
	ADD COLUMN copy_origin_rebalance_run_id UUID REFERENCES copy_origin_rebalance_runs(id) ON DELETE RESTRICT;
```

- [ ] Scope `pipeline_runs`, `pipeline_run_snapshots`, `agent_decisions`, `agent_events`, `trade_decisions`, `orders`, `positions`, `trades`, `portfolio_opportunities`, `allocation_decisions`, `replay_events`, `financial_fill_idempotency`, and `prediction_settlement_idempotency`.
- [ ] Scope `copy_subscriptions`, `copy_trade_intents`, `copy_origin_rebalance_runs`, `copy_origin_rebalance_intents`, `copy_target_drift_runs`, and `copy_target_drift_legs`. Child evidence stores `account_id` where a direct account filter is needed and validates it against its parent in migration 109.
- [ ] Preserve `copy_subscriptions.origin_type`, `copy_subscriptions.origin_id UUID`, `copy_trade_intents.origin_type`, `copy_trade_intents.origin_id UUID`, and the origin fields on `copy_origin_rebalance_runs`. Keep `origin_id = subscription_id` and all append-only evidence guards.
- [ ] Add nullable `copy_origin_rebalance_run_id` to both common `execution_intents` and `execution_orders`. Keep the migration-71 append-only triggers installed. Migration 109 requires this column exactly when `origin_type='copy_subscription'`, joins it to `copy_origin_rebalance_runs.id`, requires the run and common row to have the same `account_id`, and requires `execution_intents.origin_id = copy_origin_rebalance_runs.subscription_id::text`. It also requires `execution_orders.copy_origin_rebalance_run_id = execution_intents.copy_origin_rebalance_run_id` through the order's `intent_id`. Every other origin type requires both common columns to be `NULL`.
- [ ] Add partial account-first indexes with `WHERE account_id IS NOT NULL`. Do not create an enforcement table, trigger, guard function, `NOT NULL` constraint, or data update.
- [ ] Replace migration 77's whole-row implementation of `strategy_legacy_snapshot_sha(UUID)` before adding `strategies.execution_strategy_version_id`. The replacement hashes a JSON object built from the explicit pre-108 columns `id`, `name`, `description`, `ticker`, `market_type`, `schedule_cron`, `config`, `is_active`, `is_paper`, `created_at`, `updated_at`, `status`, `skip_next_run`, and `active_thesis`, with fixed keys and values. It must not use `to_jsonb(s)` or `s.*`. Add the new execution-version column only after the function replacement so adding that column cannot change an existing legacy snapshot hash. The migration test records a hash on schema 107, applies 108, and proves the hash is unchanged.
- [ ] Create `account_projection_outbox` in migration 108 with `id`, `account_id`, `request_kind`, `through_transaction_id`, `as_of`, `mark_as_of`, `mark_generation`, mark-source fields, `status`, `attempt_count`, `next_attempt_at`, `last_error_code`, `claimed_at`, `claimed_by`, `claim_expires_at`, `completed_at`, `created_at`, and `updated_at`. `request_kind` is `economic_fill` or `mark_rebuild`. Make `mark_generation UUID NOT NULL`. Require absent `mark_as_of` and `00000000-0000-0000-0000-000000000000` for `economic_fill`; require non-NULL `mark_as_of` and a nonzero generation for `mark_rebuild`. Create `uq_account_projection_outbox_request` on `(account_id, request_kind, through_transaction_id, mark_generation)` and `idx_account_projection_outbox_claimable` on `(status, next_attempt_at, claim_expires_at, created_at, id)`. This permits repeated mark rebuilds at one ledger frontier while retrying the same generation idempotently.
- [ ] Seed the reviewed `capital-margin-policy-v1` artifact and its deterministic `account_capital_policy_bindings` row for the existing migration-64 account `00000000-0000-4000-8000-000000000064`. Use `capital_margin_policy_v1_canonical_bytes`, `digest`, and `economic_deterministic_uuid`; do not hard-code a digest or a second account. Use plain `INSERT`, not `ON CONFLICT`, so migration 108 fails rather than claiming a pre-existing artifact or binding. The binding copies `paper_scored`, `100000`, `2`, `reg_t`, `promotion_evidence`, `paper_scored/default`, and `USD` from the account. The migration test proves exactly one active seeded account and one matching profile on a fresh database.
- [ ] Name the outbox validation function `validate_account_projection_outbox_row()` and its trigger `trg_validate_account_projection_outbox_row`. Make the down migration lock every altered table, `account_projection_outbox`, `account_capital_policy_bindings`, and `capital_margin_policy_artifacts` in `ACCESS EXCLUSIVE` mode before any check. Compute `seed_bytes := capital_margin_policy_v1_canonical_bytes(reviewed_json)`, `seed_sha := encode(digest(seed_bytes,'sha256'),'hex')`, `seed_version := 'capital-margin-policy-v1@sha256:' || seed_sha`, `seed_artifact_id := economic_deterministic_uuid('capital-margin-policy-artifact',seed_version)`, and `seed_binding_id := economic_deterministic_uuid('capital-policy-binding','00000000-0000-4000-8000-000000000064',seed_version)` inside one `DO` block. Require exactly one artifact with that ID, version, digest, canonical bytes, and canonical JSON. Require exactly one binding with that ID, account, artifact, version, `tier=100000`, `margin_profile='reg_t'`, `environment='paper_scored'`, `starting_capital=100000`, `buying_power_multiplier=2`, `evidence_class='promotion_evidence'`, `storage_namespace='paper_scored/default'`, and `currency='USD'`. Require no other binding to reference the artifact. Any missing or mismatched row aborts down.
- [ ] After those locked seed checks, require `account_projection_outbox` to be empty and reject every non-NULL expansion column. Then run `ALTER TABLE account_capital_policy_bindings DISABLE TRIGGER trg_account_capital_policy_bindings_immutable` and `ALTER TABLE capital_margin_policy_artifacts DISABLE TRIGGER trg_capital_margin_policy_artifacts_immutable` as `augr_db_owner`. Delete the exact binding ID first and require `ROW_COUNT=1`; delete the exact artifact ID second and require `ROW_COUNT=1`; then re-enable both triggers before dropping migration-108 objects. A failure rolls back the trigger state and deletes. Drop `trg_validate_account_projection_outbox_row`, `validate_account_projection_outbox_row()`, `idx_account_projection_outbox_claimable`, and `uq_account_projection_outbox_request`; drop `account_projection_outbox`; restore the schema-69 `validate_canonical_projection_checkpoint()` body; and drop only migration-108 expansion indexes and columns. Migration 108 seeds no outbox row.
- [ ] Add a disposable `108 -> 107 -> 108` migration test. It proves an empty outbox and the exact seeded capital rows roll down and up, a modified or missing seed row blocks down, a second binding to the seeded artifact blocks down, immutable triggers remain enabled after a failed down, a single outbox row blocks down, all migration-108 functions and indexes disappear at 107, and the second up recreates exactly one seed pair and an empty outbox with the same constraints.
- [ ] Test schema 107 with legacy copy and pipeline graphs, apply 108, run old-shape inserts, and prove owned origin values are byte-for-byte unchanged and all expansion fields remain `NULL`.
- [ ] Run `go test ./migrations ./internal/repository/postgres ./cmd/tradingagent ./cmd/augr-economic ./cmd/augr-evidence` and `go build ./cmd/tradingagent ./cmd/augr-economic ./cmd/augr-evidence`.
- [ ] Commit: `git add migrations/000108_canonical_account_expansion.up.sql migrations/000108_canonical_account_expansion.down.sql migrations/000108_canonical_account_expansion_test.go internal/repository/postgres/schema_version.go internal/repository/postgres/schema_version_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/tradingagent/schema_version_sync_test.go cmd/augr-economic/main.go cmd/augr-evidence/main.go && git commit -m 'feat(db): add compatible account expansion'`.
- [ ] In a disposable schema-107 database, run this commit's bridge binary, apply 108, and run `go test ./migrations -run TestCanonicalAccountExpansionOldWriterCanary -count=1`. Reject a DSN whose database name is `tradingagent`.
- [ ] In a second commit, set the minimum and maximum schema version to 108 in `internal/repository/postgres/schema_version.go`, `cmd/tradingagent/runtime.go`, `cmd/augr-economic/main.go`, and `cmd/augr-evidence/main.go`. Update their listed tests to reject 107 and 109. Run the same Task 3 tests and builds.
- [ ] Commit the exact-108 rehearsal gate before Task 4: `git add internal/repository/postgres/schema_version.go internal/repository/postgres/schema_version_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/tradingagent/schema_version_sync_test.go cmd/augr-economic/main.go cmd/augr-evidence/main.go && git commit -m 'chore(db): require expansion schema'`. Do not deploy it to production.

### Task 4: Create and resolve execution strategy versions

**Files:**
- Modify: `internal/domain/strategy.go`
- Modify: `internal/strategycatalog/family.go`
- Modify: `internal/strategycatalog/family_test.go`
- Modify: `internal/strategycatalog/version.go`
- Modify: `internal/strategycatalog/version_test.go`
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/repository/postgres/strategy.go`
- Modify: `internal/repository/postgres/strategy_test.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `internal/api/strategy_handlers.go`
- Modify: `internal/discovery/deploy.go`
- Modify: `internal/discovery/deploy_test.go`
- Modify: `internal/discovery/orchestrator_test.go`
- Modify: `internal/discovery/options/orchestrator_test.go`
- Modify: `internal/kalshidiscovery/orchestrator_test.go`
- Modify: `internal/polymarketdiscovery/orchestrator_test.go`
- Modify: `internal/scheduler/scheduler_test.go`
- Modify: `internal/automation/orchestrator_test.go`
- Modify: `internal/automation/alpaca_reconciliation_test.go`
- Modify: `internal/automation/jobs_portfolio_allocator_test.go`
- Modify: `internal/automation/report_worker_test.go`
- Modify: `internal/api/backtest_comparison_test.go`
- Modify: `internal/api/backtest_handlers_test.go`
- Modify: `internal/service/backtest_scaffold_test.go`
- Modify: `internal/api/portfolio_allocator_handlers_test.go`
- Modify: `internal/api/event_market_handlers_test.go`
- Modify: `internal/api/kalshi_handlers_test.go`
- Modify: `internal/copytrading/service_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `cmd/tradingagent/prod_strategy_runner_test.go`

- [ ] Add `ExecutionStrategyVersionID *uuid.UUID` to `domain.Strategy` so schema-107 rows still decode. New schema-108 paper strategies must return a non-nil value.
- [ ] Replace `StrategyRepository.Create` with the exact contract `CreateWithExecutionVersion(ctx context.Context, strategy *domain.Strategy) (uuid.UUID, error)` and add `ResolveExecutionVersionID(ctx context.Context, strategyID uuid.UUID) (uuid.UUID, error)`. Require a caller-supplied strategy UUID. `PostgresStrategyRepository.CreateWithExecutionVersion` owns one transaction: insert the validated strategy snapshot, query `strategy_legacy_snapshot_sha($1)` from that inserted row, construct the family and version from the persisted snapshot inside the repository method, insert the immutable family and version, update `strategies.execution_strategy_version_id`, and commit. Return the real `strategy_versions.id`. Any failure rolls back all four writes.
- [ ] Resolve with one joined query over `strategies`, `strategy_versions`, and `strategy_families`. Require the family ID to equal the deterministic ID for slug `legacy-{strategy UUID}`. Change `StrategyRepository.Update` to create and bind a new immutable version from the post-update snapshot in the same transaction; never mutate an old version.
- [ ] `handleCreateStrategy` validates the request, assigns the strategy UUID, and calls `CreateWithExecutionVersion`. It does not construct a family or version. Inside the repository transaction, use `legacy-{strategy UUID}` as the family slug. Map stock to `instrument.AssetClassEquity`, crypto to `instrument.AssetClassCryptoSpot`, options to `instrument.AssetClassOption`, and Kalshi and Polymarket to `instrument.AssetClassPredictionContract`. Use the transaction's `strategy_legacy_snapshot_sha(strategy.id)` result for both `SourceCommit` and `SourceTreeSHA256`, `legacy-runtime-v1` as `CompilerKind` and `CompilerVersion`, `legacy-strategy-config-v1` as `ConfigSchema`, the persisted config as `Config`, and `agent-pipeline-v1` as `DecisionContract`. Stock and crypto require `dataset.KindBars`; options requires `dataset.KindBars` and `dataset.KindOptionChains`; Kalshi and Polymarket require `dataset.KindPredictionBooks`, `dataset.KindPredictionRules`, and `dataset.KindResolutions`.
- [ ] Keep `POST /api/v1/strategies` global research CRUD, but require this endpoint to create the execution version for every new paper strategy. Test each market mapping, transaction rollback, deterministic retry conflict, and the returned `execution_strategy_version_id` in `internal/api/server_test.go` and `internal/repository/postgres/strategy_test.go`.
- [ ] Change `internal/discovery/deploy.go:CreateOrReusePaperStrategy` to call `CreateWithExecutionVersion`. Existing reused strategies must resolve a valid matching binding before return. Update `internal/discovery/deploy_test.go` and every discovery repository fake. API create and discovery create now share the same transaction-backed creation path.
- [ ] Update every `StrategyRepository` implementer and embedded fake in the same commit. The grep-discovered implementation files are `internal/repository/postgres/strategy.go`, `internal/api/server_test.go`, `internal/api/backtest_comparison_test.go`, `internal/api/portfolio_allocator_handlers_test.go`, `internal/api/event_market_handlers_test.go`, `internal/api/kalshi_handlers_test.go`, `internal/automation/alpaca_reconciliation_test.go`, `internal/automation/jobs_portfolio_allocator_test.go`, `internal/automation/orchestrator_test.go`, `internal/automation/report_worker_test.go`, `internal/copytrading/service_test.go`, `internal/discovery/deploy_test.go`, `internal/kalshidiscovery/orchestrator_test.go`, `internal/polymarketdiscovery/orchestrator_test.go`, `internal/scheduler/scheduler_test.go`, and `internal/service/backtest_scaffold_test.go`. Also compile scheduler, generic discovery, options discovery, Kalshi discovery, Polymarket discovery, the automation orchestrator, and API backtest scaffolds after deleting `Create`.
- [ ] Change `api.StrategyRunner.RunStrategy` and `realStrategyRunner.RunStrategy` to accept the resolved version UUID. In `handleRunStrategy`, resolve it before `runGroup.Admit`, before starting the goroutine, and before any `NewStrategyExecutionScope` call. Scheduled callers in `cmd/tradingagent/runtime.go` resolve the same ID before dispatch. Missing or mismatched bindings fail closed and create no pipeline run.
- [ ] Test API create then manual run, discovery create then manual run, scheduled run, missing binding, and a binding to a version from another family. `cmd/tradingagent/prod_strategy_runner_test.go` proves the persisted run and execution scope use the resolved UUID, not the mutable legacy strategy UUID. A fresh manual run must persist a nonzero real `strategy_versions.id`; no fake may return the legacy strategy UUID as the version.
- [ ] Run `go test ./internal/strategycatalog ./internal/repository/postgres ./internal/api ./cmd/tradingagent` and `go build ./cmd/tradingagent`.
- [ ] Commit: `git add internal/domain/strategy.go internal/strategycatalog/family.go internal/strategycatalog/family_test.go internal/strategycatalog/version.go internal/strategycatalog/version_test.go internal/repository/interfaces.go internal/repository/postgres/strategy.go internal/repository/postgres/strategy_test.go internal/api/server.go internal/api/server_test.go internal/api/strategy_handlers.go internal/api/backtest_comparison_test.go internal/api/backtest_handlers_test.go internal/api/portfolio_allocator_handlers_test.go internal/api/event_market_handlers_test.go internal/api/kalshi_handlers_test.go internal/service/backtest_scaffold_test.go internal/discovery/deploy.go internal/discovery/deploy_test.go internal/discovery/orchestrator_test.go internal/discovery/options/orchestrator_test.go internal/kalshidiscovery/orchestrator_test.go internal/polymarketdiscovery/orchestrator_test.go internal/scheduler/scheduler_test.go internal/automation/alpaca_reconciliation_test.go internal/automation/jobs_portfolio_allocator_test.go internal/automation/orchestrator_test.go internal/automation/report_worker_test.go internal/copytrading/service_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/tradingagent/prod_strategy_runner.go cmd/tradingagent/prod_strategy_runner_test.go && git commit -m 'feat(strategy): bind execution versions'`.

### Task 5: Change `ProcessSignal` and every caller in one commit

**Files:**
- Modify: `internal/execution/order_manager.go`
- Modify: `internal/execution/order_manager_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `cmd/tradingagent/prod_strategy_runner_test.go`
- Modify: `internal/copytrading/executor.go`
- Modify: `internal/copytrading/service.go`
- Modify: `internal/copytrading/service_test.go`
- Modify: `internal/copytrading/origin_lifecycle_test.go`
- Modify: `internal/execution/lifecycle/intent.go`
- Modify: `internal/execution/lifecycle/intent_test.go`
- Modify: `internal/execution/lifecycle/order.go`
- Modify: `internal/execution/lifecycle/order_test.go`
- Modify: `internal/portfolio/paper_order_manager_processor.go`
- Modify: `internal/portfolio/paper_executor_test.go`

- [ ] Change the method and all internal submit, reject, cancel, and fill helpers in one edit:

```go
func (m *OrderManager) ProcessSignal(
	ctx context.Context,
	scope ExecutionScope,
	signal FinalSignal,
	plan TradingPlan,
) error
```

- [ ] Migrate all six production call sites: `cmd/tradingagent/runtime.go:1540`; `cmd/tradingagent/prod_strategy_runner.go:351`, `:779`, and `:918`; `internal/copytrading/executor.go:70`; and `internal/portfolio/paper_order_manager_processor.go:81`.
- [ ] Migrate every test call in `internal/execution/order_manager_test.go`. The current direct-call lines are 692, 740, 786, 881, 907, 939, 969, 993, 1019, 1053, 1088, 1131, 1182, 1251, 1305, 1349, 1383, 1438, 1495, 1614, 1652, 1706, 1745, 1798, 1866, 1907, 1956, 1988, 2039, 2082, 2136, 2188, 2240, 2302, 2352, 2391, 2458, 2491, 2528, 2571, 2655, 2707, 2780, 2825, 2878, 2937, 2968, 3001, 3053, and 3092. Rerun `rg -n '\.ProcessSignal\(' --glob '*.go'` and require only the six migrated production sites and these scope-bearing tests.
- [ ] The strategy runner and runtime callers use `NewStrategyExecutionScope`. `PaperOrderManagerProcessor.ProcessPaperOrder` receives a scope in `PaperOrderRequest` instead of separate `StrategyID` and `RunID` fields.
- [ ] Replace `PaperOrderRequest.Run domain.PipelineRun` with `OriginRunID uuid.UUID`. In `Service.Rebalance`, always create and register the `copyorigin.Run` first, then pass `persisted.ID()` to each approved intent. The ID passed to `NewCopyExecutionScope` is the actual `copy_origin_rebalance_runs.id` returned by `RegisterPlannedRun`, never `domain.PipelineRun.ID` from the request pipeline.
- [ ] Delete the strategy-free early return at `internal/copytrading/service.go:431-445` and the `LegacyStrategyID == nil` rejection at `internal/copytrading/executor.go:67-69`. Strategy-free copy proceeds through intent persistence, `OrderManagerExecutor.ExecuteCopyOrder`, accepted-fill coordination, and intent update. It persists an account-scoped order and fill. A legacy strategy may remain research metadata but cannot select execution identity.
- [ ] `OrderManagerExecutor.ExecuteCopyOrder` uses `NewCopyExecutionScope(configuredAccountID, configuredEnvironment, request.Subscription.ID, request.OriginRunID)`. Persist `orders.copy_origin_rebalance_run_id = request.OriginRunID`. Add `OrderRepository.GetByCopyOriginRun(ctx context.Context, copyOriginRunID uuid.UUID, filter repository.OrderFilter, limit, offset int) ([]domain.Order, error)` with `WHERE account_id=$1 AND origin_type='copy_subscription' AND copy_origin_rebalance_run_id=$2`. Delete the `GetByRun` lookup from this path. Migration 109 requires the referenced copy run to have the same account and requires the order's `origin_id` to equal that run's `subscription_id::text`.
- [ ] Add `CopyOriginRebalanceRunID uuid.UUID` to `lifecycle.Intent` and `lifecycle.Order`. `NewCopyExecutionScope` populates it from the persisted `copy_origin_rebalance_runs.id`; all other scope constructors leave it zero. Carry the value through proposal, allocation, routing, recovery, and accepted-fill coordination. PostgreSQL maps zero to `NULL` and a nonzero value to both `execution_intents.copy_origin_rebalance_run_id` and `execution_orders.copy_origin_rebalance_run_id`.
- [ ] Add tests in `internal/copytrading/service_test.go` and `internal/copytrading/origin_lifecycle_test.go` for strategy-free copy with one approved buy. Assert `copy_origin_rebalance_runs.id == scope.CopyOriginRunID()`, and assert the order, lifecycle fill, position or close effect, trade, normalization, ledger transaction, and updated copy intent all carry the configured account.
- [ ] Keep `internal/execution/prediction_market.go`. Prediction settlement is the separate existing package `internal/execution/prediction`; do not create `internal/predictionexecution`.
- [ ] Run `go test ./internal/execution ./internal/copytrading ./internal/portfolio ./cmd/tradingagent` and `go build ./cmd/tradingagent`.
- [ ] Commit: `git add internal/execution/order_manager.go internal/execution/order_manager_test.go internal/execution/lifecycle/intent.go internal/execution/lifecycle/intent_test.go internal/execution/lifecycle/order.go internal/execution/lifecycle/order_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/tradingagent/prod_strategy_runner.go cmd/tradingagent/prod_strategy_runner_test.go internal/copytrading/executor.go internal/copytrading/service.go internal/copytrading/service_test.go internal/copytrading/origin_lifecycle_test.go internal/portfolio/paper_order_manager_processor.go internal/portfolio/paper_executor_test.go && git commit -m 'refactor(execution): require scope at signal entry'`.

### Task 6: Bind repositories and complete pipeline-run keys

**Files:**
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/repository/postgres/pipeline_run.go`
- Modify: `internal/repository/postgres/pipeline_run_test.go`
- Modify: `internal/repository/postgres/pipeline_run_snapshot.go`
- Modify: `internal/repository/postgres/pipeline_run_snapshot_test.go`
- Modify: `internal/repository/postgres/agent_decision.go`
- Modify: `internal/repository/postgres/agent_decision_test.go`
- Modify: `internal/repository/postgres/agent_event.go`
- Modify: `internal/repository/postgres/agent_event_test.go`
- Modify: `internal/repository/postgres/trade_decision_journal.go`
- Modify: `internal/repository/postgres/trade_decision_journal_test.go`
- Modify: `internal/repository/postgres/order.go`
- Modify: `internal/repository/postgres/order_test.go`
- Modify: `internal/repository/postgres/position.go`
- Modify: `internal/repository/postgres/position_test.go`
- Modify: `internal/repository/postgres/trade.go`
- Modify: `internal/repository/postgres/trade_test.go`
- Modify: `internal/repository/postgres/opportunity.go`
- Modify: `internal/repository/postgres/opportunity_test.go`
- Modify: `internal/repository/postgres/allocation_decision.go`
- Modify: `internal/repository/postgres/allocation_decision_test.go`
- Modify: `internal/repository/postgres/replay_event.go`
- Modify: `internal/repository/postgres/replay_event_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `cmd/tradingagent/prod_strategy_runner_test.go`
- Modify: `cmd/tradingagent/smoke_test.go`
- Modify: `internal/api/run_handlers.go`
- Modify: `internal/api/server_test.go`
- Modify: `internal/api/conversation_handlers.go`
- Create: `internal/api/conversation_handlers_test.go`
- Modify: `internal/api/memory_handlers.go`
- Create: `internal/api/memory_handlers_test.go`
- Modify: `internal/domain/conversation.go`
- Modify: `internal/domain/memory.go`
- Modify: `internal/repository/postgres/conversation.go`
- Modify: `internal/repository/postgres/conversation_test.go`
- Modify: `internal/repository/postgres/memory.go`
- Modify: `internal/repository/postgres/memory_test.go`
- Modify: `internal/service/conversation.go`
- Create: `internal/service/conversation_test.go`
- Modify: `internal/service/run.go`
- Modify: `internal/service/run_test.go`
- Modify: `internal/memory/reflection.go`
- Modify: `internal/memory/reflection_test.go`
- Modify: `internal/agent/conversation/context.go`
- Modify: `internal/agent/conversation/context_test.go`
- Modify: `internal/agent/persister_repo.go`
- Modify: `internal/agent/pipeline_test.go`
- Modify: `internal/agent/stale_run_reconciler.go`
- Modify: `internal/agent/stale_run_reconciler_test.go`
- Modify: `internal/automation/orchestrator.go`
- Modify: `internal/automation/jobs_postmarket.go`
- Modify: `internal/automation/jobs_postmarket_test.go`
- Modify: `internal/copytrading/service.go`
- Modify: `internal/copytrading/service_test.go`
- Modify: `internal/integration/testhelpers_test.go`

- [ ] Change each repository interface, concrete type, constructor, runtime assembly caller, and test fake in this task. Constructors require `accountID uuid.UUID`. Reads, counts, updates, finalization, and deletes include `account_id`. This commit includes the production runner, run handlers, conversation handlers, memory reflection, agent conversation context, runtime, smoke test, integration helpers, embedded-interface fakes, agent persister and stale-run reconciliation, automation postmarket listing, copy service, and `internal/service/run.go`.
- [ ] Replace run-only UUID arguments with `domain.PipelineRunRef`. `pipeline_runs` is partitioned by `(id, trade_date)`; every child insert stores both values and every graph query joins on both. `internal/repository/interfaces.go` imports `internal/domain`, never `internal/execution`.

```sql
SELECT run.account_id
FROM pipeline_runs AS run
WHERE run.id = $1 AND run.trade_date = $2;
```

- [ ] Return `repository.ErrNotFound` for a detail ID owned by another account. Collections return no rows. Do not expose legacy unscoped readers through `api.Deps`.
- [ ] Scope conversations and memories. Migration 108 adds nullable `account_id`, `environment`, and `pipeline_run_trade_date` to `conversations`; nullable `account_id` to `conversation_messages`; and nullable `account_id`, `environment`, and `pipeline_run_trade_date` to `agent_memories`. Migration 109 validates each message against its conversation and every run reference by `(pipeline_run_id, pipeline_run_trade_date, account_id)`.
- [ ] Add account and environment fields to `domain.Conversation` and `domain.AgentMemory`, plus account ID to `domain.ConversationMessage`. Change `ConversationFilter`, `MemorySearchFilter`, `ConversationRepository`, and `MemoryRepository`. Constructors bind the configured account. Every create, get, list, count, message append, search, and delete query includes it. Update `internal/repository/postgres/conversation.go`, `internal/repository/postgres/memory.go`, their tests, `internal/service/conversation.go`, `internal/api/conversation_handlers.go`, `internal/api/memory_handlers.go`, runtime construction at `cmd/tradingagent/runtime.go:484` and `:491`, and all fakes.
- [ ] Move routes to `/api/v1/accounts/{accountID}/conversations` and `/api/v1/accounts/{accountID}/memories`. Delete global `/api/v1/conversations` and `/api/v1/memories`. Cross-account conversation, message, memory search, and delete return 404. Legacy NULL rows never appear.
- [ ] Use a compile-valid additive transition inside this commit. Add account-scoped methods with `Scoped` suffixes. Before deleting UUID-only `PipelineRunRepository.GetByID`, migrate `internal/automation/jobs_portfolio_allocator.go:151`, `internal/automation/jobs_portfolio_allocator_test.go`, `internal/automation/jobs_postmarket_test.go`, every caller listed in this task, and every fake to the `(id, trade_date)` `domain.PipelineRunRef` API. Then delete the old UUID API and suffixes before the commit. Do not commit with both APIs.
- [ ] Run `rg -n 'PipelineRunRepository|NewPipelineRunRepo|GetByRun\(|GetByID\(|\.Runs\.(Create|Finalize)|runRepo\.(Create|GetByID|Get|List|Count|Finalize|RefineCompletedSignal)' --glob '*.go'`. Classify every match. No run-child call may pass only a UUID.
- [ ] Run `go test -race ./internal/repository/... ./cmd/tradingagent` and `go build ./cmd/tradingagent`.
- [ ] Include the conversation and memory files in this commit: `git add internal/domain/conversation.go internal/domain/memory.go internal/repository/postgres/conversation.go internal/repository/postgres/conversation_test.go internal/repository/postgres/memory.go internal/repository/postgres/memory_test.go internal/api/memory_handlers.go internal/api/memory_handlers_test.go internal/service/conversation.go internal/service/conversation_test.go`.
- [ ] Commit: `git add internal/repository/interfaces.go internal/repository/postgres/pipeline_run.go internal/repository/postgres/pipeline_run_test.go internal/repository/postgres/pipeline_run_snapshot.go internal/repository/postgres/pipeline_run_snapshot_test.go internal/repository/postgres/agent_decision.go internal/repository/postgres/agent_decision_test.go internal/repository/postgres/agent_event.go internal/repository/postgres/agent_event_test.go internal/repository/postgres/trade_decision_journal.go internal/repository/postgres/trade_decision_journal_test.go internal/repository/postgres/order.go internal/repository/postgres/order_test.go internal/repository/postgres/position.go internal/repository/postgres/position_test.go internal/repository/postgres/trade.go internal/repository/postgres/trade_test.go internal/repository/postgres/opportunity.go internal/repository/postgres/opportunity_test.go internal/repository/postgres/allocation_decision.go internal/repository/postgres/allocation_decision_test.go internal/repository/postgres/replay_event.go internal/repository/postgres/replay_event_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/tradingagent/prod_strategy_runner.go cmd/tradingagent/prod_strategy_runner_test.go cmd/tradingagent/smoke_test.go internal/api/run_handlers.go internal/api/server_test.go internal/api/conversation_handlers.go internal/api/conversation_handlers_test.go internal/service/run.go internal/service/run_test.go internal/memory/reflection.go internal/memory/reflection_test.go internal/agent/conversation/context.go internal/agent/conversation/context_test.go internal/agent/persister_repo.go internal/agent/pipeline_test.go internal/agent/stale_run_reconciler.go internal/agent/stale_run_reconciler_test.go internal/automation/orchestrator.go internal/automation/jobs_postmarket.go internal/automation/jobs_postmarket_test.go internal/copytrading/service.go internal/copytrading/service_test.go internal/integration/testhelpers_test.go && git commit -m 'refactor(repo): bind operations to account'`.

### Task 7: Scope the pipeline and portfolio graph

**Files:**
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `cmd/tradingagent/prod_strategy_runner_test.go`
- Modify: `internal/execution/decision_recorder.go`
- Modify: `internal/execution/decision_recorder_test.go`
- Modify: `internal/portfolio/opportunity_builder.go`
- Modify: `internal/portfolio/opportunity_builder_test.go`
- Modify: `internal/portfolio/paper_executor.go`
- Modify: `internal/portfolio/paper_executor_test.go`
- Modify: `internal/portfolio/paper_order_manager_processor.go`
- Modify: `internal/automation/jobs_portfolio_allocator.go`
- Modify: `internal/automation/jobs_portfolio_allocator_test.go`

- [ ] Add `Scope execution.ExecutionScope` to `portfolio.OpportunityBuildInput`. Keep the real function name and signature form: `portfolio.BuildOpportunity(input OpportunityBuildInput, cfg OpportunityBuilderConfig) (*domain.Opportunity, NoActionReason, error)`.
- [ ] Pass the same scope through run create/finalize, snapshots, decisions, events, trade journal, opportunity, allocation, paper execution, and replay. Derive child scope from the persisted parent and reject supplied mismatches.
- [ ] Test one complete graph, cancellation, retry after restart, and a conflicting account retry.
- [ ] Run `go test -race ./cmd/tradingagent ./internal/execution ./internal/portfolio ./internal/automation` and `go build ./cmd/tradingagent`.
- [ ] Commit: `git add cmd/tradingagent/prod_strategy_runner.go cmd/tradingagent/prod_strategy_runner_test.go internal/execution/decision_recorder.go internal/execution/decision_recorder_test.go internal/portfolio/opportunity_builder.go internal/portfolio/opportunity_builder_test.go internal/portfolio/paper_executor.go internal/portfolio/paper_executor_test.go internal/portfolio/paper_order_manager_processor.go internal/automation/jobs_portfolio_allocator.go internal/automation/jobs_portfolio_allocator_test.go && git commit -m 'feat(pipeline): persist account lineage'`.

### Task 8: Scope stock, options, prediction venues, copy, and restart

**Files:**
- Modify: `internal/execution/order_manager.go`
- Modify: `internal/execution/order_manager_test.go`
- Modify: `internal/execution/options_manager.go`
- Modify: `internal/execution/options_manager_test.go`
- Modify: `internal/execution/options_reconcile.go`
- Modify: `internal/execution/options_reconcile_test.go`
- Modify: `internal/execution/options_expiry.go`
- Modify: `internal/execution/options_expiry_test.go`
- Modify: `internal/execution/prediction_market.go`
- Modify: `internal/execution/prediction/settlement.go`
- Modify: `internal/execution/prediction/settlement_test.go`
- Modify: `internal/execution/prediction/types.go`
- Modify: `internal/execution/venue/result.go`
- Modify: `internal/execution/venue/result_test.go`
- Modify: `internal/execution/kalshi/executor.go`
- Modify: `internal/execution/kalshi/executor_test.go`
- Modify: `internal/execution/kalshi/reconciler.go`
- Modify: `internal/execution/kalshi/reconciler_test.go`
- Modify: `internal/execution/polymarket/executor.go`
- Modify: `internal/execution/polymarket/executor_test.go`
- Modify: `internal/execution/polymarket/reconciler.go`
- Modify: `internal/execution/polymarket/reconciler_test.go`
- Modify: `internal/execution/polymarket/stop_guard.go`
- Modify: `internal/execution/polymarket/stop_guard_test.go`
- Modify: `internal/copytrading/executor.go`
- Modify: `internal/copytrading/origin_lifecycle_test.go`
- Modify: `internal/copytrading/service.go`
- Modify: `internal/copytrading/service_test.go`
- Modify: `internal/repository/postgres/copy_trading.go`
- Modify: `internal/repository/postgres/copy_trading_test.go`
- Modify: `internal/repository/postgres/paper_account.go`
- Modify: `internal/repository/postgres/paper_account_test.go`
- Modify: `internal/automation/jobs_events.go`
- Modify: `internal/automation/jobs_events_test.go`
- Modify: `internal/automation/jobs_kalshi_marking.go`
- Modify: `internal/automation/jobs_kalshi_marking_test.go`
- Modify: `internal/automation/orchestrator.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`

- [ ] Scope risk reads, restoration reads, copy subscription and intent access, settlement candidates, stop guards, and idempotency keys. Replace `kalshiExposureMu` with an account-keyed PostgreSQL transaction advisory lock.
- [ ] Bind the Kalshi marking job to the configured account. Add `CanonicalAccountID uuid.UUID` to `automation.OrchestratorDeps`. Change `ProjectionRepository.ListCanonicalOpenLots` to require that account. `internal/automation/jobs_kalshi_marking.go` rejects any returned lot whose `AccountID` differs and records marks only for the configured account. Delete the current map-based multi-account rebuild behavior. Do not convert its projection enqueue in this task; Task 9 moves that enqueue only after the outbox repository and worker exist.
- [ ] Update `internal/automation/jobs_kalshi_marking_test.go` to cover configured-account lots, a foreign-account row from a faulty repository, no inventory, unavailable marks, provider failure, and shutdown. Update runtime wiring and all `OrchestratorDeps` fakes.
- [ ] Lock persisted orders, positions, decisions, subscriptions, and intents by account before deriving child writes. A caller-supplied account never overrides persisted ownership.
- [ ] Propagate `ExecutionScope` through every venue result and settlement input listed here. Keep the existing financial lifecycle call in this compiling commit. Task 9 switches all accepted-fill consumers after the coordinator exists.
- [ ] Cover stock buy/partial fill/fill/sell/cancel/reject/retry/restart; option open/close/multi-leg/expiry; Kalshi buy/sell/settlement; Polymarket buy/sell/resolution; and copy preview/rebalance/order/fill/restart.
- [ ] Run `go test -race ./internal/execution/... ./internal/copytrading ./internal/repository/postgres ./internal/automation ./cmd/tradingagent` and `go build ./cmd/tradingagent`.
- [ ] Include the Kalshi marking files in this commit: `git add internal/automation/jobs_kalshi_marking.go internal/automation/jobs_kalshi_marking_test.go internal/automation/orchestrator.go`.
- [ ] Commit: `git add internal/execution/order_manager.go internal/execution/order_manager_test.go internal/execution/options_manager.go internal/execution/options_manager_test.go internal/execution/options_reconcile.go internal/execution/options_reconcile_test.go internal/execution/options_expiry.go internal/execution/options_expiry_test.go internal/execution/prediction_market.go internal/execution/prediction/settlement.go internal/execution/prediction/settlement_test.go internal/execution/prediction/types.go internal/execution/venue/result.go internal/execution/venue/result_test.go internal/execution/kalshi/executor.go internal/execution/kalshi/executor_test.go internal/execution/kalshi/reconciler.go internal/execution/kalshi/reconciler_test.go internal/execution/polymarket/executor.go internal/execution/polymarket/executor_test.go internal/execution/polymarket/reconciler.go internal/execution/polymarket/reconciler_test.go internal/execution/polymarket/stop_guard.go internal/execution/polymarket/stop_guard_test.go internal/copytrading/executor.go internal/copytrading/origin_lifecycle_test.go internal/copytrading/service.go internal/copytrading/service_test.go internal/repository/postgres/copy_trading.go internal/repository/postgres/copy_trading_test.go internal/repository/postgres/paper_account.go internal/repository/postgres/paper_account_test.go internal/automation/jobs_events.go internal/automation/jobs_events_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go && git commit -m 'feat(execution): isolate venue writers'`.

### Task 9: Coordinate atomic economic writes and post-commit projections

**Files:**
- Create: `internal/repository/postgres/economic_fill_coordinator.go`
- Create: `internal/repository/postgres/economic_fill_coordinator_test.go`
- Create: `internal/execution/economic_fill.go`
- Create: `internal/execution/economic_fill_test.go`
- Create: `internal/repository/postgres/projection_outbox.go`
- Create: `internal/repository/postgres/projection_outbox_test.go`
- Create: `internal/repository/postgres/projection_worker.go`
- Create: `internal/repository/postgres/projection_worker_test.go`
- Modify: `internal/execution/order_manager.go`
- Modify: `internal/execution/order_manager_test.go`
- Modify: `internal/execution/options_manager.go`
- Modify: `internal/execution/options_manager_test.go`
- Modify: `internal/execution/prediction/settlement.go`
- Modify: `internal/execution/prediction/settlement_test.go`
- Modify: `internal/execution/prediction/types.go`
- Modify: `internal/execution/venue/result.go`
- Modify: `internal/execution/venue/result_test.go`
- Modify: `internal/execution/alpaca/common_lifecycle_result.go`
- Modify: `internal/execution/alpaca/common_lifecycle_test.go`
- Modify: `internal/execution/kalshi/common_lifecycle_result.go`
- Modify: `internal/execution/kalshi/common_lifecycle_test.go`
- Modify: `internal/ledger/projection.go`
- Modify: `internal/ledger/projection_test.go`
- Modify: `internal/repository/postgres/financial_lifecycle.go`
- Modify: `internal/repository/postgres/financial_lifecycle_test.go`
- Modify: `internal/repository/postgres/execution_lifecycle.go`
- Modify: `internal/repository/postgres/execution_lifecycle_test.go`
- Modify: `internal/repository/postgres/projection.go`
- Modify: `internal/repository/postgres/projection_test.go`
- Modify: `internal/repository/postgres/projection_reader.go`
- Modify: `internal/repository/postgres/projection_reader_test.go`
- Modify: `internal/accountingrecon/source.go`
- Modify: `internal/accountingrecon/source_test.go`
- Modify: `internal/accountingrecon/gate.go`
- Modify: `internal/accountingrecon/gate_test.go`
- Modify: `internal/automation/jobs_kalshi_marking.go`
- Modify: `internal/automation/jobs_kalshi_marking_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`

- [ ] Persist raw venue evidence before financial mutation. A failed raw-evidence insert stops processing. A committed raw event can be retried by deterministic provider event identity.
- [ ] Put `EconomicFillCoordinator` in `internal/execution/economic_fill.go`, the consumer package. Do not put it in `internal/repository/interfaces.go`. `internal/execution` may import neutral repository input types; `internal/repository/postgres` implements the interface and may import both packages. The root `internal/repository` package never imports `internal/execution`.
- [ ] Convert every accepted-fill consumer in this commit. Run `rg -n 'ApplyOrderFill\(|ApplyOptionFills\(|lifecycle\.RecordFill\(' internal cmd --glob '*.go'` and classify every match. Convert `OrderManager`, `OptionsManager`, prediction settlement, `internal/execution/venue/result.go`, `internal/execution/alpaca/common_lifecycle_result.go`, and `internal/execution/kalshi/common_lifecycle_result.go`. Convert their tests and fakes in the same commit. After conversion, no production caller invokes `repository.FinancialLifecycleRepository.ApplyOrderFill` or `ApplyOptionFills` directly.
- [ ] Add one explicit PostgreSQL coordinator. Its `ApplyAcceptedFill` starts one transaction and passes `pgx.Tx` only to PostgreSQL repository helpers. The transaction locks the account-owned order and writes mutable order/position/trade state, common lifecycle, economic source normalization, balanced ledger postings, idempotency rows, and one `account_projection_outbox` row. It commits or rolls back as one unit.
- [ ] Construct both the economic coordinator and `ProjectionOutboxRepository` with the runtime application `*pgxpool.Pool`, which connects as `augr_app_runtime`. Claim, heartbeat, complete, retry, and degrade outbox rows through that pool. Grant `augr_app_runtime` only `SELECT, INSERT, UPDATE` on `account_projection_outbox`; do not grant `DELETE` or `TRUNCATE`. The same runtime role inserts `mark_observations`. Keep `augr_projection_writer` checkpoint-only: projection input `SELECT` and `EXECUTE` on `persist_canonical_projection_checkpoint`. Revoke its `INSERT` on `mark_observations` and every outbox privilege.
- [ ] For `economic_fill`, use the zero generation UUID. For `mark_rebuild`, compute `economicid.DeterministicUUID("projection-mark-generation-v1", accountID.String(), throughTransactionID.String(), markAsOf.UTC().Format(time.RFC3339Nano), strings.Join(sortedMarkObservationIDs, ","))`. A later mark batch at the same ledger frontier therefore gets a new generation. Use this exact race-safe coalescing statement:

```sql
INSERT INTO account_projection_outbox (
	id, account_id, request_kind, through_transaction_id, as_of,
	mark_as_of, mark_generation, status, attempt_count, next_attempt_at,
	created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', 0, $5, $5, $5)
ON CONFLICT (account_id, request_kind, through_transaction_id, mark_generation)
DO UPDATE SET
	next_attempt_at = LEAST(account_projection_outbox.next_attempt_at, EXCLUDED.next_attempt_at),
	updated_at = EXCLUDED.updated_at
WHERE account_projection_outbox.status IN ('pending', 'retry')
RETURNING id, status;
```

  Treat no returned row as an already processing, completed, or degraded generation. Never reset that row. Preserve the original `as_of` on conflict.
- [ ] In this task, convert `internal/automation/jobs_kalshi_marking.go` from direct projection rebuild to `ProjectionOutboxRepository.RecordMarksAndEnqueueRebuild`. That repository starts one runtime-pool transaction, inserts all deduplicated mark observations, computes the generation from the returned mark IDs, inserts the outbox row, and commits. Any mark or outbox failure rolls back both. The projection repository exposes no mark insert method and the projection worker only reads marks. Tests race two identical batches, enqueue a later generation at the same ledger frontier, inject failure between marks and outbox, and prove one atomic row per generation with no orphan marks or lost rebuild.

```go
type AcceptedFillInput struct {
	Scope             ExecutionScope
	Mutation          repository.OrderFillInput
	PriorLifecycle    *lifecycle.Aggregate
	Transition        *lifecycle.Transition
	AcceptedFill      *lifecycle.Fill
	SourceEvent       *ledger.EconomicSourceEvent
	Instrument        *instrument.Instrument
	VenueContract     *instrument.VenueContract
	Normalization     *ledger.EconomicNormalization
	LedgerTransaction *ledger.Transaction
}

type EconomicFillCoordinator interface {
	ApplyAcceptedFill(context.Context, AcceptedFillInput) (AcceptedFillResult, error)
	ApplyAcceptedOptionFills(context.Context, []AcceptedFillInput) ([]AcceptedFillResult, error)
	SettlePredictionDecision(context.Context, AcceptedPredictionSettlementInput) (repository.PredictionDecisionSettlementResult, error)
}
```

- [ ] `AcceptedFillInput.Validate` requires one accepted lifecycle fill and exact payload equality among `Transition.Fill`, `Transition.Normalization`, `SourceEvent`, `Instrument`, `VenueContract`, `Normalization`, and `LedgerTransaction`. It verifies the scope account and origin, source-event ID, canonical instrument and venue-contract IDs, normalization ID, and ledger transaction ID before PostgreSQL begins a transaction. `AcceptedPredictionSettlementInput` carries the same scope, source event, canonical instrument and venue contract, normalization, and ledger transaction fields for settlement.

- [ ] In `internal/execution/venue/result.go`, keep `RecordVenueObservation` and `RecordEconomicSourceEvent` on the raw-first `Persistence` boundary. Replace only `ApplyExecutionFill` with `ApplyAcceptedFill(AcceptedFillInput)`; non-fill `ApplyExecutionTransition` remains lifecycle-only. Delete `settleDecisionLegacy` from prediction settlement and require `EconomicFillCoordinator.SettlePredictionDecision`. Alpaca and Kalshi planners pass their `CommonLifecycleContext` account, instrument, venue contract, source event, transition, normalization, and transaction without reconstructing them from IDs.

- [ ] Extend the actual `ledger.ProjectionRequest` in `internal/ledger/projection.go` with `ThroughTransactionID uuid.UUID`. Update `loadProjectionTransactions`, `loadProjectionMechanics`, and `loadProjectionMarks` so a rebuild includes only the deterministic transaction frontier ending at that ID. Load the named transaction for the account, require both timestamps at or before `AsOf`, and include rows whose `(effective_at, observed_at, id)` tuple is less than or equal to the named frontier tuple. Reject a frontier owned by another account or later than `AsOf`; do not infer the frontier with `ORDER BY ... LIMIT 1`.
- [ ] In migration 108, replace `validate_canonical_projection_checkpoint()` from migration 69. Remove its current requirement that `through_transaction_id` equal the latest eligible transaction at `AsOf`. Validate that the named frontier belongs to the account, is eligible at `AsOf`, and that `transaction_count` equals the count through the same `(effective_at, observed_at, id)` tuple. Preserve every schema-69 payload, HMAC, currency, deterministic-ID, mark, lot, match, and position check. The down migration restores the exact migration-69 function only if no migration-108 checkpoint exists.
- [ ] Do not call `ProjectionRepo.RebuildPortfolioProjection` inside the economic transaction. The outbox insert is the enqueue. After commit, the worker claims rows with `FOR UPDATE SKIP LOCKED` through the runtime `*pgxpool.Pool` and commits the claim. It then calls `ProjectionRepo.RebuildPortfolioProjection` through the separately constructed projection pool with the stored account, `AsOf`, and `ThroughTransactionID`. The projection pool uses the existing repeatable-read input load, HMAC attestation, and `persist_canonical_projection_checkpoint(BYTEA, TEXT, BYTEA)`. Completion, retry, degradation, and heartbeat return to the runtime pool.
- [ ] Add `claimed_by` and `claim_expires_at` to the outbox. A claim transaction may select `pending`, due `retry`, or `processing` rows whose lease expired. It sets a bounded lease and commits before projection work. Heartbeats extend only the same worker's lease. Completion and retry updates require matching `claimed_by`; losing the lease aborts completion. Clear claim fields on retry, degraded, and completed. No `processing` claim may remain stranded after a crash.
- [ ] On rebuild failure, persist `status='retry'`, an incremented `attempt_count`, bounded exponential `next_attempt_at`, and a sanitized `last_error_code` through the runtime pool. After the retry limit, persist `status='degraded'`. Portfolio, Risk, Cockpit, and Reports read pending, processing, retry, and degraded rows and return degraded or unavailable evidence. A successful retry marks the row completed and rebuilds the same frontier without another economic effect. Process restart resumes pending, retry, and expired processing rows.
- [ ] Give the projection worker a stop-accepting signal and bounded `Drain(ctx)` method. Shutdown stops claims, waits for in-flight rebuilds, and either completes them or returns their leases to retry before pool closure. Tests kill a worker after claim, recover the expired lease, retry through completion, and prove that no claim remains stranded.
- [ ] Inject failures after raw evidence, mutable state, lifecycle, normalization, ledger, transaction commit, projection enqueue, attestation, and checkpoint persistence. Prove rollback before commit and degraded durable state after projection failure.
- [ ] Run `go test -race ./internal/repository/postgres ./internal/accountingrecon ./cmd/tradingagent` and `go build ./cmd/tradingagent`.
- [ ] Commit: `git add internal/execution/economic_fill.go internal/execution/economic_fill_test.go internal/execution/order_manager.go internal/execution/order_manager_test.go internal/execution/options_manager.go internal/execution/options_manager_test.go internal/execution/prediction/settlement.go internal/execution/prediction/settlement_test.go internal/execution/prediction/types.go internal/execution/venue/result.go internal/execution/venue/result_test.go internal/execution/alpaca/common_lifecycle_result.go internal/execution/alpaca/common_lifecycle_test.go internal/execution/kalshi/common_lifecycle_result.go internal/execution/kalshi/common_lifecycle_test.go internal/ledger/projection.go internal/ledger/projection_test.go internal/repository/postgres/economic_fill_coordinator.go internal/repository/postgres/economic_fill_coordinator_test.go internal/repository/postgres/projection_outbox.go internal/repository/postgres/projection_outbox_test.go internal/repository/postgres/projection_worker.go internal/repository/postgres/projection_worker_test.go internal/repository/postgres/financial_lifecycle.go internal/repository/postgres/financial_lifecycle_test.go internal/repository/postgres/execution_lifecycle.go internal/repository/postgres/execution_lifecycle_test.go internal/repository/postgres/projection.go internal/repository/postgres/projection_test.go internal/repository/postgres/projection_reader.go internal/repository/postgres/projection_reader_test.go internal/accountingrecon/source.go internal/accountingrecon/source_test.go internal/accountingrecon/gate.go internal/accountingrecon/gate_test.go internal/automation/jobs_kalshi_marking.go internal/automation/jobs_kalshi_marking_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go && git commit -m 'feat(ledger): coordinate accepted fills'`.

### Task 10: Enable guards in migration 109

**Files:**
- Create: `migrations/000109_enforce_canonical_account.up.sql`
- Create: `migrations/000109_enforce_canonical_account.down.sql`
- Create: `migrations/000109_enforce_canonical_account_test.go`
- Modify: `internal/repository/postgres/schema_version.go`
- Modify: `internal/repository/postgres/schema_version_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `cmd/tradingagent/schema_version_sync_test.go`
- Modify: `cmd/augr-economic/main.go`
- Modify: `cmd/augr-evidence/main.go`
- Create: `scripts/apply-migrations-psql.sh`
- Create: `scripts/apply-migrations-psql_test.sh`
- Modify: `internal/cli/root_test.go`
- Modify: `internal/runcontrol/group_test.go`

- [ ] Build an enforcement-ready rehearsal binary that accepts 108 and 109. Keep all scoped writes valid on 108. Do not deploy it to production.
- [ ] In migration 109, lock every guarded table before inspecting rows or installing triggers. Triggers reject a new unscoped row, account mutation, inactive account, environment mismatch, and cross-account parentage.
- [ ] Validate pipeline links with both key columns. Never use `ORDER BY trade_date DESC LIMIT 1`:

```sql
IF NEW.pipeline_run_id IS NOT NULL THEN
	IF NEW.pipeline_run_trade_date IS NULL OR NOT EXISTS (
		SELECT 1 FROM pipeline_runs AS run
		WHERE run.id = NEW.pipeline_run_id
		  AND run.trade_date = NEW.pipeline_run_trade_date
		  AND run.account_id = NEW.account_id
	) THEN
		RAISE EXCEPTION 'scoped row does not match pipeline run identity';
	END IF;
END IF;
```

- [ ] Validate copy subscription, intent, origin run, origin-run intent, drift run, and drift leg account links without changing their existing UUID origin columns or append-only guards.
- [ ] For an order with `origin_type='copy_subscription'`, require non-NULL `copy_origin_rebalance_run_id`. Join `orders.copy_origin_rebalance_run_id` to `copy_origin_rebalance_runs.id`, require equal `account_id`, and require `orders.origin_id = copy_origin_rebalance_runs.subscription_id::text`. Reject `copy_origin_rebalance_run_id` on every other origin type.
- [ ] Apply the same rule to the common lifecycle. For `execution_intents.origin_type='copy_subscription'`, require a non-NULL `copy_origin_rebalance_run_id`, join that ID to `copy_origin_rebalance_runs`, require equal account IDs, and require `execution_intents.origin_id = copy_origin_rebalance_runs.subscription_id::text`. For each `execution_orders` insert, lock its parent intent, require equal `account_id`, and require `execution_orders.copy_origin_rebalance_run_id IS NOT DISTINCT FROM execution_intents.copy_origin_rebalance_run_id`. Reject a common copy-run ID for every non-copy origin. Test missing IDs, foreign-account runs, another subscription's run, intent/order mismatch, retry, and strategy, settlement, and reconciliation rows with `NULL`.
- [ ] Add role grants in migration 109 and test them with `has_table_privilege`. `augr_app_runtime` gets only `SELECT, INSERT, UPDATE` on `account_projection_outbox` and `INSERT` on `mark_observations`. `augr_projection_writer` gets no outbox privilege and no `INSERT` on `mark_observations`; it retains projection input reads and checkpoint-function execute only.
- [ ] Do not add a shutdown, dispatch, drain, or system-safety endpoint. Preserve the current SIGTERM path: `internal/cli/root.go` receives SIGTERM, stops HTTP admission, calls `runtimeLifecycle.Stop`, and leaves the DB pools open until `runtimeTeardown.Stop` closes run admission, cancels admitted contexts, stops automation, scheduler, reconciler, and workers, waits for all run leases, and finalizes or cancels terminal DB state.
- [ ] Add tests in `internal/cli/root_test.go`, `internal/runcontrol/group_test.go`, `cmd/tradingagent/runtime_test.go`, and `internal/repository/postgres/projection_worker_test.go`. Prove SIGTERM rejects later admissions, cancels an active pipeline, waits for its terminal write, drains or returns every projection lease to retry, then closes pools. Assert zero `pipeline_runs.status='running'`, zero `automation_job_runs.status='running'`, and zero `account_projection_outbox.status='processing'` after shutdown.
- [ ] Before Phase D, run the same test against the exact currently deployed schema-107 image. If it fails, stop this cutover. Plan and release an earlier schema-107-compatible shutdown-only fix that changes no migration and no account schema. Deploy and verify that fix before resuming this plan. Never solve old-production drain with a schema-108 or schema-109 endpoint.
- [ ] The down migration starts with `LOCK TABLE pipeline_runs, pipeline_run_snapshots, agent_decisions, agent_events, trade_decisions, orders, positions, trades, portfolio_opportunities, allocation_decisions, replay_events, financial_fill_idempotency, prediction_settlement_idempotency, execution_intents, execution_orders, copy_subscriptions, copy_trade_intents, copy_origin_rebalance_runs, copy_origin_rebalance_intents, copy_target_drift_runs, copy_target_drift_legs, conversations, conversation_messages, agent_memories, account_projection_outbox IN ACCESS EXCLUSIVE MODE`. It then refuses rollback while any scoped row exists and removes only migration-109 triggers and functions. Migration 108 remains applied.
- [ ] Add `scripts/apply-migrations-psql.sh`. It accepts exactly `--database NAME --from VERSION --to VERSION`, rejects `tradingagent`, reads the tracked `migrations/NNNNNN_name.up.sql` files in numeric order, and pipes each file through `docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$database"`. Hold `flock -n 9` on `.git/augr-migration-runner.lock` for the whole invocation. Before migration 1 on `--from 0`, open one owner transaction, take `pg_advisory_xact_lock(hashtextextended(current_database() || ':schema_migrations',0))`, create `schema_migrations(version BIGINT NOT NULL, dirty BOOLEAN NOT NULL)`, lock it in `ACCESS EXCLUSIVE` mode, require zero rows, and insert exactly `(0,false)`. For nonzero `--from`, require the table, exactly one row, the requested version, and `dirty=false`.
- [ ] Before each file, use an owner transaction with the same advisory lock and `LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE`; recheck the exact prior version and `dirty=false`, then commit `dirty=true`. Run the migration file with `psql --single-transaction`, `ON_ERROR_STOP=1`, and `SET ROLE augr_db_owner`. After success, use another locked owner transaction to require the prior version with `dirty=true`, set the applied version, and set `dirty=false`. Down uses the same protocol in reverse and records the resulting lower version. A SQL failure leaves the prior version with `dirty=true`; the script refuses every later invocation and runs `printf 'database %s is dirty at version %s; restore the disposable database or fresh target from its pre-migration backup\n' "$database" "$stored_version" >&2`. Do not add a force or repair flag. Production failure recovery drops and recreates the still-unselected fresh DB, then reruns `0 -> 109`; disposable tests do the same. The old DB is never a recovery target.
- [ ] Test exact ordering, missing numbers, zero-row initialization, duplicate metadata rows, a mismatched `--from`, concurrent-lock refusal, dirty refusal, role setup, stdin piping, `0 -> 109`, `108 -> 107 -> 108`, argument rejection, and refusal of `tradingagent` in `scripts/apply-migrations-psql_test.sh`. Inject a failure into a copied migration set, require the prior version with `dirty=true`, require rerun refusal, recreate the disposable DB, and prove `0 -> 109` ends at exactly `109|f`. Run `chmod +x scripts/apply-migrations-psql.sh scripts/apply-migrations-psql_test.sh`, `bash scripts/apply-migrations-psql_test.sh`, and `shellcheck scripts/apply-migrations-psql.sh scripts/apply-migrations-psql_test.sh`.
- [ ] Commit the enforcement-ready 108/109 artifact before any rehearsal command below: `git add migrations/000109_enforce_canonical_account.up.sql migrations/000109_enforce_canonical_account.down.sql migrations/000109_enforce_canonical_account_test.go internal/repository/postgres/schema_version.go internal/repository/postgres/schema_version_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/tradingagent/schema_version_sync_test.go cmd/augr-economic/main.go cmd/augr-evidence/main.go internal/cli/root_test.go internal/runcontrol/group_test.go scripts/apply-migrations-psql.sh scripts/apply-migrations-psql_test.sh && git commit -m 'feat(db): enforce canonical account writes'`.
- [ ] Rehearse graceful SIGTERM against a disposable schema-108 database. Disable every automation job through existing `POST /api/v1/automation/jobs/{name}/enable` with `{"enabled":false}` and activate the existing `POST /api/v1/risk/killswitch` with `{"active":true,"reason":"canonical cutover drain"}`. Then stop only app with the current Compose stop path. Do not infer sessions from `application_name`:

```bash
: "${AUGR_API_BASE_URL:?set the disposable rehearsal API URL}"
: "${CANARY_JWT:?set the rehearsal admin token}"
: "${TARGET_DB_NAME:?set the disposable database name}"
curl --fail --silent --show-error -H "Authorization: Bearer $CANARY_JWT" "$AUGR_API_BASE_URL/api/v1/automation/status" | jq -r '.[].name' | while IFS= read -r job; do curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" -H 'Content-Type: application/json' --data '{"enabled":false}' "$AUGR_API_BASE_URL/api/v1/automation/jobs/$job/enable" >/dev/null; done
curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" -H 'Content-Type: application/json' --data '{"active":true,"reason":"canonical cutover drain"}' "$AUGR_API_BASE_URL/api/v1/risk/killswitch" >/dev/null
docker compose --env-file .env -f docker-compose.nuc.yml stop -t 300 app
test -z "$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app)"
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$TARGET_DB_NAME" -At <<'SQL' | grep -qx '0|0|0'
SET ROLE augr_db_owner;
SELECT (SELECT count(*) FROM pipeline_runs WHERE status='running'), (SELECT count(*) FROM automation_job_runs WHERE status='running'), (SELECT count(*) FROM account_projection_outbox WHERE status='processing');
SQL
```

- [ ] Run the migration-108 old-writer fixture before applying 109 to the disposable rehearsal database. Apply 109 only if all new scoped rows pass the account and graph audit.
- [ ] Apply only migration 109 after fixture workers stop:

```bash
: "${TARGET_DB_NAME:?set a disposable TARGET_DB_NAME}"
test "$TARGET_DB_NAME" != tradingagent
./scripts/apply-migrations-psql.sh --database "$TARGET_DB_NAME" --from 108 --to 109
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$TARGET_DB_NAME" -At -c "SELECT version, dirty FROM schema_migrations" | grep -qx '109|f'
```
- [ ] Run `go test ./migrations -run 'TestCanonicalAccount(ExpansionOldWriterCanary|Enforcement)' -count=1`, `go test ./...`, and `go build ./cmd/tradingagent ./cmd/augr-economic ./cmd/augr-evidence`.
- [ ] After migration 109 succeeds, set the accepted schema minimum and maximum to 109 in all three binaries. Run `go test ./internal/repository/postgres ./cmd/tradingagent ./cmd/augr-economic ./cmd/augr-evidence` and `go build ./cmd/tradingagent ./cmd/augr-economic ./cmd/augr-evidence`.
- [ ] Commit the exact release gate: `git add internal/repository/postgres/schema_version.go internal/repository/postgres/schema_version_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/augr-economic/main.go cmd/augr-evidence/main.go && git commit -m 'chore(db): require enforced schema'`.

## Phase B: fixed-account APIs and frontend

### Task 11: Enforce one server-bound account at the API

**Authorization decision:** v1 has one canonical account. Existing JWT claims contain only `sub` and token type. Existing API keys have no owner. Do not invent partial RBAC or add `account_user_access`. Every authenticated JWT or valid API key can access only the account loaded from `PROJECTION_ACCOUNT_ID`. A route account mismatch returns 404. Existing admin checks remain on global administrative mutations.

**Files:**
- Modify: `internal/api/auth.go`
- Modify: `internal/api/auth_test.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `internal/api/strategy_handlers.go`
- Modify: `internal/api/journal_handlers.go`
- Modify: `internal/api/portfolio_handlers.go`
- Modify: `internal/api/portfolio_projection_test.go`
- Modify: `internal/api/portfolio_allocator_handlers.go`
- Modify: `internal/api/portfolio_allocator_handlers_test.go`
- Modify: `internal/api/risk_handlers.go`
- Modify: `internal/api/risk_handlers_test.go`
- Modify: `internal/api/risk_cockpit_handlers.go`
- Modify: `internal/api/risk_cockpit_handlers_test.go`
- Modify: `internal/api/run_handlers.go`
- Modify: `internal/api/order_handlers.go`
- Modify: `internal/api/trade_handlers.go`
- Modify: `internal/api/event_handlers.go`
- Modify: `internal/api/replay_handlers.go`
- Modify: `internal/api/replay_handlers_test.go`
- Modify: `internal/api/copy_trading_handlers.go`
- Modify: `internal/api/report_handlers.go`
- Modify: `internal/api/report_handlers_test.go`
- Modify: `internal/repository/postgres/report_artifact.go`
- Modify: `internal/repository/postgres/report_artifact_test.go`
- Modify: `internal/repository/postgres/paper_evaluation_scope.go`
- Modify: `internal/repository/postgres/paper_evaluation_scope_test.go`
- Modify: `internal/api/conversation_handlers.go`
- Modify: `internal/api/conversation_handlers_test.go`
- Modify: `internal/api/memory_handlers.go`
- Modify: `internal/api/memory_handlers_test.go`
- Modify: `internal/api/polymarket_handlers.go`
- Create: `internal/api/polymarket_handlers_test.go`
- Modify: `internal/api/economic_handlers.go`
- Modify: `internal/api/economic_handlers_test.go`
- Modify: `internal/api/websocket.go`
- Modify: `internal/api/websocket_test.go`
- Modify: `internal/api/hub.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `cmd/tradingagent/prod_strategy_runner_test.go`
- Modify: `cmd/tradingagent/smoke_test.go`
- Modify: `internal/cli/dashboard.go`
- Create: `internal/cli/dashboard_test.go`
- Modify: `internal/cli/tui/websocket.go`
- Modify: `internal/cli/tui/websocket_test.go`
- Modify: `internal/cli/tui/model.go`
- Modify: `internal/cli/tui/model_test.go`

- [ ] Add `GET /api/v1/me/accounts`. It returns exactly the configured account after authentication.
- [ ] Register these account paths. Preserve current resource shapes:
  - `GET /api/v1/accounts/{accountID}/portfolio/positions`
  - `GET /api/v1/accounts/{accountID}/portfolio/positions/open`
  - `GET /api/v1/accounts/{accountID}/portfolio/summary`
  - `GET /api/v1/accounts/{accountID}/portfolio/allocator/diagnostics`
  - `GET /api/v1/accounts/{accountID}/portfolio/allocator/opportunities`
  - `GET /api/v1/accounts/{accountID}/portfolio/allocator/decisions`
  - `GET /api/v1/accounts/{accountID}/portfolio/allocator/summary`
  - `GET /api/v1/accounts/{accountID}/runs`, `GET /api/v1/accounts/{accountID}/runs/{id}?trade_date=YYYY-MM-DD`, `GET /api/v1/accounts/{accountID}/runs/{id}/decisions?trade_date=YYYY-MM-DD`, `POST /api/v1/accounts/{accountID}/runs/{id}/cancel?trade_date=YYYY-MM-DD`, and `GET /api/v1/accounts/{accountID}/runs/{id}/snapshot?trade_date=YYYY-MM-DD`
  - `GET /api/v1/accounts/{accountID}/orders` and `GET /api/v1/accounts/{accountID}/orders/{id}`
  - `GET /api/v1/accounts/{accountID}/trades`
  - `GET /api/v1/accounts/{accountID}/journal/decisions` and `GET /api/v1/accounts/{accountID}/journal/decisions/{id}`
  - `GET /api/v1/accounts/{accountID}/replay/decisions/{id}`
  - `GET /api/v1/accounts/{accountID}/events`
  - `GET /api/v1/accounts/{accountID}/copy-trading/leaders`, `POST /api/v1/accounts/{accountID}/copy-trading/leaders`, and `GET /api/v1/accounts/{accountID}/copy-trading/leaders/{id}`
  - `POST /api/v1/accounts/{accountID}/copy-trading/leaders/{id}/sources`, `POST /api/v1/accounts/{accountID}/copy-trading/sources/{id}/refresh`, and `PUT /api/v1/accounts/{accountID}/copy-trading/mappings`
  - `GET /api/v1/accounts/{accountID}/copy-trading/subscriptions`, `POST /api/v1/accounts/{accountID}/copy-trading/subscriptions`, `GET /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}`, and `PUT /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}`
  - `POST /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}/preview`, `POST /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}/activate`, `POST /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}/pause`, `POST /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}/resume`, `POST /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}/stop`, and `POST /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}/rebalance`
  - `GET /api/v1/accounts/{accountID}/copy-trading/subscriptions/{id}/intents`
  - `GET /api/v1/accounts/{accountID}/risk/status` and `GET /api/v1/accounts/{accountID}/risk/cockpit`
  - `GET /api/v1/accounts/{accountID}/economic/account`
  - `GET /api/v1/accounts/{accountID}/economic/capital-flows`
  - `GET /api/v1/accounts/{accountID}/economic/capital-summary`
  - `GET /api/v1/accounts/{accountID}/economic/ledger-transactions/{id}`
  - `GET /api/v1/accounts/{accountID}/strategies/{strategyID}/reports` and `GET /api/v1/accounts/{accountID}/strategies/{strategyID}/reports/latest`
  - `GET /api/v1/accounts/{accountID}/conversations`, `POST /api/v1/accounts/{accountID}/conversations`, `GET /api/v1/accounts/{accountID}/conversations/{id}/messages`, and `POST /api/v1/accounts/{accountID}/conversations/{id}/messages`
  - `GET /api/v1/accounts/{accountID}/memories`, `POST /api/v1/accounts/{accountID}/memories/search`, and `DELETE /api/v1/accounts/{accountID}/memories/{id}`
- [ ] Require `trade_date` in ISO `YYYY-MM-DD` form on every account run detail, decisions, cancel, and snapshot request. Parse it before repository access and construct `domain.PipelineRunRef{ID: id, TradeDate: tradeDate}`. Missing or malformed `trade_date` returns 400. A valid `(id, trade_date)` owned by another account or not found returns 404. List responses include `trade_date` so clients can construct the key.
- [ ] Register `POST /api/v1/accounts/{accountID}/strategies/{strategyID}/run`, `/pause`, `/resume`, and `/skip-next`. Register `POST /api/v1/accounts/{accountID}/paper-evaluation-scopes`. Require the route account to match `PROJECTION_ACCOUNT_ID`; require the paper-evaluation scope body account to match both values.
- [ ] Register `GET /api/v1/accounts/{accountID}/paper-evaluation-scopes`. It returns only scopes owned by the route account, newest first, with `id`, `account_id`, `label`, and immutable evidence digest fields. The frontend uses a selected returned `id` as `evidence_scope_id`; it never invents or infers one.
- [ ] Keep report identity strategy-specific. Do not add `/reports` without `{strategyID}`. Do not add `/fills`. Remove `legacy=legacy_unscoped` and `account_id` query handling from `reportScopeFilter`.
- [ ] Require `evidence_scope_id` on both report routes. The exact read key is `(route account_id, evidence_scope_id, strategy_id, report_type)`. `ReportArtifactStore.List` receives all four values. PostgreSQL joins `report_artifacts.scope_id = paper_evaluation_scopes.id`, requires `paper_evaluation_scopes.account_id = route account_id`, and requires the artifact strategy ID to equal the route strategy ID. Missing evidence scope is 400. A scope from another account or an artifact from another strategy is 404. Legacy `scope_id IS NULL` artifacts never appear in canonical APIs.
- [ ] For economic and ledger detail handlers, load through the configured account repository before returning data. A capital flow or ledger transaction from another account returns 404.
- [ ] Keep only `/api/v1/strategies` research list, create, get, update, and delete global. Delete registrations for global strategy run, pause, resume, skip-next, and reports. Keep `/api/v1/risk/killswitch`, `/api/v1/risk/breaker/reset`, `/api/v1/risk/market/{type}/stop`, `/api/v1/risk/market/{type}/resume`, `/api/v1/settings`, and every `/api/v1/automation` route global with its existing admin policy.
- [ ] Delete every old global operational registration from `internal/api/server.go`: `/api/v1/runs`, `/api/v1/orders`, `/api/v1/trades`, `/api/v1/journal/decisions`, `/api/v1/replay/decisions`, `/api/v1/events`, `/api/v1/copy-trading`, `/api/v1/portfolio`, `/api/v1/risk/status`, `/api/v1/risk/cockpit`, `/api/v1/economic`, `/api/v1/paper-evaluation-scopes`, `/api/v1/conversations`, `/api/v1/memories`, and strategy report routes. Add server tests that each old path returns 404 and each account path reaches its handler.
- [ ] Run `rg -n 'v1\.(Get|Post|Put|Delete)|v1\.Route' internal/api/server.go` and inspect every registration. The old operational prefixes above must be absent. Global strategy registration may contain only list, create, get, update, and delete; all account mutations and reports must appear under `/accounts/{accountID}`.
- [ ] Classify `internal/api/polymarket_handlers.go` as global provider research and system administration. Keep its existing `/api/v1/polymarket` routes global, but preserve their existing auth and admin checks. `PublishPolymarketEvent` emits `scope: "system"`; it cannot emit an account event. Canonical Polymarket orders, trades, positions, settlements, and reports use the account routes above.
- [ ] Require `account_id` on `/ws` before upgrade. Compare it to the configured account. Add `AccountID` and `Scope` to `WSMessage`; `Hub` and `Client` route account events only to the matching client. Global events carry `scope: "system"`. Update every producer in `internal/api/server.go`, `internal/api/polymarket_handlers.go`, and `cmd/tradingagent/prod_strategy_runner.go`.
- [ ] Update every non-browser client. `cmd/tradingagent/smoke_test.go:openSmokeWebSocket`, `internal/cli/dashboard.go:websocketURL`, and `internal/cli/tui/websocket.go` append `account_id={configured account}`. The CLI first calls `/api/v1/me/accounts`, requires one account, then opens the socket. Update `internal/cli/dashboard_test.go`, `internal/cli/tui/websocket_test.go`, `internal/cli/tui/model_test.go`, `internal/api/websocket_test.go`, and producer serialization tests. Runtime wiring passes the immutable configured account to the hub and production runner.
- [ ] Test JWT and API-key access, a random account, a second real account, cross-account entity IDs, legacy NULL rows, and WebSocket isolation. Every account mismatch is 404, including the membership check itself. Unauthenticated requests remain 401.
- [ ] Run `go test -race ./internal/api` and `go build ./cmd/tradingagent`.
- [ ] Update deployment canaries to call the account-bound strategy run route and account-bound paper-evaluation-scope route. No canary calls an old global operational path.
- [ ] Commit: `git add internal/api/auth.go internal/api/auth_test.go internal/api/server.go internal/api/server_test.go internal/api/strategy_handlers.go internal/api/portfolio_handlers.go internal/api/portfolio_projection_test.go internal/api/portfolio_allocator_handlers.go internal/api/portfolio_allocator_handlers_test.go internal/api/risk_handlers.go internal/api/risk_handlers_test.go internal/api/risk_cockpit_handlers.go internal/api/risk_cockpit_handlers_test.go internal/api/run_handlers.go internal/api/order_handlers.go internal/api/trade_handlers.go internal/api/event_handlers.go internal/api/journal_handlers.go internal/api/replay_handlers.go internal/api/replay_handlers_test.go internal/api/copy_trading_handlers.go internal/api/report_handlers.go internal/api/report_handlers_test.go internal/api/conversation_handlers.go internal/api/conversation_handlers_test.go internal/api/memory_handlers.go internal/api/memory_handlers_test.go internal/api/polymarket_handlers.go internal/api/polymarket_handlers_test.go internal/api/economic_handlers.go internal/api/economic_handlers_test.go internal/api/websocket.go internal/api/websocket_test.go internal/api/hub.go internal/repository/postgres/report_artifact.go internal/repository/postgres/report_artifact_test.go internal/repository/postgres/paper_evaluation_scope.go internal/repository/postgres/paper_evaluation_scope_test.go cmd/tradingagent/runtime.go cmd/tradingagent/runtime_test.go cmd/tradingagent/prod_strategy_runner.go cmd/tradingagent/prod_strategy_runner_test.go cmd/tradingagent/smoke_test.go internal/cli/dashboard.go internal/cli/dashboard_test.go internal/cli/tui/websocket.go internal/cli/tui/websocket_test.go internal/cli/tui/model.go internal/cli/tui/model_test.go && git commit -m 'feat(api): bind routes to execution account'`.

### Task 12: Cut the frontend to the fixed account

**Files:**
- Create: `web/src/shared/account/AccountProvider.tsx`
- Create: `web/src/shared/account/AccountProvider.test.tsx`
- Modify: `web/src/App.test.tsx`
- Modify: `web/src/App.auth-cockpit.test.tsx`
- Modify: `web/src/App.product-surfaces.test.tsx`
- Modify: `web/src/App.risk.test.tsx`
- Modify: `web/src/App.overhaul.test.tsx`
- Modify: `web/src/app/providers/AppProviders.tsx`
- Create: `web/src/app/providers/AppProviders.test.tsx`
- Modify: `web/src/app/router/router.tsx`
- Modify: `web/src/app/router/RouteStatePages.tsx`
- Modify: `web/src/app/layout/AppShell.tsx`
- Modify: `web/src/components/CommandPalette.tsx`
- Modify: `web/src/shared/components/EntityLinks.tsx`
- Modify: `web/src/shared/api/endpoints.ts`
- Modify: `web/src/shared/api/schemas.ts`
- Modify: `web/src/shared/api/schemas.test.ts`
- Modify: `web/src/shared/query/keys.ts`
- Modify: `web/src/shared/types/api.ts`
- Modify: `web/src/shared/types/domain.ts`
- Modify: `web/src/shared/types/index.ts`
- Modify: `web/src/shared/types/websocket.ts`
- Modify: `web/src/shared/websocket/RealtimeProvider.tsx`
- Modify: `web/src/test/app-harness.ts`
- Modify: `web/src/test/fixtures/builders.ts`
- Modify: `web/src/test/fixtures/builders.test.ts`
- Modify: `web/src/test/fixtures/ids.ts`
- Modify: `web/src/test/mocks/rest.ts`
- Modify: `web/src/test/mocks/rest.test.ts`
- Modify: `web/src/test/mocks/scenarios.ts`
- Modify: `web/src/test/mocks/websocket.ts`
- Modify: `web/src/test/mocks/websocket.test.ts`
- Modify: `web/src/features/cockpit/CockpitPage.tsx`
- Modify: `web/src/features/portfolio/PortfolioPage.tsx`
- Modify: `web/src/features/risk/RiskPage.tsx`
- Modify: `web/src/features/runs/RunsListPage.tsx`
- Modify: `web/src/features/runs/RunDetailPage.tsx`
- Modify: `web/src/features/orders/OrdersListPage.tsx`
- Modify: `web/src/features/orders/OrderDetailPage.tsx`
- Modify: `web/src/features/trades/TradesListPage.tsx`
- Modify: `web/src/features/journal/JournalPage.tsx`
- Modify: `web/src/features/journal/ReplayPage.tsx`
- Modify: `web/src/features/events/EventsPage.tsx`
- Modify: `web/src/features/events/EventTimeline.tsx`
- Modify: `web/src/features/copy-trading/CopyTradingPage.tsx`
- Modify: `web/src/features/automation/AutomationPage.tsx`
- Modify: `web/src/features/automation/AutomationDetailPage.tsx`
- Modify: `web/src/features/settings/SettingsPage.tsx`
- Modify: `web/src/features/stock/StockPage.tsx`
- Modify: `web/src/features/strategies/StrategyDetailPage.tsx`
- Modify: `web/src/features/strategies/StrategyCreatePage.tsx`
- Modify: `web/src/features/strategies/StrategyEditPage.tsx`
- Modify: `web/src/features/options/OptionsPage.tsx`
- Modify: `web/src/features/event-markets/EventMarketsPage.tsx`
- Modify: `web/src/features/backtests/BacktestsPage.tsx`
- Modify: `web/src/features/auth-login/LoginPage.tsx`
- Create: `web/src/features/system-safety/SystemSafetyPage.tsx`
- Create: `web/src/features/system-safety/SystemSafetyPage.test.tsx`
- Delete: `web/src/features/overhaul/OverhaulPage.tsx`

- [ ] `AccountProvider` loads `/api/v1/me/accounts` and requires exactly one account. Zero or multiple accounts is a blocking configuration error. There is no account switcher in v1. Remove old account-switch tests and replace them with tests that reject a URL account different from the sole server account.
- [ ] In `web/src/app/providers/AppProviders.tsx`, place `AccountProvider` inside `AuthProvider` and outside `RealtimeProvider`. Replace `RealtimeBridge` so it reads the resolved account and opens `/ws?account_id={id}` only after both authentication and account resolution succeed. `AppProviders.test.tsx` proves no socket opens while account loading fails or returns the wrong cardinality.
- [ ] Put account routes below `/accounts/:accountId`. This includes `/accounts/:accountId/cockpit`, Portfolio, account Risk, runs, orders, trades, journal, replay, events, copy trading, stock execution context, and strategy reports. Redirect operational legacy URLs to the sole account only after account resolution. Keep global strategy research and system routes outside that tree.
- [ ] Make account query keys start with `['accounts', accountId]`. Do not add a fills key. Reports use `['accounts', accountId, 'evidence-scopes', evidenceScopeId, 'strategies', strategyId, 'reports', filters]`. `StrategyDetailPage` loads the account's paper-evaluation scopes, requires an explicit selected scope, and sends `evidence_scope_id` on latest and history requests. Delete `legacyReportScope` and every `legacy_unscoped` request.
- [ ] Change `EntityLinks.hrefFor` to accept the account ID for run, order, trade, position, decision, event, opportunity, and risk links. Strategy research links remain `/strategies/{id}`; report links use the account-nested strategy report route.
- [ ] Change every run link and run query key to carry `trade_date`. Use `/accounts/{accountId}/runs/{runId}?trade_date={YYYY-MM-DD}` in `EntityLinks`, breadcrumbs, event links, order links, and list-to-detail navigation. `RunDetailPage` preserves the query value when it requests decisions, snapshot, or cancel. Tests cover two rows with the same run UUID on different dates and prove that navigation and all follow-up requests retain the selected date.
- [ ] Change `StockPage` so position, trade, and run requests use the account. Strategy lookup stays global. Route account execution links through `EntityLinks`.
- [ ] Update Cockpit, Portfolio, account Risk, runs, orders, trades, journal, replay, events, copy, StockPage, StrategyDetailPage reports, AppShell navigation, CommandPalette actions, and breadcrumbs. Keep AutomationPage, AutomationDetailPage, SettingsPage, OptionsPage, EventMarketsPage, BacktestsPage, StrategyCreatePage, and StrategyEditPage global, but make every Cockpit return link use the resolved `/accounts/{accountId}/cockpit`. Update `RouteStatePages.tsx` so both error links use the resolved account. `LoginPage` redirects to `/`; account resolution then chooses the account Cockpit.
- [ ] Create global `/system/safety` owned by `web/src/features/system-safety/SystemSafetyPage.tsx`. Move the existing global kill switch, breaker reset, market stop and resume, automation job enable controls, and automation status from `RiskPage` to this page. Do not add dispatch, drain, or shutdown APIs. `RiskPage` owns only account-scoped risk status and cockpit evidence. `AppShell` places **System Safety** in its existing System group. Add route, component, API, query-key, MSW, and state tests.
- [ ] Inspect and update every current Cockpit link in these exact files: `web/src/app/router/router.tsx`, `web/src/app/router/RouteStatePages.tsx`, `web/src/app/layout/AppShell.tsx`, `web/src/components/CommandPalette.tsx`, `web/src/features/settings/SettingsPage.tsx`, `web/src/features/options/OptionsPage.tsx`, `web/src/features/event-markets/EventMarketsPage.tsx`, `web/src/features/runs/RunDetailPage.tsx`, `web/src/features/copy-trading/CopyTradingPage.tsx`, `web/src/features/events/EventsPage.tsx`, `web/src/features/backtests/BacktestsPage.tsx`, `web/src/features/auth-login/LoginPage.tsx`, `web/src/features/stock/StockPage.tsx`, `web/src/features/portfolio/PortfolioPage.tsx`, `web/src/features/orders/OrderDetailPage.tsx`, `web/src/features/risk/RiskPage.tsx`, `web/src/features/orders/OrdersListPage.tsx`, `web/src/features/trades/TradesListPage.tsx`, `web/src/features/journal/JournalPage.tsx`, `web/src/features/strategies/StrategyCreatePage.tsx`, `web/src/features/strategies/StrategyEditPage.tsx`, and `web/src/features/strategies/StrategyDetailPage.tsx`. Update the matching assertions in `web/src/App.test.tsx` and `web/src/App.auth-cockpit.test.tsx`.
- [ ] Keep global Safety separate from account Risk. Delete `/overhaul`; move its account ledger content to Portfolio and strategy Reports. Do not claim a fills page exists.
- [ ] Test populated, genuine empty, filtered empty, loading, validation error, transport error, stale, degraded, unavailable, reconciliation failure, account mismatch, and unknown mutation outcome states on every requested operational page.
- [ ] Run this search gate:

```bash
rg -n "legacy_unscoped|/overhaul|['\"]/(cockpit|portfolio|risk|runs|orders|trades|journal|events|copy-trading|replay|stock)(['\"/?])|/api/v1/(portfolio|risk/status|risk/cockpit|runs|orders|trades|journal|events|copy-trading|replay|economic|paper-evaluation-scopes)|/strategies/.*/(run|pause|resume|skip-next|reports)|/paper-evaluation-scopes|getPortfolioSummary\(|getOpenPortfolioPositions\(|getRiskCockpit\(|getRuns\(|getOrders\(|getTrades\(|getEvents\(|getCopy|Replay|Stock|hrefFor\(" web/src
```

  Inspect every match. Only account-nested builders, explicit migration redirects, global system functions prefixed `getSystem`, and global strategy research requests may remain.
- [ ] From `web/`, run `npm test -- --run`, `npm run lint`, and `npm run build`.
- [ ] Commit: `git add web/src/shared/account/AccountProvider.tsx web/src/shared/account/AccountProvider.test.tsx web/src/App.test.tsx web/src/App.auth-cockpit.test.tsx web/src/App.product-surfaces.test.tsx web/src/App.risk.test.tsx web/src/App.overhaul.test.tsx web/src/app/providers/AppProviders.tsx web/src/app/providers/AppProviders.test.tsx web/src/app/router/router.tsx web/src/app/router/RouteStatePages.tsx web/src/app/layout/AppShell.tsx web/src/components/CommandPalette.tsx web/src/shared/components/EntityLinks.tsx web/src/shared/api/endpoints.ts web/src/shared/api/schemas.ts web/src/shared/api/schemas.test.ts web/src/shared/query/keys.ts web/src/shared/types/api.ts web/src/shared/types/domain.ts web/src/shared/types/index.ts web/src/shared/types/websocket.ts web/src/shared/websocket/RealtimeProvider.tsx web/src/test/app-harness.ts web/src/test/fixtures/builders.ts web/src/test/fixtures/builders.test.ts web/src/test/fixtures/ids.ts web/src/test/mocks/rest.ts web/src/test/mocks/rest.test.ts web/src/test/mocks/scenarios.ts web/src/test/mocks/websocket.ts web/src/test/mocks/websocket.test.ts web/src/features/cockpit/CockpitPage.tsx web/src/features/portfolio/PortfolioPage.tsx web/src/features/risk/RiskPage.tsx web/src/features/runs/RunsListPage.tsx web/src/features/runs/RunDetailPage.tsx web/src/features/orders/OrdersListPage.tsx web/src/features/orders/OrderDetailPage.tsx web/src/features/trades/TradesListPage.tsx web/src/features/journal/JournalPage.tsx web/src/features/journal/ReplayPage.tsx web/src/features/events/EventsPage.tsx web/src/features/events/EventTimeline.tsx web/src/features/copy-trading/CopyTradingPage.tsx web/src/features/automation/AutomationPage.tsx web/src/features/automation/AutomationDetailPage.tsx web/src/features/settings/SettingsPage.tsx web/src/features/stock/StockPage.tsx web/src/features/options/OptionsPage.tsx web/src/features/event-markets/EventMarketsPage.tsx web/src/features/backtests/BacktestsPage.tsx web/src/features/auth-login/LoginPage.tsx web/src/features/strategies/StrategyCreatePage.tsx web/src/features/strategies/StrategyEditPage.tsx web/src/features/strategies/StrategyDetailPage.tsx web/src/features/system-safety/SystemSafetyPage.tsx web/src/features/system-safety/SystemSafetyPage.test.tsx web/src/features/overhaul/OverhaulPage.tsx && git commit -m 'feat(web): use fixed account routes'`.

## Phase C: cutover tooling and release proof

### Task 13: Create and commit the atomic DB-target updater

**Files:**
- Create: `scripts/update-db-targets.sh`
- Create: `scripts/update-db-targets_test.sh`
- Modify: `scripts/release-gate.sh`
- Create: `deploy/docker-compose.nuc.scheduler-paused.yml`

- [ ] Implement four mutually exclusive modes. `--prepare-rollback` requires only `--env-file`. It creates the first and only sibling `.canonical-cutover.rollback`, records its SHA-256 and mode in `.canonical-cutover.rollback.meta`, validates the four original DB keys, and changes neither `.env` nor a DB. Default update mode requires `--env-file`, `--postgres-db`, `--app-database-url-fd`, `--database-url-fd`, and `--kalshi-projection-database-url-fd`. `--validate` requires only `--env-file` and makes no write. `--restore` requires only `--env-file`, validates the sibling rollback copy, restores it atomically, and preserves the current file as `.canonical-cutover.pre-restore`. Reject a mode flag combined with any update-only flag.
- [ ] Resolve the repository root from the script path with `git -C "$SCRIPT_DIR/.." rev-parse --show-toplevel`. Require `realpath -- "$env_file"` to equal `realpath -- "$REPO_ROOT/.env"`. Run `docker compose --env-file "$REPO_ROOT/.env" -f "$REPO_ROOT/docker-compose.nuc.yml" config --quiet` before reading or writing. This is the only production env path; do not accept `/tmp`, a caller-selected NUC path, or a Compose-default env file from another working directory.
- [ ] Read each URL with `IFS= read -r value <&"$fd"`. Never print values. Require each target key exactly once. `--prepare-rollback` creates the rollback file with mode `0600` via `O_CREAT|O_EXCL`, writes metadata containing mode `600`, the rollback SHA-256, the non-target digest, and hashes of the exact original values for `POSTGRES_DB`, `APP_DATABASE_URL`, `DATABASE_URL`, and `KALSHI_PROJECTION_DATABASE_URL`, then `fsync`s both files and the parent directory. Update and restore open both artifacts read-only and fail unless every checksum, mode, and four-key hash matches. Neither mode truncates, renames over, chmods, or rewrites either rollback artifact.
- [ ] Replace exactly `POSTGRES_DB`, `APP_DATABASE_URL`, `DATABASE_URL`, and `KALSHI_PROJECTION_DATABASE_URL`. Compare a SHA-256 digest of all non-target lines before and after.
- [ ] Test `--prepare-rollback`, default, `--validate`, and `--restore`. Cover duplicate keys, missing keys, invalid FD, wrong DB name, same general and projection username, interrupted write, rollback mode, exclusive first creation, second-prepare rejection, checksum mismatch, mode mismatch, each of the four original-key mismatches, rollback and metadata non-overwrite by update and restore, pre-restore copy, exact four-key replacement, non-repo env rejection, conflicting modes, validation with no mutation, restore with no secret FDs, and secret absence from stdout, stderr, and `ps` output.
- [ ] Add `scripts/update-db-targets_test.sh` and `shellcheck scripts/update-db-targets.sh scripts/update-db-targets_test.sh` to `scripts/release-gate.sh`.
- [ ] Add this tested deployment override so canary restarts cannot dispatch strategies:

```yaml
services:
  app:
    environment:
      ENABLE_SCHEDULER: "false"
```

- [ ] Run `docker compose -f docker-compose.nuc.yml -f deploy/docker-compose.nuc.scheduler-paused.yml config` and require `ENABLE_SCHEDULER: "false"` for `services.app`.
- [ ] Run `bash scripts/update-db-targets_test.sh`, `shellcheck scripts/update-db-targets.sh scripts/update-db-targets_test.sh`, and `git diff --check`.
- [ ] Commit before Task 14: `git add scripts/update-db-targets.sh scripts/update-db-targets_test.sh scripts/release-gate.sh deploy/docker-compose.nuc.scheduler-paused.yml && git commit -m 'feat(ops): update DB targets atomically'`.

### Task 14: Run the release gate

**Files:**
- Create: `scripts/verify-account-cutover.sh`
- Create: `scripts/capture-old-db-baseline.sh`
- Create: `scripts/verify-old-db-after-drain.sh`
- Create: `docs/runbooks/canonical-account-cutover.md`
- Modify: `scripts/release-gate.sh`
- Modify: `scripts/verify-prod-build.sh`
- Modify: `cmd/tradingagent/prod_build_verification_test.go`

- [ ] Make the verifier create disposable schema-107 and schema-0 databases with `docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres`, then migrate them with `scripts/apply-migrations-psql.sh`. Every owner operation starts with `SET ROLE augr_db_owner;`. Do not accept a host, DSN, password, TLS option, alternate Compose file, or mount. Prefix generated databases with `augr_cutover_fixture_` and drop them on success or failure.
- [ ] Give the verifier five exact, mutually exclusive modes: `--schema-matrix`, `--writer-fixtures`, `--api-matrix`, `--target-zero-history-audit`, and `--target-graph-audit`. Reject invocation without one mode and reject multiple modes. Do not add `--all` or a default.
- [ ] `--schema-matrix`, `--writer-fixtures`, and `--api-matrix` create and mutate only disposable `augr_cutover_fixture_*` databases. `--writer-fixtures` runs stock, options, Kalshi, Polymarket, copy, settlement, restart, and risk fixtures without live provider calls. It rejects `TARGET_DB_NAME` and any database not created by the current verifier process.
- [ ] `--target-zero-history-audit` requires `TARGET_DB_NAME`, starts `BEGIN READ ONLY`, and checks schema 109, owner, grants, the one seeded account/profile, zero strategies, zero operational rows, zero outbox rows, zero checkpoints, and no cross-account rows. Run it only before the first canary. `--target-graph-audit` requires `TARGET_DB_NAME`, `TARGET_ACCOUNT_ID`, `TARGET_RUN_ID`, and `TARGET_RUN_TRADE_DATE`. It starts `BEGIN READ ONLY`, requires exactly one matching pipeline-run row, and rejects a run owned by another account. It checks non-NULL scope, complete `(id, trade_date)`, copy-run linkage, balanced ledgers, and every order, trade, execution intent, execution order, fill, normalization, ledger transaction, outbox row, and checkpoint reachable from that exact run. Require at least the run plus one decision or terminal event; if the run produced an accepted fill, require nonzero common lifecycle, normalization, ledger, and outbox counts. Every reachable outbox row must be `completed` or `degraded`, and completed rows require a valid signed checkpoint at the same frontier. Neither mode runs fixtures or invokes a side-effecting function. Tests include a nonexistent run, wrong trade date, wrong account, a vacuous run graph, foreign rows, and valid no-fill and fill graphs. Tests snapshot every table count and sequence state before and after each mode and require equality.
- [ ] Add `scripts/capture-old-db-baseline.sh` and `scripts/verify-old-db-after-drain.sh`. Both accept only `--database tradingagent --record-dir /var/lib/augr-cutover/canonical-20260827`, use the required NUC `compose exec -T postgres psql` command, and never run DDL or DML. The capture script runs before safety controls. In one repeatable-read, read-only transaction, select every top-level public table with `c.relkind IN ('r','p') AND NOT EXISTS (SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid)` and exclude exactly `risk_state`, `automation_job_controls`, `pipeline_runs`, `automation_job_runs`, `agent_events`, and `audit_log`. For every selected table, emit its name, `count(*)`, and `encode(digest(COALESCE(string_agg(to_jsonb(t)::text,E'\n' ORDER BY to_jsonb(t)::text),''),'sha256'),'hex')`. This set includes every strategy, financial, economic, order, position, trade, ledger, common-execution, copy, mark, and checkpoint table. Write the schema-107 catalog, table list, counts, and digests to mode-0600 files and protect the manifest with SHA-256. The post-drain script regenerates the table list and requires exact equality before comparing every digest.
- [ ] The old-DB baseline also stores full JSON rows for `risk_state`, `automation_job_controls`, every `pipeline_runs` row with `status='running'`, every `automation_job_runs` row with `status='running'`, all existing `agent_events`, and all existing `audit_log` rows. Store a digest of all non-running run rows. Store invariant digests for all pipeline rows after removing exactly `status`, `signal`, `completed_at`, `error_message`, and `phase_timings`, and for all automation-run rows after removing exactly `status`, `completed_at`, `duration_ns`, `result`, `error`, `last_error_at`, and `consecutive_failures`. The post-drain verifier requires byte-identical schema and protected-table snapshots, no new or deleted pipeline or automation-run IDs, unchanged non-running rows, unchanged invariant digests, and changes only to the listed terminal columns of IDs captured as running. It requires every captured running row to be terminal. It permits new `agent_events` only for a captured running `pipeline_run_id` and only with `event_kind IN ('pipeline_completed','pipeline_failed','pipeline_cancelled')`. It permits new `audit_log` rows only with `event_type='kill_switch.activated'`, `entity_type='system'`, and details reason `canonical cutover drain`. It permits only `automation_job_controls.enabled=false` with `updated_by` and `updated_at` changes, and only `risk_state.kill_switch` plus `risk_state.updated_at` changes; `risk_state.market_kill_switches` must equal baseline. Any other audit, financial, economic, strategy, order, position, trade, copy, mark, checkpoint, new-run, or nonterminal change aborts cutover.
- [ ] Keep `--writer-fixtures` as the only mutating verifier mode. It refuses `TARGET_DB_NAME`, creates its own disposable database, runs fixtures, and drops that database. Make `scripts/release-gate.sh` invoke `--schema-matrix`, `--writer-fixtures`, and `--api-matrix` explicitly. Target audit calls never run from the default release gate.
- [ ] Make `scripts/release-gate.sh` run `bash scripts/apply-migrations-psql_test.sh` and `shellcheck scripts/apply-migrations-psql.sh scripts/apply-migrations-psql_test.sh scripts/update-db-targets.sh scripts/update-db-targets_test.sh scripts/verify-account-cutover.sh`. Update `cmd/tradingagent/prod_build_verification_test.go` to require those exact release-gate lines.
- [ ] Prove 107 to 108 bridge compatibility, old-writer canary behavior, scoped writers on 108, 109 enforcement, 0 to 109 fresh migration, complete `(id, trade_date)` linkage, copy UUID-origin preservation, cross-account denial, projection-failure degradation, API 404 behavior, and WebSocket isolation.
- [ ] Run `go test -race ./...`, `go vet ./...`, `golangci-lint run`, `npm test -- --run`, `npm run lint`, and `npm run build` from their proper directories.
- [ ] Run verifier unit tests and `git diff --check`. Run the self-review checklist below. Fix all failures.
- [ ] Update `scripts/verify-prod-build.sh` and `cmd/tradingagent/prod_build_verification_test.go` so the production smoke requires the seeded `PROJECTION_ACCOUNT_ID`, exercises the exact Phase-C app image, and does not substitute a temporary verifier image for the release artifact. Remove its migration `docker run` and `/migrations` mount. In the isolated Compose project, pipe ordered SQL through `compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`, with the same dirty-version handling as `scripts/apply-migrations-psql.sh`. Keep all NUC commands on the exact `docker-compose.nuc.yml exec -T postgres psql` form.
- [ ] Commit the verifier, old-DB fingerprint scripts, runbook, production-build verification, and release-gate wiring before invoking the clean-tree gate: `git add scripts/verify-account-cutover.sh scripts/capture-old-db-baseline.sh scripts/verify-old-db-after-drain.sh scripts/release-gate.sh scripts/verify-prod-build.sh cmd/tradingagent/prod_build_verification_test.go docs/runbooks/canonical-account-cutover.md && git commit -m 'test(cutover): prove account isolation'`.
- [ ] Require the exact empty status `test "$(git status --porcelain=v1 --untracked-files=all)" = ""`. Then run `bash scripts/update-db-targets_test.sh`, `bash scripts/apply-migrations-psql_test.sh`, `./scripts/verify-account-cutover.sh --schema-matrix`, `./scripts/verify-account-cutover.sh --writer-fixtures`, `./scripts/verify-account-cutover.sh --api-matrix`, `./scripts/release-gate.sh`, and `git diff --check`. Do not edit generated or tracked files during this gate.
- [ ] Before the outage, build the exact clean Phase-C commit into both production images. Tag each image with `canonical-$PHASE_C_SHORT`, capture its content ID, and verify all OCI labels:

```bash
PHASE_C_COMMIT="$(git rev-parse HEAD)"
PHASE_C_SHORT="$(git rev-parse --short=12 HEAD)"
PHASE_C_VERSION="canonical-$PHASE_C_SHORT"
PHASE_C_BUILD_TIME="$(git show -s --format=%cI "$PHASE_C_COMMIT")"
docker buildx build --load --target production --file Dockerfile --tag "augr-app:$PHASE_C_VERSION" --build-arg "BUILD_VERSION=$PHASE_C_VERSION" --build-arg "BUILD_COMMIT=$PHASE_C_COMMIT" --build-arg "BUILD_TIME=$PHASE_C_BUILD_TIME" .
docker buildx build --load --target production --file Dockerfile.web --tag "augr-web:$PHASE_C_VERSION" --build-arg "BUILD_VERSION=$PHASE_C_VERSION" --build-arg "BUILD_COMMIT=$PHASE_C_COMMIT" --build-arg "BUILD_TIME=$PHASE_C_BUILD_TIME" .
PHASE_C_APP_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "augr-app:$PHASE_C_VERSION")"
PHASE_C_WEB_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "augr-web:$PHASE_C_VERSION")"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}|{{ index .Config.Labels "org.opencontainers.image.version" }}|{{ index .Config.Labels "org.opencontainers.image.created" }}' "$PHASE_C_APP_IMAGE_ID")" = "$PHASE_C_COMMIT|$PHASE_C_VERSION|$PHASE_C_BUILD_TIME"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}|{{ index .Config.Labels "org.opencontainers.image.version" }}|{{ index .Config.Labels "org.opencontainers.image.created" }}' "$PHASE_C_WEB_IMAGE_ID")" = "$PHASE_C_COMMIT|$PHASE_C_VERSION|$PHASE_C_BUILD_TIME"
export AUGR_APP_IMAGE="$PHASE_C_APP_IMAGE_ID"
export AUGR_WEB_IMAGE="$PHASE_C_WEB_IMAGE_ID"
test "$(docker compose --env-file .env -f docker-compose.nuc.yml config --images | sort)" = "$(printf '%s\n%s\n%s\n%s\n' "$AUGR_APP_IMAGE" "$AUGR_WEB_IMAGE" 'ghcr.io/anomalyco/opencode:1.17.20@sha256:f5381f02f54777e1ea51a4790be843e76e2d9a0ca3488633c8b71e136f9c590d' 'redis:7-alpine' 'timescale/timescaledb:2.17.2-pg17' | sort)"
test "$(docker compose --env-file .env -f docker-compose.nuc.yml --profile tools config --images | sort)" = "$(printf '%s\n%s\n%s\n%s\n%s\n' "$AUGR_APP_IMAGE" "$AUGR_WEB_IMAGE" 'ghcr.io/anomalyco/opencode:1.17.20@sha256:f5381f02f54777e1ea51a4790be843e76e2d9a0ca3488633c8b71e136f9c590d' 'migrate/migrate:v4.18.3' 'redis:7-alpine' 'timescale/timescaledb:2.17.2-pg17' | sort)"
```

- [ ] Store `AUGR_APP_IMAGE=$PHASE_C_APP_IMAGE_ID`, `AUGR_WEB_IMAGE=$PHASE_C_WEB_IMAGE_ID`, `PHASE_C_COMMIT`, and both tagged names in the protected operator session record. Do not write them to `.env`; exactly four DB target values change on the NUC. Every Phase-D `--no-build` command runs in this exported environment and verifies the resolved app and web images equal the captured content IDs.

## Phase D: fresh database and cutover

### Task 15: Create, migrate, grant, and provision the fresh database

**Files:** No source changes.

- [ ] Record operator approval, the exact Phase C commit, old DB fingerprints, `/healthz`, and backup SHA-256. Record immutable rollback values before stopping containers:

```bash
OLD_APP_CONTAINER="$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app)"
OLD_WEB_CONTAINER="$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q web)"
test -n "$OLD_APP_CONTAINER"
test -n "$OLD_WEB_CONTAINER"
OLD_AUGR_APP_IMAGE="$(docker inspect --format '{{.Config.Image}}' "$OLD_APP_CONTAINER")"
OLD_AUGR_WEB_IMAGE="$(docker inspect --format '{{.Config.Image}}' "$OLD_WEB_CONTAINER")"
OLD_AUGR_APP_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "$OLD_AUGR_APP_IMAGE")"
OLD_AUGR_WEB_IMAGE_ID="$(docker image inspect --format '{{.Id}}' "$OLD_AUGR_WEB_IMAGE")"
test "$(docker inspect --format '{{.Image}}' "$OLD_APP_CONTAINER")" = "$OLD_AUGR_APP_IMAGE_ID"
test "$(docker inspect --format '{{.Image}}' "$OLD_WEB_CONTAINER")" = "$OLD_AUGR_WEB_IMAGE_ID"
ORIGINAL_PROJECTION_ACCOUNT_ID="$PROJECTION_ACCOUNT_ID"
OLD_DB_NAME=tradingagent
: "${AUGR_API_BASE_URL:=http://10.0.0.56:3030}"
: "${CANARY_JWT:?set the authenticated operator canary token}"
: "${AUGR_APP_IMAGE:?export the Phase-C app content ID}"
: "${AUGR_WEB_IMAGE:?export the Phase-C web content ID}"
test "$PHASE_C_COMMIT" = "$(git rev-parse HEAD)"
install -d -m 0700 /var/lib/augr-cutover/canonical-20260827
```
- [ ] Before provisioning the fresh DB, prove the exact deployed schema-107 content image obeys current graceful shutdown: `VERIFY_ROLLBACK_IMAGE="$OLD_AUGR_APP_IMAGE_ID" VERIFY_ROLLBACK_SCHEMA_VERSION=107 ./scripts/verify-prod-build.sh`. The verifier must inspect its rehearsal container and require `.Image == $OLD_AUGR_APP_IMAGE_ID` before sending SIGTERM. It starts admitted pipeline and automation work, sends SIGTERM, and requires cancellation or finalization plus zero running rows before its disposable DB is removed. If it fails, stop and ship the earlier migration-free shutdown fix described in Task 10.
- [ ] Before any outage, create and validate the immutable rollback artifacts: `./scripts/update-db-targets.sh --prepare-rollback --env-file .env && ./scripts/update-db-targets.sh --validate --env-file .env`. Record both artifact SHA-256 values.
- [ ] Create and migrate the new database through the local PostgreSQL container socket:

```bash
set -euo pipefail
umask 077
: "${NEW_DB_NAME:=tradingagent_canonical_20260827}"
test -z "$(docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -At --set=db_name="$NEW_DB_NAME" -c "SELECT 1 FROM pg_database WHERE datname=:'db_name'")"
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres --set=db_name="$NEW_DB_NAME" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER augr_db_owner', :'db_name') \gexec
SQL
./scripts/apply-migrations-psql.sh --database "$NEW_DB_NAME" --from 0 --to 109
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" -At -c "SELECT version, dirty FROM schema_migrations" | grep -qx '109|f'
```

- [ ] Run grants as `augr_db_owner`, with the DB name passed as a psql variable. Set every security-definer owner explicitly:

```bash
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" --set=db_name="$NEW_DB_NAME" <<'SQL'
SET ROLE augr_db_owner;
GRANT CONNECT ON DATABASE :"db_name" TO augr_app_runtime, augr_projection_writer;
GRANT USAGE ON SCHEMA public TO augr_app_runtime, augr_projection_writer;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO augr_app_runtime;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO augr_app_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE augr_db_owner IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO augr_app_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE augr_db_owner IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO augr_app_runtime;
ALTER FUNCTION public.persist_canonical_projection_checkpoint(BYTEA, TEXT, BYTEA) OWNER TO augr_db_owner;
REVOKE ALL ON projection_checkpoint_signing_keys, projection_checkpoint_signing_key_revocations FROM augr_app_runtime, augr_projection_writer;
REVOKE INSERT, UPDATE, DELETE ON projection_checkpoints FROM augr_app_runtime, augr_projection_writer;
REVOKE DELETE, TRUNCATE ON account_projection_outbox FROM augr_app_runtime;
GRANT SELECT, INSERT, UPDATE ON account_projection_outbox TO augr_app_runtime;
REVOKE ALL ON account_projection_outbox FROM augr_projection_writer;
REVOKE ALL ON FUNCTION public.persist_canonical_projection_checkpoint(BYTEA, TEXT, BYTEA) FROM PUBLIC, augr_app_runtime;
GRANT SELECT ON accounts, ledger_transactions, ledger_postings, economic_event_normalizations, venue_contracts, option_contract_terms, instruments, mark_observations, projection_checkpoints TO augr_projection_writer;
GRANT INSERT ON mark_observations TO augr_app_runtime;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON mark_observations FROM augr_projection_writer;
GRANT EXECUTE ON FUNCTION public.persist_canonical_projection_checkpoint(BYTEA, TEXT, BYTEA) TO augr_projection_writer;
SQL
```

- [ ] Reuse the current `KALSHI_PROJECTION_KEY_ID` and `KALSHI_PROJECTION_SECRET_B64`. This preserves the four-target-variable update constraint. Do not rotate or print them. Insert the matching row through psql environment variables and static stdin:

```bash
set +x
export KALSHI_PROJECTION_KEY_ID KALSHI_PROJECTION_SECRET_B64
docker compose --env-file .env -f docker-compose.nuc.yml exec -T -e KALSHI_PROJECTION_KEY_ID -e KALSHI_PROJECTION_SECRET_B64 postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" <<'SQL'
\set ON_ERROR_STOP on
SET ROLE augr_db_owner;
\getenv projection_key_id KALSHI_PROJECTION_KEY_ID
\getenv projection_secret_b64 KALSHI_PROJECTION_SECRET_B64
SELECT octet_length(decode(:'projection_secret_b64', 'base64')) = 32 AS valid_secret \gset
\if :valid_secret
\else
\quit 1
\endif
INSERT INTO projection_checkpoint_signing_keys (key_id, signing_secret, created_by)
VALUES (:'projection_key_id', decode(:'projection_secret_b64', 'base64'), 'canonical-cutover-20260827');
\unset projection_secret_b64
SQL
unset KALSHI_PROJECTION_SECRET_B64
```

- [ ] This matches runtime behavior: `base64.StdEncoding.DecodeString`, exactly 32 decoded bytes, HMAC-SHA256 over domain, key ID, and payload, then `persist_canonical_projection_checkpoint(BYTEA, TEXT, BYTEA)`.
- [ ] Verify owners, roles, and security-definer settings:

```bash
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" -At <<'SQL' | grep -qx 'f|f|f|f|t'
SET ROLE augr_projection_writer;
SELECT has_table_privilege(current_user,'projection_checkpoints','INSERT'), has_table_privilege(current_user,'projection_checkpoint_signing_keys','SELECT'), has_table_privilege(current_user,'account_projection_outbox','SELECT'), has_table_privilege(current_user,'mark_observations','INSERT'), has_function_privilege(current_user,'public.persist_canonical_projection_checkpoint(bytea,text,bytea)','EXECUTE');
SQL
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" -At <<'SQL' | grep -qx 't|t|t|f|f|t'
SET ROLE augr_app_runtime;
SELECT has_table_privilege(current_user,'account_projection_outbox','SELECT'), has_table_privilege(current_user,'account_projection_outbox','INSERT'), has_table_privilege(current_user,'account_projection_outbox','UPDATE'), has_table_privilege(current_user,'account_projection_outbox','DELETE'), has_table_privilege(current_user,'account_projection_outbox','TRUNCATE'), has_table_privilege(current_user,'mark_observations','INSERT');
SQL
```

- [ ] Before any env update, prove that the configured ID did not change and names the migration-seeded active account and matching profile:

```bash
test "$PROJECTION_ACCOUNT_ID" = "$ORIGINAL_PROJECTION_ACCOUNT_ID"
test "$PROJECTION_ACCOUNT_ID" = '00000000-0000-4000-8000-000000000064'
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" -At --set=account_id="$PROJECTION_ACCOUNT_ID" -c "SET ROLE augr_db_owner; SELECT a.environment,a.status,a.storage_namespace,a.evidence_class,b.environment,b.margin_profile,b.starting_capital::TEXT,b.buying_power_multiplier::TEXT FROM accounts AS a JOIN account_capital_policy_bindings AS b ON b.account_id=a.id WHERE a.id=:'account_id'" | grep -qx 'paper_scored|active|paper_scored/default|promotion_evidence|paper_scored|reg_t|100000.00000000|2.00000000'
```

- [ ] Run `TARGET_DB_NAME="$NEW_DB_NAME" ./scripts/verify-account-cutover.sh --target-zero-history-audit`. Verify zero strategies, zero operational rows, zero outbox rows, and unchanged table counts and sequences.
- [ ] Do not run the post-drain comparison yet. Task 16 runs it after SIGTERM, when all allowed control and terminal writes have stopped.

### Task 16: Update exactly four DB targets

**Files:** No source changes. Use the committed `scripts/update-db-targets.sh`.

- [ ] Verify schema 107. Disable future automation through the existing job-control endpoint and activate the existing kill switch. These are the only operator writes allowed on the old DB. Then use current SIGTERM shutdown with `docker compose stop -t 300 app`. Shutdown closes run admission, cancels in-flight work, waits for terminal writes, and closes pools. Stop web only after app exits. Require zero running DB work and zero runtime sessions while PostgreSQL and Redis remain up.

```bash
CURRENT_OLD_APP_CONTAINER="$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app)"
test -n "$CURRENT_OLD_APP_CONTAINER"
test "$CURRENT_OLD_APP_CONTAINER" = "$OLD_APP_CONTAINER"
test "$(docker inspect --format '{{.Image}}' "$CURRENT_OLD_APP_CONTAINER")" = "$OLD_AUGR_APP_IMAGE_ID"
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$OLD_DB_NAME" -At -c "SELECT version, dirty FROM schema_migrations" | grep -qx '107|f'
./scripts/capture-old-db-baseline.sh --database tradingagent --record-dir /var/lib/augr-cutover/canonical-20260827
curl --fail --silent --show-error -H "Authorization: Bearer $CANARY_JWT" "$AUGR_API_BASE_URL/api/v1/automation/status" | jq -r '.[].name' | while IFS= read -r job; do curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" -H 'Content-Type: application/json' --data '{"enabled":false}' "$AUGR_API_BASE_URL/api/v1/automation/jobs/$job/enable" >/dev/null; done
curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" -H 'Content-Type: application/json' --data '{"active":true,"reason":"canonical cutover drain"}' "$AUGR_API_BASE_URL/api/v1/risk/killswitch" >/dev/null
docker compose --env-file .env -f docker-compose.nuc.yml stop -t 300 app
test -z "$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app)"
docker compose --env-file .env -f docker-compose.nuc.yml stop web
test -z "$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app web)"
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$OLD_DB_NAME" -At -c "SET ROLE augr_db_owner; SELECT (SELECT count(*) FROM pipeline_runs WHERE status='running'), (SELECT count(*) FROM automation_job_runs WHERE status='running')" | grep -qx '0|0'
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -At --set=db_name="$OLD_DB_NAME" -c "SELECT count(*) FROM pg_stat_activity AS a JOIN pg_roles AS r ON r.oid=a.usesysid WHERE a.datname=:'db_name' AND r.rolname IN ('augr_app_runtime','augr_projection_writer') AND a.pid <> pg_backend_pid()" | grep -qx 0
docker compose --env-file .env -f docker-compose.nuc.yml ps --status running postgres redis
./scripts/verify-old-db-after-drain.sh --database tradingagent --record-dir /var/lib/augr-cutover/canonical-20260827
```

- [ ] Open FDs from protected shell variables with tracing off. The secrets do not enter updater argv:

```bash
set +x
umask 077
: "${NEW_APP_DATABASE_URL:?set NEW_APP_DATABASE_URL}"
: "${NEW_DATABASE_URL:?set NEW_DATABASE_URL}"
: "${NEW_PROJECTION_DATABASE_URL:?set NEW_PROJECTION_DATABASE_URL}"
exec 3<<<"$NEW_APP_DATABASE_URL"
exec 4<<<"$NEW_DATABASE_URL"
exec 5<<<"$NEW_PROJECTION_DATABASE_URL"
./scripts/update-db-targets.sh --env-file .env --postgres-db "$NEW_DB_NAME" --app-database-url-fd 3 --database-url-fd 4 --kalshi-projection-database-url-fd 5
exec 3<&- 4<&- 5<&-
test "$(stat -c '%a' .env)" = 600
./scripts/update-db-targets.sh --validate --env-file .env
```

- [ ] Run the updater's validate mode. Require one instance of each target key, the new DB name in all URLs, different general and projection usernames, and an unchanged non-target digest.

### Task 17: Restart without overlap and run canaries

**Files:** No source changes.

- [ ] Prove no old process or DB session remains:

```bash
test -z "$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app web)"
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -At --set=db_name=tradingagent -c "SELECT count(*) FROM pg_stat_activity AS a JOIN pg_roles AS r ON r.oid=a.usesysid WHERE a.datname=:'db_name' AND r.rolname IN ('augr_app_runtime','augr_projection_writer') AND a.pid <> pg_backend_pid()" | grep -qx 0
```

- [ ] Start one app and run exact read canaries:

```bash
: "${AUGR_API_BASE_URL:=http://10.0.0.56:3030}"
docker compose --env-file .env -f docker-compose.nuc.yml -f deploy/docker-compose.nuc.scheduler-paused.yml up -d --no-deps --no-build --scale app=1 app
test "$(docker inspect --format '{{.Image}}' "$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app)")" = "$AUGR_APP_IMAGE"
curl --fail --silent --show-error "$AUGR_API_BASE_URL/healthz" >/dev/null
curl --fail --silent --show-error -H "Authorization: Bearer $CANARY_JWT" "$AUGR_API_BASE_URL/api/v1/me/accounts" | jq -e 'length == 1' >/dev/null
curl --fail --silent --show-error -H "Authorization: Bearer $CANARY_JWT" "$AUGR_API_BASE_URL/api/v1/accounts/$PROJECTION_ACCOUNT_ID/portfolio/summary" | jq -e '.status == "degraded" or .status == "unavailable" or .opening_capital == 100000' >/dev/null
```

- [ ] Start web after read canaries. Create one new paper strategy. Run provider-backed paper canaries only where paper or demo prerequisites pass:

```bash
CANARY_STRATEGY_ID="$(curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" -H 'Content-Type: application/json' --data '{"name":"canonical-cutover-stock","ticker":"SPY","market_type":"stock","config":{},"is_paper":true}' "$AUGR_API_BASE_URL/api/v1/strategies" | jq -er '.id')"
CANARY_REQUESTED_AT="$(date -u +%Y-%m-%dT%H:%M:%S.%6NZ)"
CANARY_ACCEPTED="$(curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" "$AUGR_API_BASE_URL/api/v1/accounts/$PROJECTION_ACCOUNT_ID/strategies/$CANARY_STRATEGY_ID/run")"
test "$(jq -r '.status + "|" + .strategy_id' <<<"$CANARY_ACCEPTED")" = "accepted|$CANARY_STRATEGY_ID"
CANARY_RUN_KEY=
for attempt in $(seq 1 120); do
  CANARY_RUN_KEY="$(docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" -At --set=account_id="$PROJECTION_ACCOUNT_ID" --set=strategy_id="$CANARY_STRATEGY_ID" --set=requested_at="$CANARY_REQUESTED_AT" -c "SET ROLE augr_db_owner; SELECT CASE WHEN count(*)=1 THEN min(id::text || '|' || trade_date::text) ELSE '' END FROM pipeline_runs WHERE account_id=:'account_id'::uuid AND strategy_id=:'strategy_id'::uuid AND started_at>=:'requested_at'::timestamptz")"
  test -n "$CANARY_RUN_KEY" && break
  sleep 1
done
test -n "$CANARY_RUN_KEY"
CANARY_RUN_ID="${CANARY_RUN_KEY%%|*}"
CANARY_RUN_TRADE_DATE="${CANARY_RUN_KEY#*|}"
test "$CANARY_RUN_ID" != "$CANARY_RUN_TRADE_DATE"
CANARY_RUN_STATUS=
for attempt in $(seq 1 300); do
  CANARY_RUN_STATUS="$(curl --fail --silent --show-error -H "Authorization: Bearer $CANARY_JWT" "$AUGR_API_BASE_URL/api/v1/accounts/$PROJECTION_ACCOUNT_ID/runs/$CANARY_RUN_ID?trade_date=$CANARY_RUN_TRADE_DATE" | jq -er '.status')"
  test "$CANARY_RUN_STATUS" != running && break
  sleep 1
done
case "$CANARY_RUN_STATUS" in completed|failed|cancelled) ;; *) exit 1 ;; esac
for attempt in $(seq 1 300); do
  OUTBOX_COUNTS="$(docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" -At --set=account_id="$PROJECTION_ACCOUNT_ID" --set=requested_at="$CANARY_REQUESTED_AT" -c "SET ROLE augr_db_owner; SELECT count(*) FILTER (WHERE status IN ('pending','processing','retry')) || '|' || count(*) FILTER (WHERE status IN ('completed','degraded')) FROM account_projection_outbox WHERE account_id=:'account_id'::uuid AND created_at>=:'requested_at'::timestamptz")"
  test "${OUTBOX_COUNTS%%|*}" = 0 && break
  sleep 1
done
test "${OUTBOX_COUNTS%%|*}" = 0
TARGET_DB_NAME="$NEW_DB_NAME" TARGET_ACCOUNT_ID="$PROJECTION_ACCOUNT_ID" TARGET_RUN_ID="$CANARY_RUN_ID" TARGET_RUN_TRADE_DATE="$CANARY_RUN_TRADE_DATE" ./scripts/verify-account-cutover.sh --target-graph-audit
docker compose --env-file .env -f docker-compose.nuc.yml -f deploy/docker-compose.nuc.scheduler-paused.yml up -d --no-deps --no-build web
test "$(docker inspect --format '{{.Image}}' "$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q web)")" = "$AUGR_WEB_IMAGE"
```

- [ ] After each provider-backed writer, capture its exact `(run_id, trade_date)` with the same unique account, strategy, and request-time query above. Poll that exact run to a terminal state and poll its reachable outbox work until every row is `completed` or `degraded`. Then run `TARGET_DB_NAME="$NEW_DB_NAME" TARGET_ACCOUNT_ID="$PROJECTION_ACCOUNT_ID" TARGET_RUN_ID="$CANARY_RUN_ID" TARGET_RUN_TRADE_DATE="$CANARY_RUN_TRADE_DATE" ./scripts/verify-account-cutover.sh --target-graph-audit` with the captured values. Require a non-vacuous exact-run graph, zero NULL scope, complete pipeline identity, actual copy-run linkage, balanced ledger, and either a valid signed projection or explicit degraded state. The audit does not write fixtures.
- [ ] Restart once with dispatch paused, rerun idempotent callbacks, then resume scheduler and observe one scheduler cycle and one settlement cycle.

```bash
docker compose --env-file .env -f docker-compose.nuc.yml -f deploy/docker-compose.nuc.scheduler-paused.yml restart app
TARGET_DB_NAME="$NEW_DB_NAME" TARGET_ACCOUNT_ID="$PROJECTION_ACCOUNT_ID" TARGET_RUN_ID="$CANARY_RUN_ID" TARGET_RUN_TRADE_DATE="$CANARY_RUN_TRADE_DATE" ./scripts/verify-account-cutover.sh --target-graph-audit
docker compose --env-file .env -f docker-compose.nuc.yml up -d --no-deps --no-build --force-recreate app
```

**Traffic rollback:** Disable existing automation controls and activate the existing kill switch. Stop app with SIGTERM and let runtime shutdown cancel and finalize work and return projection leases. Stop web after app exits. Require zero running DB work and sessions, restore the immutable rollback copy, and start the prior images with the checked-in rollback compose file:

```bash
curl --fail --silent --show-error -H "Authorization: Bearer $CANARY_JWT" "$AUGR_API_BASE_URL/api/v1/automation/status" | jq -r '.[].name' | while IFS= read -r job; do curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" -H 'Content-Type: application/json' --data '{"enabled":false}' "$AUGR_API_BASE_URL/api/v1/automation/jobs/$job/enable" >/dev/null; done
curl --fail --silent --show-error -X POST -H "Authorization: Bearer $CANARY_JWT" -H 'Content-Type: application/json' --data '{"active":true,"reason":"canonical rollback drain"}' "$AUGR_API_BASE_URL/api/v1/risk/killswitch" >/dev/null
docker compose --env-file .env -f docker-compose.nuc.yml stop -t 300 app
docker compose --env-file .env -f docker-compose.nuc.yml stop web
test -z "$(docker compose --env-file .env -f docker-compose.nuc.yml ps -q app web)"
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$NEW_DB_NAME" -At -c "SET ROLE augr_db_owner; SELECT (SELECT count(*) FROM pipeline_runs WHERE status='running'), (SELECT count(*) FROM automation_job_runs WHERE status='running'), (SELECT count(*) FROM account_projection_outbox WHERE status='processing')" | grep -qx '0|0|0'
docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -At --set=db_name="$NEW_DB_NAME" -c "SELECT count(*) FROM pg_stat_activity AS a JOIN pg_roles AS r ON r.oid=a.usesysid WHERE a.datname=:'db_name' AND r.rolname IN ('augr_app_runtime','augr_projection_writer') AND a.pid <> pg_backend_pid()" | grep -qx 0
./scripts/update-db-targets.sh --restore --env-file .env
test "$(docker image inspect --format '{{.Id}}' "$OLD_AUGR_APP_IMAGE")" = "$OLD_AUGR_APP_IMAGE_ID"
test "$(docker image inspect --format '{{.Id}}' "$OLD_AUGR_WEB_IMAGE")" = "$OLD_AUGR_WEB_IMAGE_ID"
AUGR_APP_IMAGE="$OLD_AUGR_APP_IMAGE" AUGR_WEB_IMAGE="$OLD_AUGR_WEB_IMAGE" docker compose --env-file .env -f docker-compose.nuc.yml -f deploy/docker-compose.nuc.rollback.yml up -d --no-deps --no-build app web
curl --fail --silent --show-error "$AUGR_API_BASE_URL/healthz" >/dev/null
```

Keep the fresh database intact. Do not copy new rows back to `tradingagent`.

## Self-review checklist

- [ ] Migration 108 is nullable and compatible with the old write shape. It contains no guard enablement.
- [ ] The 107 to 108 to 109 sequence runs only in disposable databases. Production `tradingagent` receives no migration, bridge, exact-108 binary, or writer canary. The fresh production database initializes migration metadata at 0 and migrates from 0 through 109.
- [ ] `PROJECTION_ACCOUNT_ID` and the account environment bind before any scoped repository or worker constructor. No `EXECUTION_ACCOUNT_ID` key exists.
- [ ] Migration 109 is separate and runs only after scoped writers and old-writer fixtures pass in rehearsal.
- [ ] Every down migration locks its tables before checking data.
- [ ] Copy origin columns remain migration-88 UUID columns, and dependent origin evidence retains its owned columns.
- [ ] Pipeline links use both `id` and `trade_date`.
- [ ] Strategy and copy runs use valid, distinct scope constructors. Copy execution needs no strategy.
- [ ] Migration 108 redefines `strategy_legacy_snapshot_sha` over explicit pre-108 columns. API and discovery strategy creation persist a real immutable execution version before a fresh manual run.
- [ ] Copy scope uses `copy_origin_rebalance_runs.id`, never a request pipeline ID. Strategy-free copy persists the scoped order and fill graph.
- [ ] Both common `execution_intents` and `execution_orders` persist the same copy-origin rebalance run. Migration 109 checks its account and subscription against the durable copy run.
- [ ] The allocator `GetByID` caller and all fakes migrate before the UUID-only run API is deleted.
- [ ] Conversations and agent memories are account-bound in schema, domain, repositories, routes, runtime wiring, and tests. No global canonical route remains.
- [ ] Raw venue evidence precedes the atomic economic transaction. The runtime pool atomically inserts marks and outbox work. After commit, the projection pool only reads inputs and writes the signed checkpoint through the controlled function.
- [ ] Accepted-fill input contains the accepted fill, common lifecycle, source event, canonical instrument and venue contract, normalization, and ledger transaction. Every consumer uses it.
- [ ] The schema-69 checkpoint validator accepts an explicit through-transaction frontier. Projection failure leaves retryable durable state, expired claims recover, shutdown drains, and no duplicate economic effect occurs.
- [ ] The migration runner initializes and locks `schema_migrations` before migration 1, proves 0 to 109, leaves a failed version dirty, refuses dirty reuse, and recovers only by recreating the unselected target.
- [ ] Kalshi marking reads and writes only the configured account.
- [ ] V1 authorization exposes only the account loaded from `PROJECTION_ACCOUNT_ID`; every mismatched account route returns 404.
- [ ] Reports require route account, evidence scope, strategy, and report type. Conversations and memories are account-nested. Polymarket system handlers and all WebSocket producers and clients have explicit scope. No standalone fills resource exists.
- [ ] `RouteStatePages`, `StrategyCreatePage`, `StrategyEditPage`, System Safety, every Cockpit link, `App.test.tsx`, StockPage, EntityLinks, endpoint/schema/query/type files, fixtures, mocks, AppShell, CommandPalette, and all listed operational pages are covered.
- [ ] The current base64 key and secret are reused without output or rotation. Only four DB target variables change.
- [ ] No password-bearing URL enters argv. DB name, owner, roles, function owner, and security-definer privileges are verified.
- [ ] `scripts/update-db-targets.sh` is created, tested, and committed before Phase C.
- [ ] The updater creates one immutable validated rollback artifact. A differing existing artifact fails closed and retries never overwrite it.
- [ ] Every NUC DB command uses `docker compose --env-file .env -f docker-compose.nuc.yml exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER"` and local container socket access. Migration SQL is piped in numeric order with `SET ROLE augr_db_owner` where needed.
- [ ] Writer fixtures mutate only disposable databases. Both target audit modes are read-only, and every verifier invocation names one mode.
- [ ] Verifier, runbook, and release-gate changes are committed before the clean-tree release gate runs.
- [ ] Existing controls disable automation and execution. `docker compose stop -t 300 app` exercises current SIGTERM shutdown, which cancels and finalizes work before web stops. The DB has zero running pipeline, automation, and processing-outbox rows after shutdown.
- [ ] The old app container and schema-107 shutdown rehearsal use the captured `$OLD_AUGR_APP_IMAGE_ID`. The old-DB pre-control and post-shutdown fingerprints differ only in enumerated control, audit, and captured-run terminal fields.
- [ ] Rollback uses `deploy/docker-compose.nuc.rollback.yml`.
- [ ] No wildcard file list or unresolved placeholder remains in this plan.
- [ ] `git diff --check` passes.

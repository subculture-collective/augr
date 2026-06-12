# Augr Architecture Deepening Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Address every architecture issue found in the review, including the deprioritized/watch items, by deepening high-leverage Seams without speculative rewrites.

**Architecture:** Work in thin, independently testable tranches. First pin current behavior with tests, then deepen the Module that owns the concept, then delete duplicated caller knowledge. Keep existing external Interfaces stable unless a task explicitly adds a backward-compatible field or checked Adapter.

**Tech Stack:** Go 1.25, chi HTTP handlers, pgx/PostgreSQL, gorilla/websocket, React 19, Vite, TypeScript 5.9, TanStack Query, Vitest.

---

## Scope and ordering

This is not one giant refactor. Execute in this order so each tranche creates Leverage for later work:

1. **Run identity + pipeline wire vocabulary** — run lookup, WebSocket events, pipeline run view.
2. **Trading safety policy** — position sizing, risk internals, risk display duplication, strategy config drift.
3. **Runtime composition and job workers** — LLM provider assembly, paper validation report worker.
4. **Stateful market data lifecycle** — Polymarket feed/recording semantics.
5. **Deprioritized composition cleanups** — legacy trading pipeline path, data provider selection, signal lifecycle.
6. **Final deletion-test pass** — remove pass-through helpers and update docs after behavior is pinned.

Commit after each task or after each reviewed subagent slice. Do not mix tranches in one commit.

---

## Issue coverage matrix

| Review issue | Plan task(s) | Intent |
| --- | --- | --- |
| Pipeline run lookup leaks storage shape | Task 1 | Deepen run lookup Module and remove caller page-scans |
| Pipeline run view reconstructs pipeline meaning | Task 3 | Move phase/status/event interpretation into a run-view Module |
| WebSocket event vocabulary has three coupled Interfaces | Task 2 | Add checked cross-stack event vocabulary Adapter |
| Paper validation report work inside broad automation Module | Task 8 | Move report work behind a worker Module |
| Position sizing and edge policy split | Task 4 | Centralize ADR-005 sizing policy; keep math helpers small |
| Risk controls overloaded Implementation | Task 5 | Split internal risk policies while preserving outer Interface |
| LLM provider chain Deep, runtime assembly Shallow | Task 7 | Move provider graph assembly out of `cmd/tradingagent` |
| Polymarket CLOB recording lifecycle implicit | Task 9 | Make buffering/flushing/drop/shutdown semantics explicit |
| Legacy trading agent pipeline split | Task 10 | Stabilize or retire `Pipeline.ExecuteStrategy` path beside Runner |
| Broad data provider chain | Task 11 | Make provider selection/cache policy declarative, not scattered |
| Signal intelligence pipeline | Task 12 | Add explicit signal lifecycle Module and end-to-end tests |
| Risk UI duplication | Task 6 | Share risk display Adapter across risk views |
| Strategy config JSON drift | Task 13 | Keep flexible JSON, deepen typed editor/view Adapter |

---

## Cross-cutting rules

- Use the architecture vocabulary in review notes: Module, Interface, Implementation, Depth, Deep, Shallow, Seam, Adapter, Leverage, Locality, deletion test.
- Do not introduce broad rewrites just because files are split. A Module is not Shallow because it has multiple files.
- Prefer pure function tests first for policy, event mapping, view mapping, and provider selection.
- Preserve wire compatibility unless a task explicitly states a versioned/additive change.
- For every task, finish with a deletion test note: which old caller knowledge, helper, or duplicate path can now be deleted, and which cannot.

---

## File map

### Likely Go files

- Modify: `internal/repository/interfaces.go`
- Modify: `internal/repository/postgres/pipeline_run.go`
- Modify: `internal/repository/postgres/pipeline_run_test.go`
- Modify: `internal/api/run_handlers.go`
- Modify: `internal/service/run.go`
- Modify: `internal/api/hub.go`
- Add/Modify: `internal/api/hub_event_types_test.go`
- Modify: `internal/automation/orchestrator.go`
- Modify: `internal/automation/jobs_reports.go`
- Add: `internal/automation/report_worker.go`
- Add: `internal/automation/report_worker_test.go`
- Add: `internal/position/policy.go`
- Add: `internal/position/policy_test.go`
- Modify: `internal/execution/position_sizing.go`
- Modify: `internal/edge/sizing.go`
- Modify: `internal/optionsresearch/scanner.go`
- Modify: `internal/polymarketresearch/scan.go`
- Modify: `internal/risk/engine_impl.go`
- Add/Modify: `internal/risk/kill_switch.go`
- Add/Modify: `internal/risk/market_exposure.go`
- Add/Modify: `internal/risk/status_projection.go`
- Modify: `internal/risk/engine_impl_test.go`
- Add: `internal/llm/composition.go`
- Add: `internal/llm/composition_test.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`
- Add/Modify: `internal/recorder/polymarket_lifecycle.go`
- Add/Modify: `internal/recorder/polymarket_lifecycle_test.go`
- Modify: `internal/marketdata/polymarket/feed.go`
- Modify: `internal/marketdata/polymarket/cleaner.go`
- Modify: `internal/recorder/polymarket.go`
- Modify: `internal/agent/pipeline.go`
- Modify: `internal/agent/runner.go`
- Modify: `internal/agent/pipeline_test.go`
- Add/Modify: `internal/data/selection_policy.go`
- Add/Modify: `internal/data/selection_policy_test.go`
- Modify: `internal/data/factory.go`
- Modify: `internal/data/chain.go`
- Add/Modify: `internal/signal/lifecycle.go`
- Add/Modify: `internal/signal/lifecycle_test.go`

### Likely frontend files

- Add/Modify: `web/src/lib/api/websocket-events.ts`
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/hooks/use-websocket-client.ts`
- Modify: `web/src/hooks/use-websocket-client.test.tsx`
- Add: `web/src/lib/pipeline/run-view.ts`
- Add: `web/src/lib/pipeline/run-view.test.ts`
- Modify: `web/src/pages/pipeline-run-page.tsx`
- Modify: `web/src/pages/pipeline-run-page.test.tsx`
- Modify: `web/src/components/pipeline/decision-inspector.tsx`
- Add: `web/src/lib/risk/presentation.ts`
- Add: `web/src/lib/risk/presentation.test.ts`
- Modify: `web/src/pages/risk-page.tsx`
- Modify: `web/src/pages/risk-page.test.tsx`
- Modify: `web/src/components/dashboard/risk-status-bar.tsx`
- Modify: `web/src/components/dashboard/risk-status-bar.test.tsx`
- Modify: `web/src/pages/strategy-detail-page.tsx`
- Modify: `web/src/components/strategies/strategy-config-editor.tsx`
- Modify: `web/src/lib/strategy-config/boundary.ts`
- Add: `web/src/lib/strategy-config/view-model.test.ts`

---

## Baseline verification before Task 1

- [ ] **Step 1: Capture current status**

Run:

```bash
git status --short
```

Expected: note any existing uncommitted files. Do not overwrite unrelated user work.

- [ ] **Step 2: Run focused baseline suites**

Run:

```bash
go test ./internal/repository/... ./internal/api/... ./internal/agent/... ./internal/automation/... ./internal/execution/... ./internal/edge/... ./internal/risk/... ./internal/llm/... ./internal/data/... ./internal/signal/... ./internal/marketdata/... ./internal/recorder/... ./cmd/tradingagent
npm --prefix web test -- --run
npm --prefix web run build
```

Expected: PASS or record pre-existing failures before changes.

---

### Task 1: Deepen run lookup Module

**Addresses:** Pipeline run lookup leaks storage shape.

**Files:**
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/repository/postgres/pipeline_run.go`
- Modify: `internal/repository/postgres/pipeline_run_test.go`
- Modify: `internal/service/run.go`
- Modify: `internal/api/run_handlers.go`

- [ ] **Step 1: Add failing repository coverage for lookup by run ID**

Add a test in `internal/repository/postgres/pipeline_run_test.go` that stores at least two pipeline runs on different trade dates and verifies lookup by run ID returns the correct row without the caller providing `tradeDate`.

Run:

```bash
go test ./internal/repository/postgres -run 'PipelineRun.*(Get|Find|Lookup)' -count=1
```

Expected: FAIL until the repository Implementation owns the lookup.

- [ ] **Step 2: Add the repository lookup path**

Modify `internal/repository/interfaces.go` and `internal/repository/postgres/pipeline_run.go` so the run repository Interface supports run-ID lookup without exposing the storage partition. Keep existing date-based lookup if other callers need it.

- [ ] **Step 3: Remove duplicate page-scans from callers**

Update `internal/service/run.go` and `internal/api/run_handlers.go` so callers ask the run lookup Module for a run by ID. Delete duplicated scan helpers if they become pass-throughs.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/repository/postgres ./internal/service ./internal/api -run 'PipelineRun|Run' -count=1
```

Expected: PASS. Deletion test note: no run-ID caller should know that old storage lookup required `tradeDate`.

---

### Task 2: Add checked WebSocket event vocabulary Adapter

**Addresses:** WebSocket event vocabulary has three coupled Interfaces.

**Files:**
- Modify: `internal/api/hub.go`
- Add/Modify: `internal/api/hub_event_types_test.go`
- Add/Modify: `web/src/lib/api/websocket-events.ts`
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/hooks/use-websocket-client.ts`
- Modify: `web/src/hooks/use-websocket-client.test.tsx`

- [ ] **Step 1: Pin the current event vocabulary**

Create or update tests that list every event string currently emitted by `internal/api/hub.go` and accepted by the frontend WebSocket Adapter.

Run:

```bash
go test ./internal/api -run 'WebSocket|Hub|Event' -count=1
npm --prefix web test -- --run web/src/hooks/use-websocket-client.test.tsx
```

Expected: PASS before refactor; this pins behavior.

- [ ] **Step 2: Create one frontend event vocabulary Module**

Move the TypeScript event literal list out of the giant type file into `web/src/lib/api/websocket-events.ts`. Export the event list and derive `WebSocketEventType` from it. Keep `web/src/lib/api/types.ts` as the importing Interface so callers do not churn.

- [ ] **Step 3: Add cross-stack drift detection**

Add a checked Adapter test path: Go exposes a sorted event list, frontend exposes a sorted event list, and tests fail if either list changes without the other. This can be a golden-list test rather than a generator; the requirement is that event drift becomes visible in CI.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/api -run 'WebSocket|Hub|Event' -count=1
npm --prefix web test -- --run web/src/hooks/use-websocket-client.test.tsx
npm --prefix web run build
```

Expected: PASS. Deletion test note: remove any comment saying event vocabularies must be manually kept in sync if the new checked Adapter makes that comment obsolete.

---

### Task 3: Deepen pipeline run-view Module

**Addresses:** Pipeline run view reconstructs too much pipeline meaning.

**Files:**
- Add: `web/src/lib/pipeline/run-view.ts`
- Add: `web/src/lib/pipeline/run-view.test.ts`
- Modify: `web/src/pages/pipeline-run-page.tsx`
- Modify: `web/src/pages/pipeline-run-page.test.tsx`
- Modify: `web/src/components/pipeline/decision-inspector.tsx`
- Modify: `web/src/lib/api/types.ts`

- [ ] **Step 1: Pin current run-view behavior**

Add pure tests for legacy role aliases, phase completion, decision ordering, and WebSocket invalidation rules. These tests should use small run fixtures, not full page rendering.

Run:

```bash
npm --prefix web test -- --run web/src/pages/pipeline-run-page.test.tsx web/src/components/pipeline/decision-inspector.test.tsx
```

Expected: PASS before extraction.

- [ ] **Step 2: Extract run interpretation into `run-view.ts`**

Move phase cards, legacy role normalization, decision grouping, and event invalidation mapping into `web/src/lib/pipeline/run-view.ts`. The page should render a view model and stop reconstructing pipeline Implementation details inline.

- [ ] **Step 3: Keep `DecisionInspector` focused**

Leave markdown/structured-output rendering in `DecisionInspector`; move pipeline ordering/role semantics out if present. This keeps it Deep for inspection and prevents it from becoming a pipeline meaning Module.

- [ ] **Step 4: Verify**

Run:

```bash
npm --prefix web test -- --run web/src/lib/pipeline/run-view.test.ts web/src/pages/pipeline-run-page.test.tsx web/src/components/pipeline/decision-inspector.test.tsx
npm --prefix web run build
```

Expected: PASS. Deletion test note: the page should not contain role alias maps or phase completion rules after this task.

---

### Task 4: Deepen position sizing policy Module

**Addresses:** Position sizing and edge policy split.

**Files:**
- Add: `internal/position/policy.go`
- Add: `internal/position/policy_test.go`
- Modify: `internal/execution/position_sizing.go`
- Modify: `internal/edge/sizing.go`
- Modify: `internal/edge/expected_value.go`
- Modify: `internal/optionsresearch/scanner.go`
- Modify: `internal/polymarketresearch/scan.go`

- [ ] **Step 1: Write ADR-005 policy tests**

Add tests proving the intended defaults: ATR for stock/crypto, fixed fractional 2% for Polymarket, and half-Kelly only after 100+ closed trades. Include negative tests for insufficient trade history and missing edge inputs.

Run:

```bash
go test ./internal/position ./internal/execution ./internal/edge -run 'Sizing|Kelly|ATR|Polymarket' -count=1
```

Expected: FAIL until the policy Module owns method choice.

- [ ] **Step 2: Implement the policy Module**

Create `internal/position/policy.go` as the Deep Module that decides which sizing method applies. Keep pure math helpers in `internal/execution` and `internal/edge` if they already have Locality; do not merge math just to merge files.

- [ ] **Step 3: Redirect market callers through the policy Module**

Update options and Polymarket scanner paths to ask the policy Module for sizing decisions. Leave market-specific inputs near the market caller, but move method choice and ADR-005 defaults out of the scanners.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/position ./internal/execution ./internal/edge ./internal/optionsresearch ./internal/polymarketresearch -run 'Sizing|Kelly|ATR|ExpectedValue|Scan' -count=1
```

Expected: PASS. Deletion test note: deleting the old dispatcher should not scatter ADR-005 defaults back into market Modules.

---

### Task 5: Split risk internals into Deep policy Modules

**Addresses:** Risk controls overloaded Implementation.

**Files:**
- Modify: `internal/risk/engine_impl.go`
- Add/Modify: `internal/risk/kill_switch.go`
- Add/Modify: `internal/risk/market_exposure.go`
- Add/Modify: `internal/risk/status_projection.go`
- Modify: `internal/risk/capital_ladder.go`
- Modify: `internal/risk/drawdown_breaker.go`
- Modify: `internal/risk/consecutive_loss.go`
- Modify: `internal/risk/cockpit.go`
- Modify: `internal/risk/persist.go`
- Modify: `internal/risk/engine_impl_test.go`
- Modify: `internal/risk/cockpit_test.go`

- [ ] **Step 1: Pin outer risk behavior**

Add or strengthen tests for kill switch, circuit breaker, Polymarket limits, position limits, persistence restore, and cockpit/status projection through the existing outer risk Interface.

Run:

```bash
go test ./internal/risk -run 'Risk|Kill|Circuit|Polymarket|Position|Cockpit|Persist' -count=1
```

Expected: PASS before splitting internals.

- [ ] **Step 2: Extract kill switch state**

Move kill switch state transitions and mechanism labels into a focused internal Module. Keep the outer risk Interface stable.

- [ ] **Step 3: Extract market exposure checks**

Move per-market exposure and position-limit logic into a focused internal Module. Do not implement ADR-008 correlated exposure yet; leave a narrow Seam that can receive it later.

- [ ] **Step 4: Extract status projection**

Move cockpit/status shaping into a focused internal Module so trading approval policy does not also own display projection.

- [ ] **Step 5: Verify**

Run:

```bash
go test ./internal/risk -count=1
```

Expected: PASS. Deletion test note: `RiskEngineImpl` should orchestrate internal policies, not own every policy detail directly.

---

### Task 6: Remove duplicated risk display rules

**Addresses:** Risk UI duplication.

**Files:**
- Add: `web/src/lib/risk/presentation.ts`
- Add: `web/src/lib/risk/presentation.test.ts`
- Modify: `web/src/pages/risk-page.tsx`
- Modify: `web/src/pages/risk-page.test.tsx`
- Modify: `web/src/components/dashboard/risk-status-bar.tsx`
- Modify: `web/src/components/dashboard/risk-status-bar.test.tsx`

- [ ] **Step 1: Pin duplicated labels and ordering**

Add pure tests for kill switch labels, circuit breaker labels, market ordering, and status severity mapping used by both risk views.

Run:

```bash
npm --prefix web test -- --run web/src/pages/risk-page.test.tsx web/src/components/dashboard/risk-status-bar.test.tsx
```

Expected: PASS before extraction.

- [ ] **Step 2: Create shared risk presentation Adapter**

Move label, severity, and ordering rules into `web/src/lib/risk/presentation.ts`. Keep rendering and interactions in the existing view files.

- [ ] **Step 3: Update both risk views**

Update `risk-page.tsx` and `risk-status-bar.tsx` to use the shared presentation Adapter.

- [ ] **Step 4: Verify**

Run:

```bash
npm --prefix web test -- --run web/src/lib/risk/presentation.test.ts web/src/pages/risk-page.test.tsx web/src/components/dashboard/risk-status-bar.test.tsx
npm --prefix web run build
```

Expected: PASS. Deletion test note: changing a risk label or mechanism order should require one frontend edit, not two.

---

### Task 7: Deepen LLM runtime composition Module

**Addresses:** LLM provider chain is Deep, runtime assembly is Shallow.

**Files:**
- Add: `internal/llm/composition.go`
- Add: `internal/llm/composition_test.go`
- Modify: `internal/llm/provider_chain.go`
- Modify: `internal/llm/registry.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/runtime_test.go`

- [ ] **Step 1: Pin provider graph assembly behavior**

Add tests for config interpretation, provider selection, fallback creation, model resolution, cache toggle, metrics Adapter binding, and ADR-002 two-tier defaults.

Run:

```bash
go test ./internal/llm ./cmd/tradingagent -run 'Provider|LLM|Runtime|Model|Cache|Tier' -count=1
```

Expected: PASS before moving code.

- [ ] **Step 2: Move provider graph assembly into `internal/llm`**

Create `internal/llm/composition.go` so `cmd/tradingagent` passes config-shaped inputs and receives a ready provider Adapter. Do not weaken the existing `provider_chain` Module; treat it as an Implementation detail used by composition.

- [ ] **Step 3: Thin `cmd/tradingagent/runtime.go`**

Delete provider selection and fallback construction logic from runtime once composition owns it.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/llm ./cmd/tradingagent -count=1
```

Expected: PASS. Deletion test note: adding a provider or changing two-tier selection should not require editing runtime assembly logic.

---

### Task 8: Deepen paper validation report worker Module

**Addresses:** Paper validation report work inside broad automation Module.

**Files:**
- Modify: `internal/automation/orchestrator.go`
- Modify: `internal/automation/jobs_reports.go`
- Add: `internal/automation/report_worker.go`
- Add: `internal/automation/report_worker_test.go`

- [ ] **Step 1: Pin current report behavior**

Add tests for strategy filtering, backtest lookup, metrics decoding, paper validation output, artifact persistence, jitter handling, and error artifact policy.

Run:

```bash
go test ./internal/automation -run 'Report|Paper|Validation|Artifact|Backtest' -count=1
```

Expected: PASS before extraction if behavior is already covered; otherwise add coverage first.

- [ ] **Step 2: Move report domain work into worker Module**

Create `internal/automation/report_worker.go` and move paper validation report Implementation there. Keep the orchestrator responsible for scheduling, job state, and observation only.

- [ ] **Step 3: Thin `jobs_reports.go`**

Make job registration and trigger code call the report worker. Remove report-specific policy from broad automation wiring.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/automation -count=1
```

Expected: PASS. Deletion test note: changing paper validation report persistence should not require editing generic job scheduling logic.

---

### Task 9: Make Polymarket recording lifecycle explicit

**Addresses:** Polymarket CLOB feed recording lifecycle implicit.

**Files:**
- Add/Modify: `internal/recorder/polymarket_lifecycle.go`
- Add/Modify: `internal/recorder/polymarket_lifecycle_test.go`
- Modify: `internal/recorder/polymarket.go`
- Modify: `internal/marketdata/polymarket/feed.go`
- Modify: `internal/marketdata/polymarket/cleaner.go`

- [ ] **Step 1: Pin lifecycle semantics**

Add tests covering buffering, flush on interval, flush on shutdown, dropped-record metrics, lag metrics, cleaner warmup/dedupe, and feed unsubscribe behavior.

Run:

```bash
go test ./internal/recorder ./internal/marketdata/polymarket -run 'Polymarket|Recorder|Lifecycle|Feed|Cleaner|Shutdown|Drop|Lag' -count=1
```

Expected: PASS where behavior already exists; FAIL only for missing explicit lifecycle coverage.

- [ ] **Step 2: Move lifecycle policy into recorder lifecycle Module**

Create or update `internal/recorder/polymarket_lifecycle.go` so buffering, flushing, shutdown, drop, and lag semantics live in one Deep Module. Keep live feed transport as an Adapter.

- [ ] **Step 3: Clarify the feed-to-recorder Seam**

Update feed/cleaner code so it exposes clean event streams and does not own durable recording behavior.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/recorder ./internal/marketdata/polymarket -count=1
```

Expected: PASS. Deletion test note: a backpressure or shutdown policy change should live in one Module.

---

### Task 10: Stabilize the legacy trading pipeline path

**Addresses:** Legacy trading agent pipeline split.

**Files:**
- Modify: `internal/agent/pipeline.go`
- Modify: `internal/agent/runner.go`
- Modify: `internal/agent/pipeline_test.go`
- Modify: `internal/agent/runner_test.go`

- [ ] **Step 1: Identify live callers**

Search for `ExecuteStrategy` and old `Pipeline` construction. Record whether the path is active, test-only, or replaceable by Runner.

Run:

```bash
go test ./internal/agent -run 'Pipeline|Runner|ExecuteStrategy' -count=1
```

Expected: PASS before behavior changes.

- [ ] **Step 2: Pin mutation and risk-review behavior**

Add tests around `ExecuteStrategy` config mutation and the forced risk-debate skip. If current behavior is intentional, document it in the test name. If it is accidental, let the test fail and fix it.

- [ ] **Step 3: Route new execution through Runner where safe**

Prefer Runner for active paths that need immutable execution plans. If old Pipeline remains, make it a small Adapter with documented semantics rather than a parallel execution Implementation.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/agent ./cmd/tradingagent -run 'Pipeline|Runner|ExecuteStrategy|Strategy' -count=1
```

Expected: PASS. Deletion test note: either old Pipeline logic is deleted, or it is visibly a narrow Adapter beside Runner.

---

### Task 11: Make data provider selection policy declarative

**Addresses:** Broad data provider chain.

**Files:**
- Add/Modify: `internal/data/selection_policy.go`
- Add/Modify: `internal/data/selection_policy_test.go`
- Modify: `internal/data/factory.go`
- Modify: `internal/data/chain.go`
- Modify: `internal/data/options_chain.go`
- Modify: `internal/data/provider.go`

- [ ] **Step 1: Pin provider order and fallback behavior**

Add tests for stock, crypto, options, and Polymarket provider selection, including cache policy and fallback order.

Run:

```bash
go test ./internal/data -run 'Provider|Registry|Chain|Options|Cache|Fallback|Selection' -count=1
```

Expected: PASS before refactor.

- [ ] **Step 2: Extract selection policy**

Move provider ordering, market-type routing, cache enablement, and fallback rules into `internal/data/selection_policy.go`. Keep provider registry and provider chains because they provide Adapter Leverage.

- [ ] **Step 3: Thin factory assembly**

Update `factory.go` so it reads policy and assembles providers; it should not hide additional selection rules.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/data -count=1
```

Expected: PASS. Deletion test note: adding/removing a provider should touch selection policy and provider registration, not many callers.

---

### Task 12: Make signal lifecycle explicit

**Addresses:** Signal intelligence pipeline.

**Files:**
- Add/Modify: `internal/signal/lifecycle.go`
- Add/Modify: `internal/signal/lifecycle_test.go`
- Modify: `internal/signal/orchestrator.go`
- Modify: `internal/signal/evaluator.go`
- Modify: `internal/signal/source.go`
- Modify: `internal/signal/strategy_provider.go`
- Modify: `internal/signal/trigger_handler.go`

- [ ] **Step 1: Pin one full signal path**

Add an end-to-end package test with fake source, fake evaluator, fake strategy provider, and fake trigger handler. It should prove ingestion, evaluation, strategy lookup, cache behavior, and trigger routing order.

Run:

```bash
go test ./internal/signal -run 'Signal|Lifecycle|Orchestrator|Evaluate|Trigger|Strategy' -count=1
```

Expected: PASS before moving code if current seams are sufficient; otherwise FAIL and guide extraction.

- [ ] **Step 2: Add lifecycle Module**

Create `internal/signal/lifecycle.go` for the explicit signal flow. Keep sources and trigger handler as Adapters.

- [ ] **Step 3: Thin orchestrator assembly**

Update `orchestrator.go` to assemble the lifecycle and own scheduling/coordination only.

- [ ] **Step 4: Verify**

Run:

```bash
go test ./internal/signal -count=1
```

Expected: PASS. Deletion test note: understanding one signal from source to trigger should require reading lifecycle plus Adapters, not every file in the package.

---

### Task 13: Deepen strategy config editor/view Adapter

**Addresses:** Strategy config JSON drift.

**Files:**
- Modify: `web/src/pages/strategy-detail-page.tsx`
- Modify: `web/src/components/strategies/strategy-config-editor.tsx`
- Modify: `web/src/lib/strategy-config/boundary.ts`
- Add: `web/src/lib/strategy-config/view-model.test.ts`
- Modify: `internal/api/strategy_handlers.go`
- Modify: `internal/domain/strategy.go`

- [ ] **Step 1: Pin flexible JSON behavior**

Add tests for existing config round-trip, numeric conversion, analyst selection, cron rules, and risk hints. Do not force a rigid schema unless tests prove current flexibility is causing drift.

Run:

```bash
npm --prefix web test -- --run web/src/components/strategies/strategy-config-editor.test.tsx web/src/pages/strategy-detail-page.test.tsx
go test ./internal/api ./internal/domain -run 'Strategy|Config|Validation' -count=1
```

Expected: PASS before extraction where tests already exist; add missing coverage first.

- [ ] **Step 2: Make editor state explicit**

Deepen `web/src/lib/strategy-config/boundary.ts` so conversion between raw JSON and editor/view state is the one place that owns numeric conversion, analyst lists, cron fields, and risk hints.

- [ ] **Step 3: Thin the page and editor**

Update `strategy-detail-page.tsx` and `strategy-config-editor.tsx` to render and edit typed view state rather than reverse-engineering raw JSON inline.

- [ ] **Step 4: Verify**

Run:

```bash
npm --prefix web test -- --run web/src/lib/strategy-config/view-model.test.ts web/src/components/strategies/strategy-config-editor.test.tsx web/src/pages/strategy-detail-page.test.tsx
npm --prefix web run build
go test ./internal/api ./internal/domain -run 'Strategy|Config|Validation' -count=1
```

Expected: PASS. Deletion test note: adding a strategy config field should require one conversion edit plus rendering, not page-wide raw JSON inspection.

---

## Final integration and review

- [ ] **Step 1: Run full verification**

Run:

```bash
go test ./...
npm --prefix web test -- --run
npm --prefix web run build
```

Expected: PASS or documented pre-existing failures only.

- [ ] **Step 2: Run deletion-test review**

For each issue, write one short note in the PR/commit summary:

```text
Issue: <name>
Deepened Module/Seam: <path or package>
Deleted caller knowledge: <what moved>
Remaining Adapter: <why it remains>
```

- [ ] **Step 3: Update architecture docs only after code lands**

Update `docs/AUGR_ARCHITECTURE_AUDIT.md` or a new follow-up note with the new Module ownership. Do not update docs first; docs should reflect the final Implementation.

- [ ] **Step 4: Review with architecture vocabulary**

Run `review-quality` or request an @oracle review focused on Depth, Locality, Interface size, Adapter placement, and deletion-test outcomes.

---

## Recommended parallel execution groups

Use parallel workers only when file ownership does not overlap:

1. **Group A:** Task 1, Task 2 can run in parallel after baseline.
2. **Group B:** Task 4, Task 7, Task 8 can run in parallel.
3. **Group C:** Task 5 then Task 6 sequentially; risk internals should land before risk presentation cleanup.
4. **Group D:** Task 9, Task 11, Task 12 can run in parallel.
5. **Group E:** Task 3 after Task 2; Task 13 can run anytime after baseline.
6. **Group F:** Task 10 after Task 7 if runtime execution paths overlap in `cmd/tradingagent`; otherwise it can run after baseline.

Do not parallelize two tasks that both edit `web/src/lib/api/types.ts`, `cmd/tradingagent/runtime.go`, or `internal/risk/*` unless ownership is explicitly split first.

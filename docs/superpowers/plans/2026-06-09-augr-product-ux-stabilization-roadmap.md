# Augr Product UX Stabilization Roadmap Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Turn the June 9 Augr UI/backend notes into an ordered, testable roadmap that fixes safety terminology, restores trustworthy data surfaces, and upgrades the operator experience without weakening trading guardrails.

**Architecture:** Treat the notes as multiple product workstreams sharing a React/Vite/TanStack Query frontend, Go/chi/PostgreSQL backend, and websocket-driven pipeline UI. Implement in safety-first phases: stabilize global controls and data correctness before adding richer detail pages, then overhaul high-complexity experiences like realtime runs, prompts, glossary, and backend decision semantics. Keep live trading paths behind the existing risk engine, live gate, and audit log.

**Tech Stack:** Go 1.25, chi, PostgreSQL/TimescaleDB/pgvector, Redis, React 19, Vite, TypeScript, React Router v7, TanStack Query, Tailwind v4, Vitest/JSDOM.

---

## Current Baseline

- Frontend route shell: `web/src/App.tsx`, `web/src/components/layout/app-shell.tsx`.
- Global HUD theme and scrollbars: `web/src/index.css`.
- Shared page framing: `web/src/components/layout/page-header.tsx`.
- Kill-switch UI: `web/src/components/dashboard/risk-status-bar.tsx`, `web/src/pages/risk-page.tsx`, `web/src/pages/settings-page.tsx`.
- Realtime/event UI: `web/src/pages/realtime-page.tsx`; run detail: `web/src/pages/pipeline-run-page.tsx`, `web/src/components/pipeline/*`.
- Strategy UI: `web/src/pages/strategies-page.tsx`, `web/src/pages/strategy-detail-page.tsx`, `web/src/components/strategies/strategy-config-editor.tsx`, `web/src/components/strategies/strategy-run-history.tsx`.
- Backtests: `web/src/pages/backtests-page.tsx`, `web/src/pages/backtest-detail-page.tsx`, `web/src/components/backtests/*`.
- Major backend routing: `internal/api/server.go`; supporting packages include `internal/api/*_handlers.go`, `internal/automation/*`, `internal/signal/*`, `internal/notification/*`, `internal/data/*`, `internal/repository/postgres/*`.
- Prompt registry: `internal/api/prompts.go`; current model supports category/key/default/effective/override text, but not persisted editable labels/tags or mock effective-preview data.
- Automation jobs: `internal/automation/orchestrator.go`; current frontend shows raw schedule strings and heavily truncated descriptions in `web/src/pages/automation-page.tsx`.
- Canonical docs warn that `docs/design/` can overstate maturity. Treat current code, `docs/README.md`, `docs/roadmap.md`, and `docs/known-issues.md` as implementation truth.

## Product Principles

1. **Safety language is consistent everywhere.** The emergency action button is always a large red **Stop All** button when trading is enabled. State labels describe trading state, not the kill switch object.
2. **Empty data is not silent.** Empty pages must show actionable setup/state diagnostics or be backed by real data.
3. **Operator layout is bounded and stable.** Route/event selection must not unexpectedly scroll the page body; local scrollers should be small and neubrutalist.
4. **Detail pages follow data readiness.** Add detail pages only after list pages have stable IDs, routes, and useful API payloads.
5. **Decision semantics match position context.** New-entry evaluation should not emit a misleading sell signal for a stock the system does not own.
6. **Live-capable changes stay guarded.** Do not bypass risk checks, order live gates, allowlists, or audit log expectations.

## Priority Order

| Priority | Workstream | Why first |
| --- | --- | --- |
| P0 | Safety wording, kill-switch controls, trading-state labels | Prevents operator confusion around emergency controls. |
| P0 | Backend decision correctness, confidence/latency truthfulness | Prevents misleading buy/sell/hold signals and false alert metadata. |
| P1 | Global shell/layout/sidebar/scrolling | Fixes cross-site usability and the faded/unclickable main-view issue. |
| P1 | Empty-data diagnostics | Separates missing data from broken UI and guides backend fixes. |
| P2 | Realtime event-feed/run-detail overhaul | High UX value, high complexity; starts after shell stability. |
| P2 | Strategy and backtest detail improvements | Improves daily operator workflows and fixes broken lifecycle controls. |
| P3 | Polymarket, Options, Calendar, Universe, Stock detail | Research surfaces need defaults, drilldowns, provider diagnostics. |
| P3 | Automation/Reliability/Surfers Ops | Adds operational context once job/run contracts are clear. |
| P4 | Prompt studio, glossary wiki, auto-derived terms | Valuable polish after safety/data foundations. |

---

## File Structure Map

### Global UI Shell and Shared Controls

- Modify: `web/src/components/layout/app-shell.tsx` — bounded shell, sidebar fit, header/footer, local scroll containers.
- Modify: `web/src/components/layout/page-header.tsx` — consistent header spacing under the shell.
- Modify: `web/src/index.css` — global page-height, scrollbar treatment, gradient pointer-event safety.
- Modify: `web/src/components/ui/button.tsx` — ensure destructive emergency buttons have the right size/visual weight.
- Modify: `web/src/components/dashboard/risk-status-bar.tsx` — overview kill-switch card wording/button style.
- Modify: `web/src/pages/risk-page.tsx` — risk kill-switch labels and audit filters.
- Modify: `web/src/pages/settings-page.tsx` — replace Activate/Deactivate wording with Stop All / Resume Trading semantics.
- Test: `web/src/components/layout/app-shell.test.tsx`, `web/src/pages/risk-page.test.tsx`, `web/src/pages/settings-page.test.tsx`, `web/src/components/dashboard/risk-status-bar.test.tsx`.

### Realtime, Run Detail, and Pipeline Conversations

- Modify: `web/src/pages/realtime-page.tsx` — run-centric event feed, layout-stable selection, debate conversation auto-population.
- Modify: `web/src/pages/pipeline-run-page.tsx` — markdown-capable decision output and stable phase cards.
- Modify: `web/src/components/pipeline/phase-progress.tsx` — equal-sized step cards.
- Modify: `web/src/components/pipeline/decision-inspector.tsx` — markdown rendering for prompt/LLM output where safe.
- Modify: `web/src/components/chat/chat-panel.tsx` — phase-conversation display without viewport jumps.
- Modify backend event/conversation handlers only if current APIs cannot provide run-level grouping and phase transcripts.
- Test: `web/src/pages/realtime-page.test.tsx`, `web/src/pages/pipeline-run-page.test.tsx`, `web/src/components/chat/chat-panel.test.tsx`, plus touched Go handler tests.

### Strategy, Backtest, Journal, and Trading Records

- Modify: `web/src/pages/strategies-page.tsx` — last-run outcome on strategy cards.
- Modify: `web/src/pages/strategy-detail-page.tsx` — summary, rule-engine table, backtests before recent orders, ticker-filtered orders, lifecycle button fixes.
- Modify: `web/src/components/strategies/strategy-run-history.tsx` — link each run to `/runs/:id`.
- Modify: `web/src/pages/backtests-page.tsx`, `web/src/pages/backtest-detail-page.tsx` — clarify config vs run detail and fix empty detail behavior.
- Modify: `web/src/pages/decision-journal-page.tsx` — show decision feed entries or explain missing backend data.
- Modify corresponding `internal/api/*_handlers.go` and `internal/repository/postgres/*` files when frontend emptiness is caused by backend filters/persistence.
- Test: strategy/backtest/journal frontend tests plus relevant Go handler/repository tests.

### Signals, Discovery, Options, Calendar, Universe, and Stock Detail

- Modify: `web/src/pages/signals-page.tsx` — signal detail links, trigger-log counts, watch-term visibility.
- Create: `web/src/pages/signal-detail-page.tsx` — detailed signal, LLM call/output, source triggers, related run/strategy links.
- Modify: `web/src/App.tsx`, `web/src/lib/api/types.ts`, `web/src/lib/api/client.ts`, `internal/api/signal_handlers.go`, `internal/signal/store.go`, `internal/signal/evaluator.go`, `internal/signal/watch_index.go`.
- Modify: `web/src/pages/discovery-page.tsx` — rate-limit messaging and safe retry/backoff UI.
- Modify: `web/src/pages/options-page.tsx`, `web/src/components/options/chain-table.tsx`, `internal/api/options_handlers.go`, `internal/data/options_provider.go` — scanner defaults, no-data diagnostics, expiry selector.
- Modify: `web/src/pages/calendar-page.tsx`, `web/src/components/calendar/upcoming-events-widget.tsx`, `internal/api/calendar_handlers.go`, `internal/data/events_provider.go` — clickable stocks, SEC filing errors, monthly calendar, analysis flags.
- Modify: `web/src/pages/universe-page.tsx`, `web/src/components/universe/watchlist-table.tsx`, `web/src/pages/stock-detail-page.tsx` — last scanned, strategy/position indicators, watchlist metrics/reasons, stock-detail chart and indicators.
- Test: existing page/component tests plus new signal-detail and stock chart tests.

### Polymarket and Automation

- Modify: `web/src/pages/polymarket-page.tsx` — scanner presets/defaults, tags, job layout, human schedules, last-run state, default markets, signal visibility, detail links.
- Modify: `web/src/pages/polymarket-account-page.tsx` — wallet trades and tags.
- Modify: `web/src/hooks/use-polymarket.ts`, `internal/api/polymarket_handlers.go`, `internal/api/polymarket_discovery_handlers.go`, `internal/automation/polymarket_discovery_*`, `internal/recorder/polymarket.go`, `internal/execution/polymarket/*`.
- Modify: `web/src/pages/automation-page.tsx`, `web/src/pages/automation-detail-page.tsx`, `internal/api/automation_handlers.go`, `internal/automation/orchestrator.go`, `internal/automation/jobs_*` — workflow triggers, resets, ratios, recent runs, human schedules, useful descriptions.
- Test: Polymarket frontend tests, automation frontend tests to create/extend, and relevant Go handler/orchestrator tests.

### Prompt Studio, Glossary, Reliability, Surfers Ops, Memories

- Modify: `internal/api/prompts.go`, prompt persistence, `web/src/pages/prompts-page.tsx`, `web/src/lib/api/types.ts`, `web/src/lib/api/client.ts` — labels/tags/layout/buttons/undo/mock preview.
- Modify: `web/src/pages/glossary-page.tsx`; create `web/src/pages/glossary-detail-page.tsx`; modify `web/src/App.tsx` — wiki-style glossary details with math and Augr links.
- Modify: `web/src/pages/reliability-page.tsx`, `web/src/pages/surfers-ops-page.tsx`, `web/src/pages/memories-page.tsx` and matching backend handlers only where real data exists but is not exposed.
- Test: prompts/glossary/reliability/memories frontend tests and prompt/memory Go handler tests.

### Backend Decision Semantics and Alerts

- Modify: `internal/agent/*`, especially trader/risk prompts and final signal parsing.
- Modify: runtime/execution code under `cmd/tradingagent/*`, `internal/execution/*`, and related runner files discovered during implementation.
- Modify: `internal/execution/order_manager.go` — truthful final signal confidence/action semantics.
- Modify: `internal/notification/discord.go`, `internal/notification/manager.go`, `internal/config/config.go` if multiple channels become first-class config.
- Modify: `internal/domain/agent.go`, `internal/repository/postgres/agent_decision.go` only if latency/confidence persistence is incorrect.
- Test: `internal/agent/risk/risk_manager_test.go`, notification tests, repository tests if touched, and runner tests for entry-evaluation vs held-position semantics.

---

## Phase 0: Scope Partition and Baseline

### Task 0.1: Convert Notes Into Tracked Work Items

**Files:** `.github/ISSUE_TEMPLATE/*.yml`, `docs/roadmap.md`, `docs/known-issues.md`, this roadmap.

- [ ] Create one epic/tracker item per workstream: Global UI, Risk/Safety, Realtime Runs, Strategy/Backtests, Data-Empty Pages, Signals/Research, Polymarket, Automation, Prompts/Glossary, Backend Decisions/Alerts.
- [ ] Mark P0 items with `priority:p0-critical` or the repo's closest equivalent.
- [ ] Keep implementation chunks independently testable; do not create a single mega-issue for all notes.
- [ ] Link this roadmap file from every created work item.
- [ ] Validation: every user note appears in the coverage matrix and has a destination workstream.

### Task 0.2: Record Baseline Reproductions

**Files:** create `docs/superpowers/plans/artifacts/2026-06-09-augr-product-ux-baseline.md` during execution if persistent reproduction notes are useful.

- [ ] Open each affected route and record URL, visible issue, console errors, network errors, and exact API response shape when data is empty.
- [ ] For backend-dependent pages, classify responses as empty arrays, errors, `501`, missing routes, or frontend filtering mistakes.
- [ ] For broken controls, record DOM target, disabled state, network request, response status, and mutation invalidation behavior.
- [ ] Validation: every implementation task starts with concrete reproduction notes rather than only the symptom.

---

## Phase 1: Global Shell and Safety Controls

### Task 1.1: Fix App Chrome, Page Height, Header/Footer, and Scrollbars

**Files:** `web/src/components/layout/app-shell.tsx`, `web/src/components/layout/page-header.tsx`, `web/src/index.css`, `web/src/components/layout/app-shell.test.tsx`.

- [ ] Remove or constrain the top radial/linear gradient so it cannot visually gray out interactive content and remains `pointer-events-none`.
- [ ] Make desktop shell fit within `100vh`; sidebar and main route content use local scroll containers when needed.
- [ ] Remove visible sidebar scrollbar by tightening spacing, item height, or grouping; if overflow remains necessary, use the small neubrutal scrollbar from `web/src/index.css`.
- [ ] Add a footer/status strip below route content with environment/auth/build/runtime hints.
- [ ] Preserve mobile nav accessibility and keyboard focus behavior.
- [ ] Run from `web/`: `npm test -- --run src/components/layout/app-shell.test.tsx` and `npm run lint`.

### Task 1.2: Normalize Kill-Switch Verbiage and State Semantics

**Files:** `web/src/components/dashboard/risk-status-bar.tsx`, `web/src/pages/risk-page.tsx`, `web/src/pages/settings-page.tsx`, optional `web/src/components/ui/button.tsx`, related tests.

- [ ] When trading is enabled, every emergency action button says **Stop All**, uses destructive/red styling, and reads as an emergency action.
- [ ] When trading is halted, state text says **Trading halted** and recovery action says **Resume Trading** or **Resume All** consistently.
- [ ] Remove user-facing **Kill switch active/inactive** labels where they describe the object rather than trading state; keep mechanism/source as secondary detail.
- [ ] Preserve `POST /api/v1/risk/killswitch`; this is wording/UI semantics unless backend fields prove insufficient.
- [ ] Add tests that fail if Settings regresses to `Activate`/`Deactivate` wording.
- [ ] Run from `web/`: `npm test -- --run src/components/dashboard/risk-status-bar.test.tsx src/pages/risk-page.test.tsx src/pages/settings-page.test.tsx`.

### Task 1.3: Add Audit Log Filters on Risk Page

**Files:** `web/src/pages/risk-page.tsx`, `web/src/lib/api/client.ts`, `web/src/lib/api/types.ts`, audit-log backend handler/repository if needed.

- [ ] Add filters for event type, actor/source if available, severity/category if available, and limit.
- [ ] Keep the current audit limit control but move it into a clear filter bar.
- [ ] If backend filtering is absent, add query params that match existing audit log fields instead of browser-only filtering.
- [ ] Validation: selecting a filter changes query key, request URL, and rendered entries.

---

## Phase 2: Data Availability and Empty-State Truthfulness

### Task 2.1: Build an Empty-Data Diagnostic Pass

**Files:** candidate pages `decision-journal-page.tsx`, `backtest-detail-page.tsx`, `signals-page.tsx`, `polymarket-page.tsx`, `polymarket-account-page.tsx`, `memories-page.tsx`, `surfers-ops-page.tsx`, `options-page.tsx`; matching backend handlers when needed.

- [ ] For each empty page, classify the cause: no seed/default config, provider not configured, backend route empty, frontend filters out data, missing persistence, or runtime never writes records.
- [ ] Update empty states to show classification and next operator action.
- [ ] Add setup/default actions only where safe; scanner defaults may populate fields but must not auto-submit scans or place orders.
- [ ] Validation: every no-data page displays a useful reason and safe next step.

### Task 2.2: Fix Cross-Flow Cockpit Zeros

**Files:** `web/src/pages/risk-page.tsx`, risk cockpit backend handler/service, related tests.

- [ ] Distinguish real zero exposure from unavailable data.
- [ ] Render `—` or unavailable state when a market has no backing data instead of implying all metrics are zero.
- [ ] Preserve true zero values for configured markets with confirmed no exposure.
- [ ] Add backend tests for no data, partial data, and true zero exposure.

### Task 2.3: Repair Journal, Backtest Detail, Signals, and Memories Data Paths

**Files:** `web/src/pages/decision-journal-page.tsx`, `backtest-detail-page.tsx`, `signals-page.tsx`, `memories-page.tsx`, matching backend handlers/repos.

- [ ] Journal: verify decisions are written, listed, and linked to runs; if not, fix the write path before adding more UI.
- [ ] Backtests: distinguish config detail from run detail; add/link run detail views only where run data exists.
- [ ] Signals: verify signal generation and persistence; if observed data is in-memory only, fix or clearly disclose retention.
- [ ] Memories: verify which runtime component creates memories; if no creator is wired, show disabled/unconfigured state before adding creation UI.
- [ ] Validation: pages show real data or precise, non-generic absence reasons.

---

## Phase 3: Strategy, Backtest, and Run Detail Workflow

### Task 3.1: Strategy List Cards Show Last-Run Outcome

**Files:** `web/src/pages/strategies-page.tsx`, optional API/types/client and `internal/api/strategy_handlers.go` if enrichment is needed.

- [ ] Each strategy card shows last run status, signal/outcome, timestamp, and run-detail link when available.
- [ ] Avoid frontend N+1 queries when backend can cheaply include last-run summaries.
- [ ] Empty state says **No runs yet**.

### Task 3.2: Strategy Detail Summary, Rules Table, Backtests, Orders, and Buttons

**Files:** `web/src/pages/strategy-detail-page.tsx`, `web/src/components/strategies/strategy-run-history.tsx`, optional `strategy-config-editor.tsx`, related tests.

- [ ] Add human-readable strategy summary covering purpose, goal, parameters, triggers, thesis, schedule, paper/live mode, and risk constraints.
- [ ] Convert rules engine to compact tables: group, field, operator, value/reference, human explanation.
- [ ] Add a short human-readable rules summary above tables.
- [ ] Render linked backtests above recent orders.
- [ ] Filter recent orders by both `strategy_id` and strategy ticker/instrument when API supports it; add backend filter support if needed.
- [ ] Make run-history rows link to `/runs/:id`.
- [ ] Add delete confirmation and inline mutation errors.
- [ ] Make Pause/Resume reactive: show only valid primary lifecycle action, or disable invalid action with explanation.
- [ ] Add tests for Run now, Pause/Resume, Skip next, Delete, run-history navigation, backtest order, and ticker-filtered orders.

### Task 3.3: Run Detail Markdown and Equal Phase Cards

**Files:** `web/src/pages/pipeline-run-page.tsx`, `web/src/components/pipeline/phase-progress.tsx`, `web/src/components/pipeline/decision-inspector.tsx`, related tests.

- [ ] Make phase cards equal width/height at each breakpoint.
- [ ] Use `react-markdown` for LLM output and prompt previews where content is trusted plain markdown; keep raw JSON in `pre` blocks.
- [ ] Add markdown styling for headings, lists, tables, code, and links.
- [ ] Add tests with markdown response content and long phase labels/latency values.

---

## Phase 4: Realtime Event Feed Overhaul

### Task 4.1: Define Run-Centric Event Model

**Files:** `web/src/pages/realtime-page.tsx`, API types/client, backend event handler if needed, tests.

- [ ] Treat one feed row as a whole pipeline run when `pipeline_run_id` is present.
- [ ] Row summary includes ticker, strategy, status, current phase, signal if known, timestamps, and latest notable event.
- [ ] Preserve standalone system events not tied to a run and visually separate them from run events.
- [ ] Selecting a row updates local state only and does not scroll the page body.

### Task 4.2: Display Phases, State, Current Info, and Conversation by Selected Run

**Files:** `web/src/pages/realtime-page.tsx`, `web/src/components/chat/chat-panel.tsx`, conversation/event backend handlers if needed, tests.

- [ ] Selected run panel shows phase progress, latest state, latest event metadata, status, and links to `/runs/:id` and `/strategies/:id` when available.
- [ ] Debate messages auto-populate the conversation area chronologically.
- [ ] All phases can be viewed as conversation transcripts: analysis, debate, trading, risk, signal.
- [ ] Selection does not create empty persisted conversations; use read-only transcript rendering unless the user explicitly starts a chat.
- [ ] Remove auto-scroll behavior that fights layout; local containers may auto-scroll only when the user is already at the bottom.

---

## Phase 5: Research and Market Data Surfaces

### Task 5.1: Signal Detail Page and Watch-Term Improvements

**Files:** `signals-page.tsx`, create `signal-detail-page.tsx`, `App.tsx`, API types/client, `internal/api/signal_handlers.go`, `internal/signal/*`, tests.

- [ ] Add signal detail route with source trigger, strategy/run links, confidence, LLM call, LLM output, timestamps, downstream action/result.
- [ ] Show whether trigger-log length is constrained by retention, pagination, provider freshness, or actual event count.
- [ ] Add base market watch terms shared across markets.
- [ ] Generate stock/strategy terms from ticker, company name, sector, rules-engine indicators, strategy thesis, and manual terms.
- [ ] Test base terms, derived terms, and manual override precedence.

### Task 5.2: Discovery, Options, and Calendar Data UX

**Files:** `discovery-page.tsx`, `options-page.tsx`, `components/options/chain-table.tsx`, `calendar-page.tsx`, `components/calendar/upcoming-events-widget.tsx`, backend handlers/providers as needed.

- [ ] Discovery rate-limit errors show provider, cooldown/retry guidance, and a retry button that respects backoff.
- [ ] Options scanner has safe default configuration and explains provider/config requirements when data is unavailable.
- [ ] Options chain supports expiry selection without leaving stock context.
- [ ] Calendar stocks link to stock detail.
- [ ] SEC filing errors show provider/config details instead of generic failure.
- [ ] Calendar includes a monthly view.
- [ ] Event add/analysis flags are persisted and auditable if they trigger jobs; otherwise show a disabled planned state before storage work.

### Task 5.3: Universe and Stock Detail Improvements

**Files:** `universe-page.tsx`, `components/universe/watchlist-table.tsx`, `stock-detail-page.tsx`, backend universe/market-data handlers if fields are absent.

- [ ] Universe rows show last scanned and whether a strategy or position exists.
- [ ] Watchlist rows show change, gap, volume, close, and reasons.
- [ ] Watchlist clicks route to stock detail, or Discovery is prefilled if Discovery remains the destination.
- [ ] Stock detail chart supports chart timeframe, candle timeframe, zoom/scroll, common indicators, and strategy-driven indicator autoselect.

---

## Phase 6: Polymarket and Automation

### Task 6.1: Polymarket Defaults, Jobs, Signals, and Detail Paths

**Files:** `polymarket-page.tsx`, `polymarket-account-page.tsx`, `hooks/use-polymarket.ts`, Polymarket API/discovery/automation/recorder/execution files, tests.

- [ ] Add pre-created scanner settings that populate fields without submitting.
- [ ] Add a default scanner configuration.
- [ ] Show tags for tracked wallets.
- [ ] Fix job badge layout so enabled/running badges never overlap Run Now.
- [ ] Display job timer human-readably and retain raw cron in tooltip/secondary text.
- [ ] Include last-run success/error state.
- [ ] Add server-side base default markets to watch.
- [ ] Audit 200-market fetch / 15-screen / six-hour schedule against fast-reaction strategy requirements and display operating mode.
- [ ] Verify whether Polymarket signals are generated; show generation health and last signal timestamp.
- [ ] Add detail pages only for stable IDs with useful supporting data.
- [ ] Wallet detail shows trades or a precise reason trades are unavailable.

### Task 6.2: Automation Workflows, Resets, Ratios, and Detail Stats

**Files:** `automation-page.tsx`, `automation-detail-page.tsx`, `lib/cron-describe.ts`, automation API/orchestrator/job-run persistence files.

- [ ] Add ideal workflow examples: universe refresh, deep scan, discovery run, options refresh, Polymarket discovery, pre-market prep, post-market review.
- [ ] Workflow examples trigger safe job sequences and display queued/running state.
- [ ] Add reset actions for all jobs and individual jobs with last reset timestamp.
- [ ] Track global and per-job success/error ratios.
- [ ] Convert schedules to human-readable descriptions while keeping raw cron visible.
- [ ] Replace over-truncated descriptions with expandable or multi-line descriptions.
- [ ] Automation detail lists recent runs and richer stats.

---

## Phase 7: Prompt Studio, Glossary Wiki, Reliability, Surfers Ops

### Task 7.1: Prompt Studio Metadata, Layout, Undo, and Mock Preview

**Files:** `internal/api/prompts.go`, prompt persistence, `prompts-page.tsx`, API types/client, tests.

- [ ] Add prompt name/label and tags fields while preserving category and key.
- [ ] Existing prompts are labeled and tagged `default` during migration or response normalization.
- [ ] State reflects active prompt name/label.
- [ ] Top details are thirds: category, key, state.
- [ ] Detail layout order: Default Prompt full-width, then Override text two-thirds with Saved overrides one-third, then Effective preview.
- [ ] Text boxes are as wide as their containers.
- [ ] Save prompt and reset prompt buttons live in detail panel.
- [ ] Remove Reset selected from top bar.
- [ ] Add undo for unsaved prompt edits.
- [ ] Effective preview includes example mock data passed to the agent.

### Task 7.2: Glossary Wiki Detail Pages

**Files:** `glossary-page.tsx`, create `glossary-detail-page.tsx`, `App.tsx`, optional `web/src/data/glossary.ts`, tests.

- [ ] Keep the index, but link terms to detail pages.
- [ ] Explain concepts at novice/moderate trading level.
- [ ] Include formulas/math and explain every variable.
- [ ] Cite fact-based educational sources in content notes when adding externally derived explanations.
- [ ] Link to relevant Augr pages: strategies, signals, glossary neighbors, stock detail, backtests, options, risk.

### Task 7.3: Reliability, Surfers Ops, and Memories Detail

**Files:** `reliability-page.tsx`, `surfers-ops-page.tsx`, `memories-page.tsx`, matching backend handlers only where real data exists but is not exposed.

- [ ] Reliability shows service health, last successful jobs, recent failures, provider configuration, websocket health, signal hub health, and stale-run reconciliation state where available.
- [ ] Surfers Ops shows summary, description, last run, current status, and why no data is present when empty.
- [ ] Memories explains whether memory creation is disabled, unwired, or empty because no qualifying runs have occurred.
- [ ] If memories should be created, add a backend test proving a qualifying event writes a memory and the page can list it.

---

## Phase 8: Backend Decision Semantics, Prompt Inputs, and Discord Alerts

### Task 8.1: Split New-Buy Evaluation From Portfolio Management Semantics

**Files:** `internal/agent/trader/*`, `internal/agent/risk/*`, runner/execution files, optional `internal/domain/agent.go`, tests.

- [ ] Define decision contexts: **entry evaluation** for stocks not owned, **position management** for owned stocks, and **re-evaluate later** for insufficient evidence.
- [ ] Entry evaluation outcomes must not include misleading sell action against a non-owned stock.
- [ ] Preserve risk/order guardrails, but make LLM-visible prompts and parsed final decisions semantically correct.
- [ ] UI and Discord alerts reflect **No Action** or **Re-evaluate** when no position exists.
- [ ] Test that non-owned bearish evidence produces no-action/re-evaluate rather than sell.
- [ ] Test that owned portfolio stock can still produce sell when portfolio-management context applies.

### Task 8.2: Fix Prompt Data Completeness, Latency, and Confidence Plumbing

**Files:** prompt construction/runtime code, `internal/domain/agent.go`, `internal/repository/postgres/agent_decision.go`, `internal/execution/order_manager.go`, optional `decision-inspector.tsx`.

- [ ] Audit prompt inputs for price history, indicators, fundamentals, news, social sentiment, positions, orders, risk state, strategy rules, backtests, and signal/watch context.
- [ ] Store prompt input summaries/full prompt text consistently enough for run detail and signal detail inspection.
- [ ] Replace false `0ms` latency with measured latency or explicit unknown/unmeasured state.
- [ ] Replace false `0.0%` confidence with parsed confidence, normalized confidence, or explicit unknown/unavailable state.
- [ ] Add repository tests so unknown latency/confidence do not serialize as misleading zero values unless zero is real.

### Task 8.3: Improve Discord Alert Formatting and Channel Routing

**Files:** `internal/notification/discord.go`, `internal/notification/manager.go`, optional `internal/config/config.go`, tests.

- [ ] Decision alerts send only when decision webhook/channel is configured and runtime emits decision events.
- [ ] Alerts are summarized in human-readable form instead of raw unstructured dumps.
- [ ] Support channel/webhook slots for signals, decisions, critical risk alerts, operational failures, and discovery/research summaries.
- [ ] Include run, strategy, ticker, signal, confidence, latency, and action semantics.
- [ ] Test missing webhook no-op, formatted signal, formatted decision, rate-limit retry, and unknown latency/confidence display.

### Task 8.4: Diagnose Missing Buy/Sell Signals Since Recent Fixes

**Files:** candidate `cmd/tradingagent/prod_strategy_runner.go`, `internal/execution/order_manager.go`, `internal/signal/evaluator.go`, `internal/risk/*`, `internal/repository/postgres/pipeline_run.go`.

- [ ] Build a timeline of the two same-day buys and subsequent absence of buy/sell signals from persisted runs, decisions, signals, orders, and Discord sends.
- [ ] Check risk thresholds, min confidence, strategy paused state, data provider failures, prompt parsing, and cooldowns.
- [ ] Add a regression test for the actual root cause once identified.
- [ ] Do not loosen risk thresholds as a blind fix.

---

## Coverage Matrix for User Notes

| User area | Covered by |
| --- | --- |
| Main view faded/greyed/unclickable gradient, sidebar scrollbar, page height, header/footer, small scrollbar | Task 1.1 |
| Settings/sitewide kill switch red Stop All, state semantics | Task 1.2 |
| Cross-flow cockpit all zeros | Task 2.2 |
| Warning: no trade decisions available | Task 2.3, Task 8.2 |
| Audit log filter | Task 1.3 |
| Realtime event feed as whole run, phases/state/current info/conversation, scroll-on-select | Task 4.1, Task 4.2 |
| Reliability more detail | Task 7.3 |
| Strategies last-run outcome | Task 3.1 |
| Strategy detail summary, rules table, backtests above orders, ticker-filtered orders, run links, delete/pause/resume/buttons | Task 3.2 |
| Run detail equal step cards and markdown | Task 3.3 |
| Journal empty, backtest detail empty, signals/memories missing | Task 2.3 |
| Signal detail page, trigger log, auto-derived terms | Task 5.1 |
| Polymarket presets/defaults/tags/jobs/default markets/signals/details/wallet trades/polling | Task 6.1 |
| Discovery rate limit | Task 5.2 |
| Options defaults/no data/expiry selector | Task 5.2 |
| Calendar clickable stocks/SEC filings/month view/events/analysis flags | Task 5.2 |
| Universe last scanned/strategy-position indicators/watchlist metrics/better links | Task 5.3 |
| Stock detail interactive chart/indicators | Task 5.3 |
| Automation workflows/resets/ratios/human schedules/descriptions/detail stats | Task 6.2 |
| Surfers Ops no data/summary | Task 7.3 |
| Prompts label/tags/layout/buttons/undo/mock preview | Task 7.1 |
| Glossary wiki/detail/math/links | Task 7.2 |
| Backend buy/sell semantics, prompt data, latency/confidence, Discord formatting/channels, missing buy/sell signals | Task 8.1, Task 8.2, Task 8.3, Task 8.4 |

## Recommended First Implementation Tranche

Start with a bounded P0/P1 tranche before the larger realtime or backend prompt rewrites:

1. Task 1.1 — app chrome, sidebar, scrollbars, bounded layout.
2. Task 1.2 — kill-switch wording and red Stop All consistency.
3. Task 1.3 — audit log filters.
4. Task 2.1 — data-empty diagnostic pass for the most visibly empty pages.
5. Task 3.2 small sub-slice — strategy run-history links and lifecycle button reactivity.

This tranche is mostly frontend, visibly improves operator experience, and avoids changing trading semantics before a focused backend design/test pass.

## Validation Commands

- Frontend focused examples from `web/`: `npm test -- --run src/components/layout/app-shell.test.tsx src/pages/risk-page.test.tsx src/pages/settings-page.test.tsx`.
- Frontend realtime examples from `web/`: `npm test -- --run src/pages/realtime-page.test.tsx src/pages/pipeline-run-page.test.tsx`.
- Frontend strategy examples from `web/`: `npm test -- --run src/pages/strategy-detail-page.test.tsx src/components/strategies/strategy-run-history.test.tsx`.
- Frontend broad checks from `web/`: `npm run lint` and `npm run build`.
- Backend focused examples from repo root: `go test ./internal/api ./internal/automation ./internal/signal ./internal/notification ./internal/agent/risk ./internal/repository/postgres`.
- Broader backend checks when backend behavior changes: `task test` and `task build`.

## Review Gates

- **Gate 1:** User approves this roadmap and selects first tranche.
- **Gate 2:** After Task 1.1 and Task 1.2, review UI behavior before continuing into page-specific fixes.
- **Gate 3:** Before Task 4 realtime overhaul, approve the run-centric event model and whether new backend endpoints are acceptable.
- **Gate 4:** Before Task 8 backend semantics, approve final action vocabulary for entry evaluation versus portfolio management.
- **Gate 5:** Before adding many detail pages, verify list pages have stable IDs, populated data, and useful drilldown content.

## Definition of Done

- Every completed task includes focused tests or a written reason tests are not possible.
- Empty states tell the operator why data is absent and what to do next.
- No emergency trading UI uses ambiguous kill-switch wording.
- No signal/alert displays false zero latency or false zero confidence.
- No sell signal is emitted for a non-owned stock in an entry-evaluation context.
- Route selections and event selections do not scroll the page body unexpectedly.
- New detail pages are linked from their list pages and have stable route tests.
- Docs are updated when maturity claims change, especially `docs/known-issues.md` and `docs/roadmap.md`.

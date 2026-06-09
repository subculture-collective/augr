# Augr Follow-On Phases Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Finish the post-foundation Augr trading platform phases after the deployed Phases 0-8 foundation.

**Architecture:** Preserve the paper-first, journal-first platform spine that is already deployed. Add read-only scanner surfaces before any new execution features, use the trade-decision journal as the audit spine, and keep live-capable work behind the existing backend live gate and allowlists.

**Tech Stack:** Go 1.25, chi, PostgreSQL 17 with TimescaleDB and pgvector, Redis 7, React/Vite/TypeScript, TanStack Query, Docker Compose production stack.

---

## Current Baseline

Completed and deployed:

- Phase 0: `docs/AUGR_ARCHITECTURE_AUDIT.md`
- Phase 1: `internal/edge` EV, Kelly, calibration, options pricing, Greeks, realized volatility
- Phase 2: trade-decision journal domain, migration 43, repository, API, runtime wiring
- Phase 3: execution decision recorder and deny-by-default live gate
- Phase 4: Polymarket paper scanner and maker-first paper fill simulator
- Phase 5: options paper scanner for long calls, long puts, and balanced debit verticals
- Phase 6: calibration reports and deterministic regime pause rules
- Phase 7: `/journal` operator UI
- Phase 8: docs-contract cleanup after `docs/reference/*` and `docs/research/*` deletion

Remaining follow-on phases:

1. Phase 9: scanner API/UI surfacing
2. Phase 10: replay workbench
3. Phase 11: cross-flow risk cockpit
4. Phase 12: official Polymarket CLOB adapter hardening behind the live gate
5. Phase 13: advanced research lanes as paper/research-only modules

## Safety Rules for Every Phase

- No new live order path may bypass `internal/execution/live_gate.go`.
- No strategy or research module may place live orders directly.
- New scanner APIs start read-only and return journal-ready candidates.
- Any persisted decision-like record must avoid `NaN`, `Inf`, and empty identifiers.
- Any live-capable Polymarket work must default to paper/dry-run unless both `ENABLE_LIVE_TRADING=true` and explicit allowlists approve it.
- New migrations must bump `internal/repository/postgres/schema_version.go` and include focused repository tests.
- Production deploy must verify `/healthz` and latest `schema_migrations.version`.

---

## File Structure Map

### Phase 9: Scanner API/UI Surfacing

- Modify: `internal/api/server.go` — mount read-only scanner endpoints.
- Create: `internal/api/research_handlers.go` — options and Polymarket scanner handlers.
- Create: `internal/api/research_handlers_test.go` — route, validation, and failure tests.
- Modify: `cmd/tradingagent/runtime.go` — wire scanner handler dependencies.
- Create: `internal/service/research_scanners.go` — small service boundary over options and Polymarket scanner packages.
- Create: `internal/service/research_scanners_test.go` — deterministic service tests.
- Modify: `web/src/lib/api/types.ts` — scanner request/response types.
- Modify: `web/src/lib/api/client.ts` — read-only scanner client methods.
- Modify: `web/src/lib/api/client.test.ts` — request URL and query tests.
- Modify: `web/src/pages/options-page.tsx` — options opportunities card/table.
- Modify: `web/src/pages/polymarket-page.tsx` — Polymarket opportunities card/table.

### Phase 10: Replay Workbench

- Create: `internal/domain/replay.go` — replay event and replay summary shapes.
- Create: `migrations/000044_replay_events.up.sql` — replay event table.
- Create: `migrations/000044_replay_events.down.sql` — rollback for replay table.
- Modify: `internal/repository/postgres/schema_version.go` — bump to 44.
- Modify: `internal/repository/interfaces.go` — replay repository interface.
- Create: `internal/repository/postgres/replay_event.go` — replay repository.
- Create: `internal/repository/postgres/replay_event_test.go` — query/scan tests.
- Create: `internal/replay/workbench.go` — decision timeline builder.
- Create: `internal/replay/workbench_test.go` — deterministic replay timeline tests.
- Create: `internal/api/replay_handlers.go` — `GET /api/v1/replay/decisions/{id}`.
- Create: `internal/api/replay_handlers_test.go` — API tests.
- Create: `web/src/pages/replay-page.tsx` — read-only replay workbench.
- Modify: `web/src/App.tsx` and `web/src/components/layout/app-shell.tsx` — route and nav.

### Phase 11: Cross-Flow Risk Cockpit

- Create: `internal/risk/cockpit.go` — aggregate risk cockpit service.
- Create: `internal/risk/cockpit_test.go` — aggregation and safety tests.
- Create: `internal/api/risk_cockpit_handlers.go` — `GET /api/v1/risk/cockpit`.
- Create: `internal/api/risk_cockpit_handlers_test.go` — handler tests.
- Modify: `internal/api/server.go` — mount cockpit route.
- Modify: `web/src/lib/api/types.ts` — cockpit response types.
- Modify: `web/src/lib/api/client.ts` — cockpit client method.
- Modify: `web/src/pages/risk-page.tsx` — cockpit summary panels.

### Phase 12: Official Polymarket CLOB Adapter Hardening

- Create: `internal/data/polymarket/gamma_client.go` — Gamma metadata client.
- Create: `internal/data/polymarket/clob_client.go` — CLOB read/write client boundary.
- Create: `internal/data/polymarket/orderbook.go` — official order-book normalization.
- Create: `internal/data/polymarket/client_test.go` — fixture-backed HTTP tests.
- Modify: `internal/execution/polymarket/broker.go` — use official client boundary behind existing broker interface.
- Modify: `internal/execution/polymarket/broker_test.go` — dry-run, post-only, and failure tests.
- Modify: `cmd/tradingagent/prod_strategy_runner.go` — preserve live-gate construction for Polymarket broker.
- Modify: `docs/known-issues.md` — update Polymarket maturity notes after tests pass.

### Phase 13: Advanced Research Lanes

- Create: `internal/research/walletintel/` — wallet-profile scoring without copy-trading execution.
- Create: `internal/research/eventcalibration/` — event/weather evidence calibration primitives.
- Create: `internal/research/solverarb/` — offline solver-arbitrage research model with no execution path.
- Create: `internal/research/latency/` — near-resolution latency simulator with tail-risk reporting.
- Create tests in each package.
- Create read-only report artifacts through existing `internal/automation/jobs_reports.go` only after package tests pass.

---

## Phase 9: Scanner API/UI Surfacing

### Task 9.1: Research Scanner Service Boundary

**Files:**
- Create: `internal/service/research_scanners.go`
- Create: `internal/service/research_scanners_test.go`

- [ ] **Step 1: Add service request and response structs**

Create `internal/service/research_scanners.go` with this package boundary:

```go
package service

import (
    "context"

    "github.com/google/uuid"

    "github.com/subculture/augr/internal/domain"
)

type OptionsOpportunityRequest struct {
    Underlying string
    StrategyID *uuid.UUID
    Limit      int
}

type PolymarketOpportunityRequest struct {
    Slug       string
    TokenID    string
    StrategyID *uuid.UUID
    Limit      int
}

type ResearchOpportunity struct {
    Decision domain.TradeDecision `json:"decision"`
    Reasons  []string             `json:"reasons"`
    Accepted bool                 `json:"accepted"`
}

type ResearchScannerService interface {
    ScanOptions(ctx context.Context, req OptionsOpportunityRequest) ([]ResearchOpportunity, error)
    ScanPolymarket(ctx context.Context, req PolymarketOpportunityRequest) ([]ResearchOpportunity, error)
}
```

- [ ] **Step 2: Add deterministic empty implementation for wiring tests**

In the same file add:

```go
type StaticResearchScannerService struct {
    Options     []ResearchOpportunity
    Polymarket  []ResearchOpportunity
    OptionsErr  error
    PolyErr     error
}

func (s StaticResearchScannerService) ScanOptions(ctx context.Context, req OptionsOpportunityRequest) ([]ResearchOpportunity, error) {
    if s.OptionsErr != nil {
        return nil, s.OptionsErr
    }
    return limitResearchOpportunities(s.Options, req.Limit), nil
}

func (s StaticResearchScannerService) ScanPolymarket(ctx context.Context, req PolymarketOpportunityRequest) ([]ResearchOpportunity, error) {
    if s.PolyErr != nil {
        return nil, s.PolyErr
    }
    return limitResearchOpportunities(s.Polymarket, req.Limit), nil
}

func limitResearchOpportunities(items []ResearchOpportunity, limit int) []ResearchOpportunity {
    if limit <= 0 || limit >= len(items) {
        return items
    }
    return items[:limit]
}
```

- [ ] **Step 3: Test limit behavior**

Create `internal/service/research_scanners_test.go`:

```go
package service

import (
    "context"
    "testing"

    "github.com/subculture/augr/internal/domain"
)

func TestStaticResearchScannerServiceLimitsOptions(t *testing.T) {
    svc := StaticResearchScannerService{Options: []ResearchOpportunity{
        {Decision: domain.TradeDecision{InstrumentKey: "A"}, Accepted: true},
        {Decision: domain.TradeDecision{InstrumentKey: "B"}, Accepted: true},
    }}

    got, err := svc.ScanOptions(context.Background(), OptionsOpportunityRequest{Underlying: "AAPL", Limit: 1})
    if err != nil {
        t.Fatalf("ScanOptions() error = %v", err)
    }
    if len(got) != 1 || got[0].Decision.InstrumentKey != "A" {
        t.Fatalf("ScanOptions() = %#v", got)
    }
}
```

- [ ] **Step 4: Run service tests**

Run: `rtk go test ./internal/service -run ResearchScanner`

Expected: service package tests pass.

### Task 9.2: Read-Only Scanner API

**Files:**
- Modify: `internal/api/server.go`
- Create: `internal/api/research_handlers.go`
- Create: `internal/api/research_handlers_test.go`

- [ ] **Step 1: Add API dependency**

Add an optional field to `api.Deps` and `Server`:

```go
ResearchScanners service.ResearchScannerService
```

Import `github.com/subculture/augr/internal/service` if not already present.

- [ ] **Step 2: Mount read-only routes**

In the authenticated route group add:

```go
r.Route("/research", func(r chi.Router) {
    r.Get("/options/opportunities/{underlying}", s.handleScanOptionsOpportunities)
    r.Get("/polymarket/opportunities", s.handleScanPolymarketOpportunities)
})
```

- [ ] **Step 3: Implement handlers**

Create `internal/api/research_handlers.go` with handlers that:

- return `501` if `s.ResearchScanners == nil`
- parse `limit` with the existing pagination helper pattern and cap it at 100
- parse optional `strategy_id` as UUID
- for options, require `{underlying}` path param after trimming whitespace
- for Polymarket, require at least one of `slug` or `token_id`
- return `respondList(w, items, len(items), limit, offset)` using existing response helpers

- [ ] **Step 4: Add handler tests**

Create tests for:

- nil scanner service returns `501`
- options route passes `underlying`, `strategy_id`, and `limit`
- Polymarket route rejects missing `slug` and `token_id` with `400`
- scanner service error maps to `500`

- [ ] **Step 5: Run API tests**

Run: `rtk go test ./internal/api -run Research`

Expected: handler tests pass.

### Task 9.3: Runtime Wiring Without Scheduled Execution

**Files:**
- Modify: `cmd/tradingagent/runtime.go`

- [ ] **Step 1: Wire a conservative scanner service**

Set `api.Deps.ResearchScanners` to a service implementation that returns empty lists until provider-backed scanning is wired. This makes endpoints available without implying live scanner readiness.

Use:

```go
ResearchScanners: service.StaticResearchScannerService{},
```

- [ ] **Step 2: Run runtime compile tests**

Run: `rtk go test ./cmd/tradingagent -run Runtime`

Expected: package compiles and runtime-focused tests pass or report no matching tests.

### Task 9.4: Frontend Scanner Client and UI Cards

**Files:**
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/lib/api/client.ts`
- Modify: `web/src/lib/api/client.test.ts`
- Modify: `web/src/pages/options-page.tsx`
- Modify: `web/src/pages/polymarket-page.tsx`

- [ ] **Step 1: Add frontend types**

Add:

```ts
export interface ResearchOpportunity {
  decision: TradeDecision
  reasons: string[]
  accepted: boolean
}

export interface ScannerQuery {
  limit?: number
  strategy_id?: string
}

export interface PolymarketScannerQuery extends ScannerQuery {
  slug?: string
  token_id?: string
}
```

- [ ] **Step 2: Add client methods**

Add methods:

```ts
listOptionsOpportunities(underlying: string, query?: ScannerQuery): Promise<ListResponse<ResearchOpportunity>>
listPolymarketOpportunities(query: PolymarketScannerQuery): Promise<ListResponse<ResearchOpportunity>>
```

- [ ] **Step 3: Add client tests**

Assert URLs:

- `/research/options/opportunities/AAPL?limit=25`
- `/research/polymarket/opportunities?slug=btc-up-or-down&limit=25`

- [ ] **Step 4: Add read-only opportunity cards**

On options and Polymarket pages, render cards with:

- accepted/rejected badge
- instrument key
- net EV
- approved size
- risk reasons
- link to `/journal` for audit history

Do not add order buttons.

- [ ] **Step 5: Run frontend checks**

Run:

```bash
cd web
rtk lint
rtk npm run build
rtk npm test -- --run src/lib/api/client.test.ts
```

Expected: lint and build pass; client tests pass.

---

## Phase 10: Replay Workbench

### Task 10.1: Replay Domain and Migration

**Files:**
- Create: `internal/domain/replay.go`
- Create: `migrations/000044_replay_events.up.sql`
- Create: `migrations/000044_replay_events.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`

- [ ] **Step 1: Add replay domain structs**

Create:

```go
package domain

import (
    "encoding/json"
    "time"

    "github.com/google/uuid"
)

type ReplayEventType string

const (
    ReplayEventDecisionCreated ReplayEventType = "decision_created"
    ReplayEventRiskReviewed    ReplayEventType = "risk_reviewed"
    ReplayEventPaperOrdered    ReplayEventType = "paper_ordered"
    ReplayEventLiveOrdered     ReplayEventType = "live_ordered"
    ReplayEventFillObserved    ReplayEventType = "fill_observed"
    ReplayEventPositionUpdated ReplayEventType = "position_updated"
    ReplayEventOutcomeResolved ReplayEventType = "outcome_resolved"
)

type ReplayEvent struct {
    ID              uuid.UUID       `json:"id"`
    TradeDecisionID uuid.UUID       `json:"trade_decision_id"`
    EventType       ReplayEventType `json:"event_type"`
    Source          string          `json:"source"`
    Payload         json.RawMessage `json:"payload"`
    OccurredAt      time.Time       `json:"occurred_at"`
    CreatedAt       time.Time       `json:"created_at"`
}
```

- [ ] **Step 2: Add migration 44**

Create `replay_events` with:

- UUID primary key default `gen_random_uuid()`
- `trade_decision_id UUID NOT NULL REFERENCES trade_decisions(id) ON DELETE CASCADE`
- `event_type TEXT NOT NULL`
- `source TEXT NOT NULL DEFAULT 'system'`
- `payload JSONB NOT NULL DEFAULT '{}'::jsonb`
- `occurred_at TIMESTAMPTZ NOT NULL`
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- index on `(trade_decision_id, occurred_at)`
- check on the listed event types

- [ ] **Step 3: Bump schema version**

Set `RequiredSchemaVersion = 44`.

- [ ] **Step 4: Run migration compile tests**

Run: `rtk go test ./internal/domain ./internal/repository/postgres -run SchemaVersion`

Expected: schema version tests pass.

### Task 10.2: Replay Repository and Workbench API

**Files:**
- Modify: `internal/repository/interfaces.go`
- Create: `internal/repository/postgres/replay_event.go`
- Create: `internal/repository/postgres/replay_event_test.go`
- Create: `internal/replay/workbench.go`
- Create: `internal/replay/workbench_test.go`
- Create: `internal/api/replay_handlers.go`
- Create: `internal/api/replay_handlers_test.go`

- [ ] **Step 1: Add repository interface**

Methods:

```go
CreateReplayEvent(ctx context.Context, event *domain.ReplayEvent) error
ListReplayEvents(ctx context.Context, tradeDecisionID uuid.UUID) ([]domain.ReplayEvent, error)
```

- [ ] **Step 2: Add timeline builder**

`internal/replay/workbench.go` must sort events by `OccurredAt`, include the source trade decision, and produce a finite JSON-safe summary.

- [ ] **Step 3: Add API endpoint**

Mount:

```go
r.Get("/replay/decisions/{id}", s.handleGetDecisionReplay)
```

Return `404` for missing trade decision and `501` if replay dependencies are absent.

- [ ] **Step 4: Run replay tests**

Run: `rtk go test ./internal/replay ./internal/api ./internal/repository/postgres -run Replay`

Expected: replay tests pass.

### Task 10.3: Replay UI

**Files:**
- Create: `web/src/pages/replay-page.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/layout/app-shell.tsx`
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/lib/api/client.ts`

- [ ] **Step 1: Add route**

Route: `/replay/decisions/:id`

- [ ] **Step 2: Link from journal page**

In `web/src/pages/decision-journal-page.tsx`, add a read-only `Replay` link for each decision.

- [ ] **Step 3: Build timeline page**

Render decision summary, ordered event timeline, payload preview, loading, error, and empty states. Never show mutation buttons.

- [ ] **Step 4: Run frontend checks**

Run `rtk lint`, `rtk npm run build`, and targeted API client tests from `web`.

---

## Phase 11: Cross-Flow Risk Cockpit

### Task 11.1: Risk Cockpit Service

**Files:**
- Create: `internal/risk/cockpit.go`
- Create: `internal/risk/cockpit_test.go`

- [ ] **Step 1: Add response shape**

Create:

```go
type CockpitExposure struct {
    MarketType        domain.MarketType `json:"market_type"`
    OpenPositions     int               `json:"open_positions"`
    ApprovedDecisions int               `json:"approved_decisions"`
    RejectedDecisions int               `json:"rejected_decisions"`
    GrossExposure     float64           `json:"gross_exposure"`
    NetExpectedValue  float64           `json:"net_expected_value"`
}

type CockpitSummary struct {
    GeneratedAt     time.Time          `json:"generated_at"`
    KillSwitchActive bool              `json:"kill_switch_active"`
    CircuitBreaker   bool              `json:"circuit_breaker"`
    Exposures        []CockpitExposure `json:"exposures"`
    Warnings         []string          `json:"warnings"`
}
```

- [ ] **Step 2: Aggregate finite values only**

Skip non-finite EV/exposure inputs. Sort exposures by market type for deterministic output.

- [ ] **Step 3: Add tests**

Cover empty state, mixed market types, non-finite inputs, active kill switch, and deterministic ordering.

### Task 11.2: Risk Cockpit API/UI

**Files:**
- Create: `internal/api/risk_cockpit_handlers.go`
- Create: `internal/api/risk_cockpit_handlers_test.go`
- Modify: `internal/api/server.go`
- Modify: `web/src/pages/risk-page.tsx`
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/lib/api/client.ts`

- [ ] **Step 1: Add API route**

Mount `GET /api/v1/risk/cockpit`.

- [ ] **Step 2: Render cockpit panels**

On the Risk page add market-type cards for stock, crypto, options, and Polymarket. Show exposure, net EV, rejected decisions, warnings, and kill-switch state.

- [ ] **Step 3: Run checks**

Run:

```bash
rtk go test ./internal/risk ./internal/api -run Cockpit
cd web && rtk lint && rtk npm run build
```

---

## Phase 12: Official Polymarket CLOB Adapter Hardening

### Task 12.1: Official Client Boundary

**Files:**
- Create: `internal/data/polymarket/gamma_client.go`
- Create: `internal/data/polymarket/clob_client.go`
- Create: `internal/data/polymarket/orderbook.go`
- Create: `internal/data/polymarket/client_test.go`

- [ ] **Step 1: Add official client interfaces**

Define:

```go
type GammaClient interface {
    GetMarket(ctx context.Context, slug string) (GammaMarket, error)
}

type CLOBClient interface {
    GetOrderBook(ctx context.Context, tokenID string) (domain.PolymarketBookSnapshot, error)
    SubmitOrder(ctx context.Context, intent domain.PolymarketIntent) (domain.Order, error)
    CancelOrder(ctx context.Context, orderID string) error
}
```

- [ ] **Step 2: Add fixture-backed HTTP tests**

Use `httptest.Server` fixtures for Gamma market metadata and CLOB order-book responses. Assert normalized token IDs, outcomes, bid/ask, spread, and ask-side depth.

- [ ] **Step 3: Run data tests**

Run: `rtk go test ./internal/data/polymarket`

Expected: tests pass without external network calls.

### Task 12.2: Broker Integration Behind Existing Live Gate

**Files:**
- Modify: `internal/execution/polymarket/broker.go`
- Modify: `internal/execution/polymarket/broker_test.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Modify: `docs/known-issues.md`

- [ ] **Step 1: Add constructor injection**

Allow the broker to accept the official client boundary while preserving current constructor behavior for existing runtime code.

- [ ] **Step 2: Preserve dry-run/paper behavior**

Tests must show that dry-run mode never submits an external live order.

- [ ] **Step 3: Preserve live-gate behavior**

Runtime tests must show Polymarket live execution still requires `ENABLE_LIVE_TRADING`, strategy allowlist, and broker allowlist.

- [ ] **Step 4: Run tests**

Run:

```bash
rtk go test ./internal/execution/polymarket ./cmd/tradingagent -run 'Polymarket|LiveGate|LiveTrading'
```

---

## Phase 13: Advanced Research Lanes

### Task 13.1: Wallet Intelligence Research Package

**Files:**
- Create: `internal/research/walletintel/score.go`
- Create: `internal/research/walletintel/score_test.go`

- [ ] **Step 1: Add non-copy-trading wallet score**

Score wallets using finite inputs only: realized ROI, trade count, calibration proxy, recency, and category concentration.

- [ ] **Step 2: Add guardrail tests**

Tests must prove the package emits research scores only and contains no order intent or execution dependency.

### Task 13.2: Event Calibration Research Package

**Files:**
- Create: `internal/research/eventcalibration/evidence.go`
- Create: `internal/research/eventcalibration/evidence_test.go`

- [ ] **Step 1: Add evidence calibration primitive**

Represent source reliability, forecast probability, actual outcome, and Brier/log-loss contribution.

- [ ] **Step 2: Add tests**

Cover empty samples, non-finite probabilities, deterministic source ordering, and JSON-safe summaries.

### Task 13.3: Solver-Arbitrage Offline Research Package

**Files:**
- Create: `internal/research/solverarb/model.go`
- Create: `internal/research/solverarb/model_test.go`

- [ ] **Step 1: Add offline-only opportunity model**

Model complete-set costs, fee assumptions, partial-fill haircut, and net edge. Return research observations only.

- [ ] **Step 2: Add tests**

Cover negative edge, partial-fill haircut, non-finite input rejection, and no execution dependency.

### Task 13.4: Near-Resolution Latency Simulator

**Files:**
- Create: `internal/research/latency/simulator.go`
- Create: `internal/research/latency/simulator_test.go`

- [ ] **Step 1: Add tail-risk simulator**

Model stale-book probability, latency window, reversal probability, and expected loss.

- [ ] **Step 2: Add tests**

Cover high-latency rejection, stale-book penalty, non-finite input rejection, and deterministic output.

### Task 13.5: Research Report Artifacts

**Files:**
- Modify: `internal/automation/jobs_reports.go`
- Create: `internal/automation/jobs_research_reports_test.go`

- [ ] **Step 1: Add report type constants only**

Add constants:

```go
const (
    reportTypeWalletIntel      = "wallet_intelligence"
    reportTypeEventCalibration = "event_calibration"
    reportTypeSolverArb        = "solver_arbitrage"
    reportTypeLatencyResearch  = "latency_research"
)
```

- [ ] **Step 2: Add tests**

Tests assert constants are stable and no scheduled job is enabled without explicit registration.

---

## Verification and Deploy Sequence

After each phase:

```bash
rtk go test ./internal/edge ./internal/polymarketresearch ./internal/optionsresearch ./internal/calibration ./internal/regime ./internal/config ./internal/execution ./internal/api ./internal/repository/postgres ./internal/domain ./internal/automation
rtk go test ./cmd/tradingagent
cd web && rtk lint && rtk npm run build
```

Before commit:

```bash
rtk git status --short
rtk git diff --check
rtk git diff --stat
```

After commit and push, deploy with the existing production Compose stack:

```bash
docker compose --project-name augr-prod -f docker-compose.prod.yml up -d --build app
```

Verify:

```bash
curl -fsS http://127.0.0.1:8080/healthz
docker compose --project-name augr-prod -f docker-compose.prod.yml ps
docker compose --project-name augr-prod -f docker-compose.prod.yml exec -T postgres psql -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-tradingagent}" -tAc 'SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1;'
```

Expected after Phase 10 migration: schema version `44`. Expected before Phase 10 migration: schema version `43`.

## Coverage Review

- Scanner API/UI surfacing makes Phase 4 and Phase 5 research usable without order placement.
- Replay workbench closes the decision-to-outcome audit loop.
- Cross-flow risk cockpit gives operators a single exposure view across stocks, crypto, options, and Polymarket.
- Official Polymarket CLOB hardening improves adapter correctness without weakening live gates.
- Advanced research lanes remain research-only and cannot place orders.

## Execution Recommendation

Execute in this order:

1. Phase 9 scanner API/UI surfacing
2. Phase 10 replay workbench
3. Phase 11 cross-flow risk cockpit
4. Phase 12 official Polymarket CLOB hardening
5. Phase 13 advanced research lanes

Use a fresh implementation subagent per task and review each phase before committing.

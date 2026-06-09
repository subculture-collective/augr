# Augr Trading Research Platform Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Implement the safe, feasible portions of `docs/Augr Trading Research/`
as a paper-first edge-processing platform for stocks/options and Polymarket.

**Architecture:** Add shared edge, decision-journal, calibration, and regime
services before expanding strategy execution. Stocks/options and Polymarket keep
market-specific adapters, but both flows must emit a normalized candidate
decision, pass deterministic risk gates, journal the decision, and execute in
paper mode by default.

**Tech Stack:** Go 1.25, PostgreSQL 17/pgx, Redis 7, existing chi API,
existing Taskfile commands, React/Vite frontend, existing broker/data-provider
interfaces, existing report-artifact automation.

---

## Reviewed source material

Primary report files reviewed:

- `docs/Augr Trading Research/01 Synthesis/Final Combined Automated Trading Synthesis.md`
- `docs/Augr Trading Research/03 Delivery/Augr Implementation Plan.md`
- `docs/Augr Trading Research/03 Delivery/Blocking Questions and Challenges.md`
- `docs/Augr Trading Research/02 Flows/Polymarket Flow.md`
- `docs/Augr Trading Research/02 Flows/Stocks and Options Flow.md`
- `docs/Augr Trading Research/01 Synthesis/Data Pipelines.md`
- `docs/Augr Trading Research/01 Synthesis/Risk Controls and Guardrails.md`

Current codebase integration points reviewed:

- `README.md`
- `docs/development-setup.md`
- `Taskfile.yml`
- `internal/api/server.go`
- `internal/risk/engine.go`
- `internal/risk/engine_impl.go`
- `internal/execution/order_manager.go`
- `internal/execution/options_manager.go`
- `internal/execution/polymarket/broker.go`
- `internal/execution/polymarket/client.go`
- `internal/execution/polymarket/market_data.go`
- `internal/domain/order.go`
- `internal/domain/trade.go`
- `internal/domain/position.go`
- `internal/domain/options.go`
- `internal/domain/polymarket_market_data.go`
- `internal/data/options_provider.go`
- `internal/data/options_chain.go`
- `internal/service/backtest.go`
- `internal/repository/interfaces.go`
- `internal/repository/postgres/options_scan_repo.go`
- `internal/repository/postgres/report_artifact.go`
- `internal/automation/jobs_reports.go`
- `web/src/App.tsx`
- `web/src/lib/api/client.ts`
- `web/src/pages/options-page.tsx`

---

## Scope boundaries

### Implement in this plan

1. Architecture audit and safety baseline.
2. Shared EV, Kelly-cap, Brier/log-loss, and calibration primitives.
3. Persistent trade-decision journal with risk decision, evidence, and paper/live
   order references.
4. Execution boundary that records decisions before paper orders and blocks live
   execution unless backend gates pass.
5. Polymarket paper research improvements: market/book normalization, binary EV,
   spread/depth gates, paper maker-first simulation, resolution-aware journal
   fields.
6. Options paper research improvements: pricing/Greeks, realized volatility,
   IV-vs-RV scanning, defined-risk spread candidates, options risk aggregation.
7. Calibration/regime reporting and operator UI surfaces.

### Explicitly deferred

- Live options execution.
- Live Polymarket CLOB execution.
- Naked short options or undefined-risk spreads.
- Full-Kelly sizing in execution.
- LLM-placed orders or LLM live config mutation.
- Blind copy-trading.
- Near-resolution sweeper/latency strategies.
- Solver-based multi-leg Polymarket arbitrage.

---

## File structure map

New packages:

- `internal/edge/` — fair-value, EV, sizing-cap, and calibration math.
- `internal/journal/` — decision-journal service helpers.
- `internal/regime/` — simple pause/regime classification rules.
- `internal/optionsresearch/` — options scanner and spread candidate builder.
- `internal/polymarketresearch/` — binary EV scanner and paper fill simulation.

New persistence:

- `migrations/000043_trade_decision_journal.up.sql`
- `migrations/000043_trade_decision_journal.down.sql`
- `internal/domain/trade_decision.go`
- `internal/repository/postgres/trade_decision_journal.go`
- `internal/repository/postgres/trade_decision_journal_test.go`

Modified backend wiring:

- `internal/repository/interfaces.go`
- `internal/repository/postgres/schema_version.go`
- `internal/api/server.go`
- `internal/api/journal_handlers.go`
- `cmd/tradingagent/runtime.go`
- `internal/execution/order_manager.go`
- `internal/execution/options_manager.go`

Modified frontend:

- `web/src/lib/api/types.ts`
- `web/src/lib/api/client.ts`
- `web/src/App.tsx`
- `web/src/pages/options-page.tsx`
- `web/src/pages/polymarket-page.tsx`
- `web/src/pages/risk-page.tsx`
- Create: `web/src/pages/decision-journal-page.tsx`

---

## Phase 0 — Audit and safety baseline

### Task 0.1: Write the architecture audit

**Files:**
- Create: `docs/AUGR_ARCHITECTURE_AUDIT.md`

- [ ] **Step 1: Capture the answered blocker matrix**

Use this exact structure:

```markdown
# Augr Architecture Audit

## Runtime and framework

- Backend: Go 1.25, chi REST API, Cobra CLI/TUI, scheduler.
- Frontend: TypeScript, React, Vite.
- Storage: PostgreSQL 17 and Redis 7.
- Main backend bootstrap: `cmd/tradingagent`.

## Existing market/data adapters

- Stock/crypto market data: `internal/data` provider chain.
- Options data: `internal/data/options_provider.go`, `internal/data/options_chain.go`, provider files under `internal/data/{alpaca,polygon,tradier,yahoo}/`.
- Polymarket data: `internal/api/polymarket_handlers.go`, `internal/automation/jobs_polymarket_discovery.go`, `internal/repository/postgres/polymarket_*`, `internal/execution/polymarket/market_data.go`.

## Existing execution paths

- Generic orders: `internal/execution/order_manager.go`.
- Options orders: `internal/execution/options_manager.go`.
- Broker registry: `internal/execution/registry.go`.
- Broker adapters: `internal/execution/{alpaca,binance,paper,polymarket}/`.

## Existing risk controls

- `internal/risk/engine.go` exposes pre-trade checks, position limits, global kill switch, market kill switches, circuit breaker, and metrics updates.
- `internal/risk/engine_impl.go` persists API kill-switch state when a persister is wired and blocks market-specific orders through `Order.MarketType`.

## Existing journal/reporting surfaces

- Orders/trades/positions persist through repository interfaces.
- Backtest runs persist metrics/trades/equity curves.
- Report artifacts persist through `internal/repository/postgres/report_artifact.go`.
- Daily paper validation reports are generated by `internal/automation/jobs_reports.go`.

## Missing services from the trading research report

- Shared edge evaluator.
- Structured decision journal.
- Calibration store.
- Replay events tied to decision IDs.
- Regime scheduler.
- Structured risk decision output with reusable rejection reasons.
- Options pricing/Greeks as deterministic internal math.
- Polymarket CLOB/Gamma-normalized market and book model.

## Safety baseline

- New work remains paper-first.
- Live trading requires backend feature flags, strategy allowlist, market kill switch clear, global kill switch clear, and a persisted risk approval.
- Agents remain research/review-only.
```

- [ ] **Step 2: Verify conflict-marker status**

Run:

```bash
rg -n '<<<<<<<|>>>>>>>' . --glob '*.go' --glob '*.ts' --glob '*.tsx'
```

Expected: no real merge-conflict markers. Separator comments such as
`// ===================== Page =====================` are not blockers.

- [ ] **Step 3: Commit**

```bash
git add docs/AUGR_ARCHITECTURE_AUDIT.md
git commit -m "docs: audit augr trading architecture"
```

---

## Phase 1 — Shared edge and calibration primitives

### Task 1.1: Add EV and sizing math

**Files:**
- Create: `internal/edge/expected_value.go`
- Create: `internal/edge/sizing.go`
- Create: `internal/edge/expected_value_test.go`
- Create: `internal/edge/sizing_test.go`

- [ ] **Step 1: Write failing tests**

Test cases:

```go
func TestBinaryNetEV(t *testing.T) {
    got := BinaryNetEV(BinaryEVInput{Probability: 0.62, Price: 0.55, Fee: 0.005, Slippage: 0.002, ExitHaircut: 0.003})
    assert.InDelta(t, 0.060, got.NetEV, 1e-9)
    assert.InDelta(t, 0.070, got.GrossEV, 1e-9)
}

func TestOptionEdge(t *testing.T) {
    got := OptionEdge(OptionEdgeInput{ModelPrice: 2.40, ExecutablePrice: 2.10, Commission: 0.01, Slippage: 0.04, ModelHaircut: 0.05})
    assert.InDelta(t, 0.20, got.NetEdge, 1e-9)
}

func TestFractionalKellyCap(t *testing.T) {
    got := FractionalKellyCap(BinaryKellyInput{Probability: 0.60, Price: 0.50, Fraction: 0.25, Cap: 0.05})
    assert.InDelta(t, 0.05, got, 1e-9)
}
```

- [ ] **Step 2: Implement the types and functions**

```go
type BinaryEVInput struct {
    Probability float64
    Price       float64
    Fee         float64
    Slippage    float64
    ExitHaircut float64
}

type BinaryEVResult struct {
    GrossEV float64
    NetEV   float64
    Edge    float64
}

func BinaryNetEV(in BinaryEVInput) BinaryEVResult {
    gross := in.Probability*(1-in.Price) - (1-in.Probability)*in.Price
    costs := in.Fee + in.Slippage + in.ExitHaircut
    return BinaryEVResult{GrossEV: gross, NetEV: gross - costs, Edge: in.Probability - in.Price}
}

type OptionEdgeInput struct {
    ModelPrice      float64
    ExecutablePrice float64
    Commission      float64
    Slippage        float64
    ModelHaircut    float64
}

type OptionEdgeResult struct {
    GrossEdge float64
    NetEdge   float64
}

func OptionEdge(in OptionEdgeInput) OptionEdgeResult {
    gross := in.ModelPrice - in.ExecutablePrice
    return OptionEdgeResult{GrossEdge: gross, NetEdge: gross - in.Commission - in.Slippage - in.ModelHaircut}
}

type BinaryKellyInput struct {
    Probability float64
    Price       float64
    Fraction    float64
    Cap         float64
}

func FractionalKellyCap(in BinaryKellyInput) float64 {
    if in.Price <= 0 || in.Price >= 1 || in.Probability <= 0 || in.Probability >= 1 {
        return 0
    }
    b := (1 / in.Price) - 1
    q := 1 - in.Probability
    full := (in.Probability*b - q) / b
    if full <= 0 {
        return 0
    }
    sized := full * in.Fraction
    if sized > in.Cap {
        return in.Cap
    }
    return sized
}
```

- [ ] **Step 3: Run the package tests**

Run:

```bash
go test ./internal/edge
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/edge
git commit -m "feat(edge): add expected value and capped kelly math"
```

### Task 1.2: Add calibration metrics

**Files:**
- Create: `internal/edge/calibration.go`
- Create: `internal/edge/calibration_test.go`

- [ ] **Step 1: Write failing tests for Brier score, log loss, and buckets**

```go
func TestBrierScore(t *testing.T) {
    got := BrierScore([]ProbabilityOutcome{{P: 0.8, Outcome: true}, {P: 0.3, Outcome: false}})
    assert.InDelta(t, 0.065, got, 1e-9)
}

func TestCalibrationBuckets(t *testing.T) {
    buckets := BucketCalibration([]ProbabilityOutcome{
        {P: 0.12, Outcome: false},
        {P: 0.18, Outcome: true},
        {P: 0.82, Outcome: true},
    }, 10)
    assert.Equal(t, 2, buckets[1].Count)
    assert.Equal(t, 1, buckets[8].Count)
}
```

- [ ] **Step 2: Implement deterministic metrics**

```go
type ProbabilityOutcome struct {
    P       float64
    Outcome bool
}

type CalibrationBucket struct {
    Index       int     `json:"index"`
    Lower       float64 `json:"lower"`
    Upper       float64 `json:"upper"`
    Count       int     `json:"count"`
    AvgForecast float64 `json:"avg_forecast"`
    HitRate     float64 `json:"hit_rate"`
}
```

Clamp probabilities to `[1e-12, 1-1e-12]` for log loss.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/edge
```

- [ ] **Step 4: Commit**

```bash
git add internal/edge/calibration.go internal/edge/calibration_test.go
git commit -m "feat(edge): add calibration metrics"
```

---

## Phase 2 — Persistent trade-decision journal

### Task 2.1: Add domain model and migration

**Files:**
- Create: `internal/domain/trade_decision.go`
- Create: `migrations/000043_trade_decision_journal.up.sql`
- Create: `migrations/000043_trade_decision_journal.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`

- [ ] **Step 1: Create the domain model**

```go
type TradeDecisionStatus string

const (
    TradeDecisionStatusCandidate TradeDecisionStatus = "candidate"
    TradeDecisionStatusRejected  TradeDecisionStatus = "rejected"
    TradeDecisionStatusPaper     TradeDecisionStatus = "paper_ordered"
    TradeDecisionStatusLive      TradeDecisionStatus = "live_ordered"
    TradeDecisionStatusClosed    TradeDecisionStatus = "closed"
)

type RiskDecisionStatus string

const (
    RiskDecisionApproved RiskDecisionStatus = "approved"
    RiskDecisionRejected RiskDecisionStatus = "rejected"
)

type TradeDecision struct {
    ID               uuid.UUID          `json:"id"`
    StrategyID       *uuid.UUID         `json:"strategy_id,omitempty"`
    PipelineRunID    *uuid.UUID         `json:"pipeline_run_id,omitempty"`
    MarketType       MarketType         `json:"market_type"`
    InstrumentKey    string             `json:"instrument_key"`
    ExternalMarketID string             `json:"external_market_id,omitempty"`
    Side             OrderSide          `json:"side"`
    Outcome          string             `json:"outcome,omitempty"`
    FairValue        float64            `json:"fair_value"`
    ExecutablePrice  float64            `json:"executable_price"`
    Spread           float64            `json:"spread"`
    Depth            float64            `json:"depth"`
    GrossEV          float64            `json:"gross_ev"`
    NetEV            float64            `json:"net_ev"`
    KellyFraction    float64            `json:"kelly_fraction"`
    ProposedSize     float64            `json:"proposed_size"`
    ApprovedSize     float64            `json:"approved_size"`
    RiskStatus       RiskDecisionStatus `json:"risk_status"`
    RiskReasons      []string           `json:"risk_reasons"`
    Evidence         json.RawMessage    `json:"evidence,omitempty"`
    Features         json.RawMessage    `json:"features,omitempty"`
    RegimeTags       []string           `json:"regime_tags"`
    PaperOrderID     *uuid.UUID         `json:"paper_order_id,omitempty"`
    LiveOrderID      *uuid.UUID         `json:"live_order_id,omitempty"`
    Status           TradeDecisionStatus `json:"status"`
    CreatedAt        time.Time          `json:"created_at"`
    UpdatedAt        time.Time          `json:"updated_at"`
}
```

- [ ] **Step 2: Create the migration**

```sql
CREATE TABLE IF NOT EXISTS trade_decisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id UUID REFERENCES strategies(id) ON DELETE SET NULL,
    pipeline_run_id UUID,
    market_type TEXT NOT NULL,
    instrument_key TEXT NOT NULL,
    external_market_id TEXT,
    side TEXT NOT NULL,
    outcome TEXT,
    fair_value DOUBLE PRECISION NOT NULL DEFAULT 0,
    executable_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    spread DOUBLE PRECISION NOT NULL DEFAULT 0,
    depth DOUBLE PRECISION NOT NULL DEFAULT 0,
    gross_ev DOUBLE PRECISION NOT NULL DEFAULT 0,
    net_ev DOUBLE PRECISION NOT NULL DEFAULT 0,
    kelly_fraction DOUBLE PRECISION NOT NULL DEFAULT 0,
    proposed_size DOUBLE PRECISION NOT NULL DEFAULT 0,
    approved_size DOUBLE PRECISION NOT NULL DEFAULT 0,
    risk_status TEXT NOT NULL CHECK (risk_status IN ('approved', 'rejected')),
    risk_reasons TEXT[] NOT NULL DEFAULT '{}',
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    features JSONB NOT NULL DEFAULT '{}'::jsonb,
    regime_tags TEXT[] NOT NULL DEFAULT '{}',
    paper_order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
    live_order_id UUID REFERENCES orders(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('candidate', 'rejected', 'paper_ordered', 'live_ordered', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trade_decisions_strategy_created
    ON trade_decisions(strategy_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_trade_decisions_market_created
    ON trade_decisions(market_type, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_trade_decisions_status_created
    ON trade_decisions(status, created_at DESC);
```

The down migration drops indexes and `trade_decisions`.

- [ ] **Step 3: Bump schema version**

Change `internal/repository/postgres/schema_version.go`:

```go
const RequiredSchemaVersion = 43
```

- [ ] **Step 4: Commit**

```bash
git add internal/domain/trade_decision.go migrations/000043_trade_decision_journal.* internal/repository/postgres/schema_version.go
git commit -m "feat(journal): add trade decision schema"
```

### Task 2.2: Add repository and API

**Files:**
- Modify: `internal/repository/interfaces.go`
- Create: `internal/repository/postgres/trade_decision_journal.go`
- Create: `internal/repository/postgres/trade_decision_journal_test.go`
- Modify: `internal/api/server.go`
- Create: `internal/api/journal_handlers.go`
- Modify: `cmd/tradingagent/runtime.go`

- [ ] **Step 1: Add repository interface**

```go
type TradeDecisionFilter struct {
    StrategyID *uuid.UUID
    MarketType domain.MarketType
    Status     domain.TradeDecisionStatus
    CreatedAfter *time.Time
    CreatedBefore *time.Time
}

type TradeDecisionJournalRepository interface {
    Create(ctx context.Context, decision *domain.TradeDecision) error
    Get(ctx context.Context, id uuid.UUID) (*domain.TradeDecision, error)
    List(ctx context.Context, filter TradeDecisionFilter, limit, offset int) ([]domain.TradeDecision, error)
    Count(ctx context.Context, filter TradeDecisionFilter) (int, error)
    AttachPaperOrder(ctx context.Context, decisionID, orderID uuid.UUID) error
    AttachLiveOrder(ctx context.Context, decisionID, orderID uuid.UUID) error
}
```

- [ ] **Step 2: Implement Postgres repository**

Mirror the style of `internal/repository/postgres/report_artifact.go` and
`internal/repository/postgres/polymarket_discovery_run.go`. Preserve JSON and
text-array round trips.

- [ ] **Step 3: Add repository tests**

Test cases:

- `Create` stores all numeric fields, risk reasons, evidence, features, and regime tags.
- `List` filters by `strategy_id`, `market_type`, and `status`.
- `AttachPaperOrder` updates `paper_order_id`, `status`, and `updated_at`.

- [ ] **Step 4: Add API route**

In `internal/api/server.go`, add `TradeDecisions repository.TradeDecisionJournalRepository`
to `Deps` and `Server`, then add:

```go
v1.Route("/journal", func(jr chi.Router) {
    jr.Get("/decisions", s.handleListTradeDecisions)
    jr.Get("/decisions/{id}", s.handleGetTradeDecision)
})
```

- [ ] **Step 5: Wire runtime**

In `cmd/tradingagent/runtime.go`, instantiate:

```go
tradeDecisionRepo := pgrepo.NewTradeDecisionJournalRepo(db.Pool)
```

Pass it to API deps and execution deps.

- [ ] **Step 6: Run focused tests**

```bash
go test ./internal/repository/postgres -run TradeDecision
go test ./internal/api -run Journal
```

- [ ] **Step 7: Commit**

```bash
git add internal/repository/interfaces.go internal/repository/postgres/trade_decision_journal* internal/api/server.go internal/api/journal_handlers.go cmd/tradingagent/runtime.go
git commit -m "feat(journal): expose trade decisions"
```

---

## Phase 3 — Execution boundary and paper/live parity

### Task 3.1: Record decisions before generic paper orders

**Files:**
- Modify: `internal/execution/order_manager.go`
- Modify: `internal/execution/order_manager_test.go`

- [ ] **Step 1: Add a recorder seam**

```go
type DecisionRecorder interface {
    RecordPreOrderDecision(ctx context.Context, decision *domain.TradeDecision) error
    AttachPaperOrder(ctx context.Context, decisionID, orderID uuid.UUID) error
    AttachLiveOrder(ctx context.Context, decisionID, orderID uuid.UUID) error
}
```

Add `decisionRecorder DecisionRecorder` to `OrderManager` and a constructor
method:

```go
func (m *OrderManager) WithDecisionRecorder(recorder DecisionRecorder) *OrderManager {
    m.decisionRecorder = recorder
    return m
}
```

- [ ] **Step 2: Build a minimal decision from each order plan**

Before `orderRepo.Create`, construct a `domain.TradeDecision` with:

- `MarketType` from `order.MarketType`, defaulting to `domain.MarketTypeStock`.
- `InstrumentKey` from `plan.Ticker`.
- `ExecutablePrice` from `plan.EntryPrice`.
- `ProposedSize` from calculated quantity.
- `ApprovedSize` from calculated quantity only after risk approval.
- `RiskStatus` set to `rejected` with reasons when limits fail.
- `Status` set to `candidate`, `rejected`, or `paper_ordered`.

- [ ] **Step 3: Add tests**

Tests should assert:

- A rejected risk check creates one rejected decision and no submitted broker order.
- A paper order creates one decision and attaches the created order ID.
- A hold signal creates no decision and no order.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/execution -run OrderManager
```

- [ ] **Step 5: Commit**

```bash
git add internal/execution/order_manager.go internal/execution/order_manager_test.go
git commit -m "feat(execution): journal generic order decisions"
```

### Task 3.2: Enforce live-trading gates in execution managers

**Files:**
- Modify: `internal/execution/order_manager.go`
- Modify: `internal/execution/options_manager.go`
- Modify: `internal/execution/order_manager_test.go`
- Create: `internal/execution/live_gate.go`
- Create: `internal/execution/live_gate_test.go`

- [ ] **Step 1: Add explicit live gate**

```go
type LiveGateConfig struct {
    EnableLiveTrading bool
    AllowedStrategies map[uuid.UUID]bool
    AllowedBrokers    map[string]bool
}

func (c LiveGateConfig) Allows(strategyID *uuid.UUID, broker string) (bool, string) {
    if !c.EnableLiveTrading {
        return false, "live trading disabled"
    }
    if strategyID == nil || !c.AllowedStrategies[*strategyID] {
        return false, "strategy not live-allowlisted"
    }
    if !c.AllowedBrokers[strings.ToLower(strings.TrimSpace(broker))] {
        return false, "broker not live-allowlisted"
    }
    return true, ""
}
```

- [ ] **Step 2: Wire the gate**

Order managers should default to paper behavior. If a broker is configured as
live, `LiveGateConfig.Allows` must pass before `SubmitOrder` or
`SubmitOptionOrder` is called.

- [ ] **Step 3: Add tests**

Test cases:

- Live disabled blocks broker submission.
- Strategy missing from allowlist blocks broker submission.
- Broker missing from allowlist blocks broker submission.
- All gates enabled allows the broker submission path.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/execution -run 'LiveGate|OrderManager|OptionsOrderManager'
```

- [ ] **Step 5: Commit**

```bash
git add internal/execution/live_gate* internal/execution/order_manager.go internal/execution/options_manager.go internal/execution/*test.go
git commit -m "feat(execution): enforce live trading gates"
```

---

## Phase 4 — Polymarket paper research flow

### Task 4.1: Add Polymarket EV scanner

**Files:**
- Create: `internal/polymarketresearch/scanner.go`
- Create: `internal/polymarketresearch/scanner_test.go`
- Modify: `internal/domain/polymarket_market_data.go`

- [ ] **Step 1: Extend normalized book snapshot**

Add fields needed by the report while preserving existing fields:

```go
type PolymarketBookSnapshot struct {
    Slug       string
    TokenID    string
    Outcome    string
    BestBid    float64
    BestAsk    float64
    Bids       []PolymarketBookLevel
    Asks       []PolymarketBookLevel
    Spread     float64
    Midpoint   float64
    DepthUSD   float64
    ReceivedAt time.Time
    ConnID     int
}
```

- [ ] **Step 2: Create scanner input and output**

```go
type BinaryProbabilityEstimate struct {
    Slug        string
    Outcome     string
    Probability float64
    Evidence    json.RawMessage
    Features    json.RawMessage
}

type ScanConfig struct {
    MinNetEdge  float64
    MaxSpread   float64
    MinDepthUSD float64
    Fee         float64
    Slippage    float64
    KellyFraction float64
    KellyCap      float64
}

type Opportunity struct {
    Decision domain.TradeDecision
    Book     domain.PolymarketBookSnapshot
}
```

- [ ] **Step 3: Implement scanner**

`ScanBinaryOpportunity` should:

1. Use ask as executable price for buy-YES/buy-NO paper entries.
2. Call `edge.BinaryNetEV`.
3. Reject if spread exceeds `MaxSpread`.
4. Reject if depth is below `MinDepthUSD`.
5. Reject if net edge is below `MinNetEdge`.
6. Compute capped fractional Kelly.
7. Return a `domain.TradeDecision` with `MarketType=domain.MarketTypePolymarket`.

- [ ] **Step 4: Add tests**

Test cases:

- A 62% probability against 55c ask and 2c friction passes with positive net EV.
- A wide spread is rejected with `risk_reasons=["spread_too_wide"]`.
- Low depth is rejected with `risk_reasons=["depth_too_low"]`.
- Net edge below threshold is rejected with `risk_reasons=["edge_below_minimum"]`.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/polymarketresearch ./internal/edge
```

- [ ] **Step 6: Commit**

```bash
git add internal/polymarketresearch internal/domain/polymarket_market_data.go
git commit -m "feat(polymarket): add binary ev scanner"
```

### Task 4.2: Add maker-first paper fill simulator

**Files:**
- Create: `internal/polymarketresearch/paper_fill.go`
- Create: `internal/polymarketresearch/paper_fill_test.go`

- [ ] **Step 1: Define simulator contract**

```go
type PaperFillConfig struct {
    MakerFirst        bool
    PostOnly          bool
    StaleAfter        time.Duration
    QueueHaircutPct   float64
    AdverseSelectionPct float64
}

type PaperFillResult struct {
    FilledQuantity float64
    AvgPrice       float64
    Maker          bool
    Taker          bool
    FillQuality    float64
    Reason         string
}
```

- [ ] **Step 2: Implement deterministic paper fill rules**

- Post-only orders rest at bid for buy intent.
- A fill occurs only if a later snapshot crosses the resting limit after queue haircut.
- Stale orders return `Reason="stale_order_cancelled"`.
- Taker simulation is disabled unless `MakerFirst=false`.

- [ ] **Step 3: Add tests**

Tests should cover maker fill, stale cancellation, and taker-disabled rejection.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/polymarketresearch
```

- [ ] **Step 5: Commit**

```bash
git add internal/polymarketresearch/paper_fill.go internal/polymarketresearch/paper_fill_test.go
git commit -m "feat(polymarket): add maker first paper fills"
```

---

## Phase 5 — Stocks/options paper research flow

### Task 5.1: Add Black-Scholes, Greeks, and realized volatility

**Files:**
- Create: `internal/edge/options_pricing.go`
- Create: `internal/edge/options_pricing_test.go`

- [ ] **Step 1: Add tests with known sanity checks**

Test cases:

- At-the-money call and put have prices greater than zero.
- Call delta is in `(0, 1)` and put delta is in `(-1, 0)`.
- Gamma and vega are positive.
- Realized volatility of a flat series is zero.

- [ ] **Step 2: Implement pricing input/output**

```go
type BlackScholesInput struct {
    Spot       float64
    Strike     float64
    Rate       float64
    Volatility float64
    DaysToExpiry int
    OptionType domain.OptionType
}

type OptionModelResult struct {
    Price float64
    Delta float64
    Gamma float64
    Vega  float64
    Theta float64
    Rho   float64
}
```

Use standard normal CDF/PDF from `math.Erf`; no external dependency is required.

- [ ] **Step 3: Add realized volatility helper**

```go
func RealizedVolatility(closes []float64, annualization float64) float64
```

Use log returns and sample variance.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/edge -run 'BlackScholes|RealizedVolatility'
```

- [ ] **Step 5: Commit**

```bash
git add internal/edge/options_pricing.go internal/edge/options_pricing_test.go
git commit -m "feat(edge): add options pricing primitives"
```

### Task 5.2: Add defined-risk options scanner

**Files:**
- Create: `internal/optionsresearch/scanner.go`
- Create: `internal/optionsresearch/scanner_test.go`
- Modify: `internal/api/server.go`
- Create: `internal/api/options_scanner_handlers.go`
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/lib/api/client.ts`
- Modify: `web/src/pages/options-page.tsx`

- [ ] **Step 1: Define scanner config**

```go
type ScannerConfig struct {
    MinOpenInterest float64
    MinVolume       float64
    MaxSpreadPct    float64
    MinNetEdge      float64
    MaxDailyThetaPctEquity float64
    MaxSingleUnderlyingPctEquity float64
}
```

- [ ] **Step 2: Implement candidate generation**

The scanner should:

1. Filter stale/illiquid contracts.
2. Compute model price and Greeks when provider Greeks are missing or zero.
3. Compare model price to executable ask for debit candidates.
4. Build bull-call and bear-put vertical spread candidates from same-expiry legs.
5. Reject naked short legs.
6. Return `domain.TradeDecision` rows with `MarketType=domain.MarketTypeStock`, `InstrumentKey` as OCC symbol or spread key, and options features in JSON.

- [ ] **Step 3: Add API route**

In `internal/api/server.go` under `/api/v1/options`:

```go
or.Get("/opportunities/{underlying}", s.handleGetOptionsOpportunities)
```

The handler fetches the chain through `s.optionsProvider`, calls the scanner,
and returns ranked candidates. If `s.optionsProvider` is nil, return `501`.

- [ ] **Step 4: Add frontend client method**

```ts
async getOptionsOpportunities(underlying: string, params: { expiry?: string } = {}) {
  return this.request<TradeDecision[]>(`/api/v1/options/opportunities/${underlying}`, {
    query: toQueryParams(params),
  })
}
```

- [ ] **Step 5: Update Options page**

Add a card below the chain table named `Defined-risk opportunities` showing:

- instrument/spread key
- net edge
- executable price
- model fair value
- spread
- risk status
- risk reasons

- [ ] **Step 6: Run tests**

```bash
go test ./internal/optionsresearch ./internal/api -run Options
npm run test -- --run options-page
```

- [ ] **Step 7: Commit**

```bash
git add internal/optionsresearch internal/api/options_scanner_handlers.go internal/api/server.go web/src/lib/api web/src/pages/options-page.tsx
git commit -m "feat(options): add defined risk opportunity scanner"
```

---

## Phase 6 — Calibration, regime, and reporting loop

### Task 6.1: Add calibration report generator

**Files:**
- Create: `internal/calibration/report.go`
- Create: `internal/calibration/report_test.go`
- Modify: `internal/automation/jobs_reports.go`

- [ ] **Step 1: Define report shape**

```go
type StrategyCalibrationReport struct {
    StrategyID uuid.UUID `json:"strategy_id"`
    MarketType domain.MarketType `json:"market_type"`
    SampleSize int `json:"sample_size"`
    BrierScore float64 `json:"brier_score"`
    LogLoss float64 `json:"log_loss"`
    Buckets []edge.CalibrationBucket `json:"buckets"`
    PositiveEVRealizedPnL float64 `json:"positive_ev_realized_pnl"`
    RejectedReasons map[string]int `json:"rejected_reasons"`
    GeneratedAt time.Time `json:"generated_at"`
}
```

- [ ] **Step 2: Generate from trade decisions**

Use completed journal rows and their outcome fields once available. Until
outcomes are connected, produce a report with `SampleSize=0` and a clear JSON
field `"outcome_status":"awaiting_outcome_links"`.

- [ ] **Step 3: Register report type**

Add `calibration` report generation to `internal/automation/jobs_reports.go`
using the existing `report_artifacts` table and schedule after the paper
validation report.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/calibration ./internal/automation -run Calibration
```

- [ ] **Step 5: Commit**

```bash
git add internal/calibration internal/automation/jobs_reports.go
git commit -m "feat(reports): add calibration reports"
```

### Task 6.2: Add regime pause helper

**Files:**
- Create: `internal/regime/rules.go`
- Create: `internal/regime/rules_test.go`

- [ ] **Step 1: Define deterministic inputs**

```go
type Snapshot struct {
    ConsecutiveLosses int
    RollingWinRate float64
    FillRate float64
    SlippagePct float64
    VolatilityPct float64
    LiquidityUSD float64
    APILatencyMs int
    DataSourceConflict bool
}

type RuleConfig struct {
    MaxConsecutiveLosses int
    MinRollingWinRate float64
    MinFillRate float64
    MaxSlippagePct float64
    MaxVolatilityPct float64
    MinLiquidityUSD float64
    MaxAPILatencyMs int
}
```

- [ ] **Step 2: Return pause reasons**

```go
func Evaluate(snapshot Snapshot, cfg RuleConfig) []string
```

Reasons should match report language: `loss_cluster_pause`,
`win_rate_below_floor`, `fill_rate_collapse`, `slippage_above_baseline`,
`volatility_out_of_range`, `liquidity_disappeared`, `api_latency_spike`,
`data_source_conflict`.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/regime
```

- [ ] **Step 4: Commit**

```bash
git add internal/regime
git commit -m "feat(regime): add pause rule helper"
```

---

## Phase 7 — Operator UI

### Task 7.1: Add decision journal page

**Files:**
- Modify: `web/src/lib/api/types.ts`
- Modify: `web/src/lib/api/client.ts`
- Modify: `web/src/App.tsx`
- Create: `web/src/pages/decision-journal-page.tsx`
- Create: `web/src/pages/decision-journal-page.test.tsx`

- [ ] **Step 1: Add `TradeDecision` type**

Include fields from `internal/domain/trade_decision.go` using snake_case API
names.

- [ ] **Step 2: Add client methods**

```ts
async listTradeDecisions(params: { strategy_id?: string; market_type?: string; status?: string; limit?: number; offset?: number } = {}) {
  return this.requestList<TradeDecision>('/api/v1/journal/decisions', { query: toQueryParams(params) })
}

async getTradeDecision(id: string) {
  return this.request<TradeDecision>(`/api/v1/journal/decisions/${id}`)
}
```

- [ ] **Step 3: Add route**

In `web/src/App.tsx`:

```tsx
<Route path="journal" element={<DecisionJournalPage />} />
```

- [ ] **Step 4: Build page**

Show table columns:

- created time
- market type
- instrument key
- net EV
- risk status
- risk reasons
- status
- paper/live order ID

- [ ] **Step 5: Run frontend tests**

```bash
npm run test -- --run decision-journal-page
npm run lint
```

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/api web/src/App.tsx web/src/pages/decision-journal-page*
git commit -m "feat(web): add decision journal page"
```

### Task 7.2: Surface kill-switch and paper/live state prominently

**Files:**
- Modify: `web/src/pages/risk-page.tsx`
- Modify: `web/src/pages/polymarket-page.tsx`
- Modify: `web/src/pages/options-page.tsx`

- [ ] **Step 1: Add market halt badges**

Use existing `getRiskStatus` and `toggleMarketKillSwitch` client methods to
show global and per-market halt state.

- [ ] **Step 2: Add paper-first banner**

Add a banner to Polymarket and Options pages:

```text
Paper-first mode: research candidates and paper decisions are enabled. Live order placement remains disabled unless backend live gates and risk approvals pass.
```

- [ ] **Step 3: Run frontend tests**

```bash
npm run test -- --run risk-page polymarket-page options-page
npm run lint
```

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/risk-page.tsx web/src/pages/polymarket-page.tsx web/src/pages/options-page.tsx
git commit -m "feat(web): surface trading safety state"
```

---

## Verification sequence

Run this after each phase that touches Go code:

```bash
task fmt
go test ./internal/edge ./internal/polymarketresearch ./internal/optionsresearch ./internal/regime ./internal/calibration
go test ./internal/repository/postgres -run 'TradeDecision|ReportArtifact|OptionsScan|Polymarket'
go test ./internal/api -run 'Journal|Options|Polymarket|Risk'
go test ./internal/execution -run 'OrderManager|OptionsOrderManager|LiveGate'
```

Run this after frontend phases:

```bash
cd web
npm run test -- --run
npm run lint
npm run build
```

Run this before merging all phases:

```bash
task build
task test
task lint
```

If broad checks fail because of pre-existing unrelated failures, record the
failure command, failing package/file, and whether the focused checks above pass.

---

## Coverage review

Implemented by this plan:

- Shared platform services: edge evaluator, journal, calibration, regime helper,
  API/UI visibility.
- Stocks/options: pricing/Greeks/RV, IV-vs-model opportunity scanning,
  defined-risk paper candidates.
- Polymarket: binary EV, spread/depth gates, maker-first paper fill simulation,
  journaled decision rows.
- Risk: live-gate hardening, decision-level risk status, persisted rejection
  reasons, UI safety visibility.
- Agent boundary: agents remain outside execution; this plan adds deterministic
  services they can read but not use to place live orders.

Remaining report items intentionally deferred:

- Official live Polymarket CLOB order signing.
- Weather-market station mapping and calibration.
- Wallet-intelligence scoring dashboard.
- Cross-flow risk cockpit with capital-at-risk aggregation.
- Replay workbench with full order-book reconstruction.
- Cross-venue prediction-market abstraction.
- Advanced latency, solver, and near-resolution lanes.

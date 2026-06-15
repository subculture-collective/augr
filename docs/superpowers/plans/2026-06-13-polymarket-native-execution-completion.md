# Polymarket Native Execution Completion Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Finish Polymarket native execution from safe paper-native scaffold to production-ready, live-switchable prediction-market trading.

**Architecture:** Keep `cmd/tradingagent/prod_strategy_runner.go` as the Polymarket routing boundary and keep `execution.OrderManager` as the single order/trade/position persistence choke point. Improve native snapshots, side-aware validation, Polymarket sizing, exit/stop lifecycle, strategy intelligence, live-readiness gates, and reconciliation without reintroducing legacy OHLCV execution.

**Tech Stack:** Go backend, Postgres repositories, existing `internal/execution`, `internal/execution/polymarket`, `internal/polymarketdiscovery`, `internal/risk`, `cmd/tradingagent`, and current Polymarket retail/gateway clients.

---

## Current State

- Polymarket strategy runs route to `runPolymarketNative` before OHLCV loading.
- Paper trading is the default unless the strategy is explicitly set to `is_paper=false` and global live trading, strategy allowlist, and `polymarket` broker allowlist all pass.
- Discovery-created Polymarket strategies are active, scheduled, and paper.
- A deterministic native executor turns discovery metadata into buy/hold decisions.
- Order manager persists side-qualified Polymarket positions as `slug:YES` / `slug:NO`.

---

## File Structure

- Modify `internal/execution/polymarket/snapshot.go`: side-aware quote/token validation helpers.
- Modify `internal/execution/polymarket/snapshot_test.go`: YES/NO executable snapshot tests.
- Modify `cmd/tradingagent/prod_strategy_runner.go`: use canonical validation and real Polymarket notional prechecks.
- Modify `internal/execution/position_sizing.go`: add Polymarket sizing method or helper.
- Modify `cmd/tradingagent/sizing_policy.go`: select Polymarket sizing from config.
- Modify `internal/execution/order_manager.go`: side-qualified open-position matching and close/update support.
- Modify `internal/execution/order_manager_test.go`: Polymarket sizing, side identity, close lifecycle tests.
- Modify `internal/execution/polymarket/executor.go`: richer native decision actions and evaluator interface.
- Add `internal/execution/polymarket/evaluator.go`: template-aware native evaluator.
- Add `internal/execution/polymarket/evaluator_test.go`: deterministic evaluator scenarios.
- Modify `internal/polymarketdiscovery/generator.go`: produce richer execution metadata.
- Modify `internal/polymarketdiscovery/orchestrator.go`: validate native execution metadata before activating.
- Add `internal/execution/polymarket/live_readiness.go`: dry-run/live activation checklist helper.
- Add `internal/execution/polymarket/live_readiness_test.go`: live gate/readiness tests.
- Add `docs/runbooks/polymarket-live-activation.md`: operator runbook for paper-to-live switch.

---

### Task 1: Side-Aware Snapshot Validation

**Files:**
- Modify: `internal/execution/polymarket/snapshot.go`
- Test: `internal/execution/polymarket/snapshot_test.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`

- [x] **Step 1: Write failing side validation tests**

Add tests covering YES and NO quote requirements:

```go
func TestSnapshotValidateExecutableSide(t *testing.T) {
    now := time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)
    end := now.Add(48 * time.Hour)
    snap := Snapshot{
        Slug: "will-example-happen", EndDate: &end,
        BestBidYes: 0.41, BestAskYes: 0.43,
        BestBidNo: 0.56, BestAskNo: 0.58,
        Liquidity: 20_000,
    }

    if err := snap.ValidateExecutableSide("YES", 1000, now); err != nil {
        t.Fatalf("YES side rejected: %v", err)
    }
    if err := snap.ValidateExecutableSide("NO", 1000, now); err != nil {
        t.Fatalf("NO side rejected: %v", err)
    }
}

func TestSnapshotValidateExecutableSideRejectsMissingSideBook(t *testing.T) {
    now := time.Now().UTC()
    end := now.Add(48 * time.Hour)
    snap := Snapshot{Slug: "m", EndDate: &end, BestBidYes: 0.41, BestAskYes: 0.43, Liquidity: 20_000}
    if err := snap.ValidateExecutableSide("NO", 1000, now); err == nil {
        t.Fatal("expected missing NO book to be rejected")
    }
}
```

- [x] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/execution/polymarket -run TestSnapshotValidateExecutableSide -count=1
```

Expected: FAIL because `ValidateExecutableSide` does not exist.

- [x] **Step 3: Implement canonical side validation**

Add to `snapshot.go`:

```go
func (s Snapshot) ValidateExecutableSide(side string, minLiquidity float64, now time.Time) error {
    side = strings.ToUpper(strings.TrimSpace(side))
    if side != "YES" && side != "NO" {
        return fmt.Errorf("polymarket snapshot: invalid side %q", side)
    }
    if strings.TrimSpace(s.Slug) == "" {
        return errors.New("polymarket snapshot: slug is required")
    }
    if s.EndDate == nil || !s.EndDate.After(now) {
        return errors.New("polymarket snapshot: valid future end date is required")
    }
    if s.Liquidity < minLiquidity {
        return fmt.Errorf("polymarket snapshot: liquidity %.2f below minimum %.2f", s.Liquidity, minLiquidity)
    }
    bid, ask := s.BidAskForSide(side)
    if bid <= 0 || ask <= 0 || ask < bid || ask > 1 {
        return fmt.Errorf("polymarket snapshot: valid %s orderbook quote is required", side)
    }
    return nil
}

func (s Snapshot) BidAskForSide(side string) (float64, float64) {
    switch strings.ToUpper(strings.TrimSpace(side)) {
    case "YES":
        return s.BestBidYes, s.BestAskYes
    case "NO":
        return s.BestBidNo, s.BestAskNo
    default:
        return 0, 0
    }
}
```

- [x] **Step 4: Route runner through canonical validation**

In `checkPolymarketNativePreconditions`, call:

```go
if err := snapshot.ValidateExecutableSide(decision.Side, minLiquidity, time.Now().UTC()); err != nil {
    return err
}
```

Remove duplicate slug/end-date/liquidity checks from the runner once this passes.

- [x] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/execution/polymarket ./cmd/tradingagent -count=1
```

Expected: PASS.

---

### Task 2: Polymarket-Specific Sizing and Notional Caps

**Files:**
- Modify: `internal/execution/position_sizing.go`
- Modify: `cmd/tradingagent/sizing_policy.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Test: `internal/execution/position_sizing_test.go`
- Test: `internal/execution/order_manager_test.go`

- [x] **Step 1: Add failing sizing tests**

Add to `position_sizing_test.go`:

```go
func TestPolymarketPositionSizeCapsUSDCExposure(t *testing.T) {
    qty := execution.PolymarketPositionSize(execution.PolymarketSizingParams{
        AccountValue: 100_000,
        FractionPct: 0.02,
        MaxPositionUSDC: 500,
        EntryPrice: 0.25,
    })
    if qty != 2000 {
        t.Fatalf("qty = %v, want 2000", qty)
    }
}
```

- [x] **Step 2: Run test to verify failure**

Run:

```bash
go test ./internal/execution -run TestPolymarketPositionSizeCapsUSDCExposure -count=1
```

Expected: FAIL because `PolymarketPositionSize` is missing.

- [x] **Step 3: Implement sizing helper**

Add to `position_sizing.go`:

```go
type PolymarketSizingParams struct {
    AccountValue     float64
    FractionPct      float64
    MaxPositionUSDC  float64
    EntryPrice       float64
}

func PolymarketPositionSize(p PolymarketSizingParams) float64 {
    if p.AccountValue <= 0 || p.FractionPct <= 0 || p.EntryPrice <= 0 {
        return 0
    }
    notional := p.AccountValue * p.FractionPct
    if p.MaxPositionUSDC > 0 && notional > p.MaxPositionUSDC {
        notional = p.MaxPositionUSDC
    }
    return notional / p.EntryPrice
}
```

- [x] **Step 4: Enforce actual notional in runner precheck**

In `checkPolymarketNativePreconditions`, do not pass `0` as `positionUSDC`. Compute planned notional from config, entry price, and account fraction, then pass it into `risk.CheckPolymarketPreConditions`.

- [x] **Step 5: Run risk/sizing tests**

Run:

```bash
go test ./internal/execution ./internal/risk ./cmd/tradingagent -count=1
```

Expected: PASS.

---

### Task 3: Close and Exit Lifecycle

**Files:**
- Modify: `internal/execution/polymarket/executor.go`
- Modify: `internal/execution/order_manager.go`
- Test: `internal/execution/order_manager_test.go`
- Test: `internal/execution/polymarket/executor_test.go`

- [x] **Step 1: Extend native decision action**

Change `NativeDecision` to include:

```go
Action string `json:"action,omitempty"` // "enter"|"exit"|"hold"
```

- [x] **Step 2: Add failing exit tests**

Add order-manager tests:

```go
func TestProcessSignal_PolymarketExitClosesSideQualifiedPosition(t *testing.T) {
    // Seed open position ticker "will-example-happen:YES".
    // Send sell signal with plan.Side="YES".
    // Expect sell order and position closed/updated, not a new unrelated position.
}
```

- [x] **Step 3: Implement side-qualified position lookup**

Add helper in `order_manager.go`:

```go
func polymarketPositionTicker(slug, side string) string {
    side = strings.ToUpper(strings.TrimSpace(side))
    if side == "" {
        return slug
    }
    return slug + ":" + side
}
```

Use it both when creating and when locating Polymarket positions.

- [x] **Step 4: Update close behavior**

For Polymarket sell signals, locate side-qualified open positions before submitting the order. On fill, reduce quantity or mark position closed instead of always creating a new position.

- [x] **Step 5: Run lifecycle tests**

Run:

```bash
go test ./internal/execution ./internal/execution/polymarket -run 'Polymarket|Exit|Close' -count=1
```

Expected: PASS.

---

### Task 4: Wire Stop/Take-Profit Monitoring

**Files:**
- Modify: `internal/execution/polymarket/stop_guard.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`
- Test: `internal/execution/polymarket/stop_guard_test.go`

- [x] **Step 1: Define guard registration boundary**

Add an interface near runtime wiring:

```go
type polymarketStopRegistrar interface {
    RegisterPosition(ctx context.Context, position domain.Position, order domain.Order) error
    UnregisterPosition(ctx context.Context, positionID uuid.UUID) error
}
```

- [x] **Step 2: Register after filled paper/live Polymarket positions**

After `OrderManager.ProcessSignal` succeeds and positions are fetched, register positions with stop/take-profit values.

- [x] **Step 3: Rebuild guards at startup**

Startup now loads open positions from the repository and re-registers open
Polymarket side-qualified positions through the existing stop-guard path.

At runtime startup, query open Polymarket positions and register guards.

- [x] **Step 4: Add duplicate-fire test**

Add a test where two ticks cross the stop; assert only one template send happens.

- [x] **Step 5: Run stop tests**

Run:

```bash
go test ./internal/execution/polymarket ./cmd/tradingagent -run 'StopGuard|Polymarket' -count=1
```

Expected: PASS.

---

### Task 5: Native Strategy Evaluator

**Files:**
- Add: `internal/execution/polymarket/evaluator.go`
- Add: `internal/execution/polymarket/evaluator_test.go`
- Modify: `internal/execution/polymarket/executor.go`
- Modify: `internal/polymarketdiscovery/templates.go`

- [x] **Step 1: Define evaluator input/output**

Create `evaluator.go`:

```go
type EvaluationInput struct {
    Strategy domain.Strategy
    Snapshot Snapshot
    ThesisSummary string
    Template string
    Direction string
}

type Evaluator interface {
    Evaluate(ctx context.Context, input EvaluationInput) (NativeDecision, error)
}
```

- [x] **Step 2: Add template golden tests**

Add table tests for `whale_copy`, `microstructure`, `resolution_edge`, `news_catalyst`, and invalid config. Each test should assert `buy` or `hold` with an exact rationale substring.

- [x] **Step 3: Implement deterministic template gates**

Implement conservative non-LLM gates first:

```go
case "microstructure": require spread <= max and liquidity >= min
case "resolution_edge": require resolution source/criteria present
case "news_catalyst": require recent catalyst metadata when available, else hold
case "whale_copy": require wallet/account evidence when available, else hold
```

- [x] **Step 4: Keep current executor as fallback**

If evaluator returns an error, return hold. Do not silently submit orders on evaluator failure.

- [x] **Step 5: Run evaluator tests**

Run:

```bash
go test ./internal/execution/polymarket -run Evaluator -count=1
```

Expected: PASS.

---

### Task 6: Discovery Metadata Contract

**Files:**
- Modify: `internal/polymarketdiscovery/generator.go`
- Modify: `internal/polymarketdiscovery/orchestrator.go`
- Test: `internal/polymarketdiscovery/orchestrator_test.go`

- [x] **Step 1: Extend proposal schema**

Add fields to `Proposal`:

```go
MaxSpreadPct float64 `json:"max_spread_pct,omitempty"`
MinLiquidity float64 `json:"min_liquidity,omitempty"`
StopPolicy string `json:"stop_policy,omitempty"`
TargetPolicy string `json:"target_policy,omitempty"`
SourceReferences []string `json:"source_references,omitempty"`
```

- [x] **Step 2: Validate required execution metadata**

For non-skip proposals require direction, entry ceiling, watch terms, and at least one concrete source/reference term.

- [x] **Step 3: Persist richer `discovery_meta`**

Include all new fields under `discovery_meta` in `DeployStrategy`.

- [x] **Step 4: Add activation-readiness test**

Test that invalid metadata does not create an active scheduled strategy.

- [x] **Step 5: Run discovery tests**

Run:

```bash
go test ./internal/polymarketdiscovery -count=1
```

Expected: PASS.

---

### Task 7: Live Readiness and Operator Runbook

**Files:**
- Add: `internal/execution/polymarket/live_readiness.go`
- Add: `internal/execution/polymarket/live_readiness_test.go`
- Add: `docs/runbooks/polymarket-live-activation.md`

- [x] **Step 1: Add readiness checklist helper**

Create:

```go
type LiveReadinessInput struct {
    EnableLiveTrading bool
    StrategyAllowlisted bool
    BrokerAllowlisted bool
    HasCredentials bool
    PaperBurnInDays int
    ValidationFailures int
}

func CheckLiveReadiness(in LiveReadinessInput) error { /* return explicit first failure */ }
```

- [x] **Step 2: Add denial tests**

Test denied by default, denied without credentials, denied without allowlists, and allowed only when all fields pass.

- [x] **Step 3: Write operator runbook**

Document required env:

```text
ENABLE_LIVE_TRADING=true
LIVE_TRADING_ALLOWED_STRATEGIES=<strategy uuid>
LIVE_TRADING_ALLOWED_BROKERS=polymarket
POLYMARKET_KEY_ID=<key>
POLYMARKET_SECRET_KEY=<secret>
```

Also document paper burn-in acceptance criteria and rollback to paper, including setting the strategy back to `is_paper=true`.

- [x] **Step 4: Run tests**

Run:

```bash
go test ./internal/execution/polymarket -run LiveReadiness -count=1
```

Expected: PASS.

---

### Task 8: Reconciliation and Observability

**Files:**
- Add: `internal/execution/polymarket/reconciler.go`
- Add: `internal/execution/polymarket/reconciler_test.go`
- Modify: runtime metrics wiring where Polymarket execution metrics are registered.

- [x] **Step 1: Define reconciliation input**

Create reconciler that compares broker positions to local open positions by side-qualified instrument key.

- [x] **Step 2: Emit audit events for drift**

When external and local positions disagree, create audit/event records with `event_type=polymarket_position_drift`.

- [x] **Step 3: Add metrics**

Track reconciliation drift count, stop-guard trigger/send-error counts,
tick-to-fire latency, and active guard count.

- [x] **Step 4: Add idempotency test**

Run reconciliation twice with the same state and assert it does not duplicate corrective events.

- [x] **Step 5: Run reconciliation tests**

Run:

```bash
rtk go test ./internal/execution/polymarket -run 'Reconcile|Reconciler|Broker' -count=1
```

Expected: PASS.

Audit-first reconciler implemented and reviewed; runtime scheduling and metrics wiring are now active.

---

## Suggested Execution Order

1. Task 1 — side/book validation.
2. Task 2 — sizing and notional caps.
3. Task 3 — close/exit lifecycle.
4. Task 4 — stop/take-profit monitoring.
5. Task 7 — live readiness docs and checks.
6. Task 5 — richer strategy evaluator.
7. Task 6 — richer discovery metadata contract.
8. Task 8 — reconciliation and observability.

This order prioritizes preventing wrong-side/oversized trades before improving alpha.

---

## Validation Command Set

Run after each task:

```bash
go test ./internal/execution/... ./internal/risk/... ./internal/polymarketdiscovery/... -count=1
```

Run before merge:

```bash
go test ./internal/execution ./internal/execution/polymarket ./internal/polymarketdiscovery ./cmd/tradingagent ./internal/scheduler ./internal/repository/postgres -count=1
```

Required invariants:

- Polymarket never enters legacy OHLCV execution.
- Paper remains default.
- Live requires `is_paper=false`, global live flag, strategy allowlist, broker allowlist, and credentials; burn-in/readiness checks are manual unless wired into runtime.
- Invalid side/book/liquidity/spread/resolution fails before order creation.
- YES and NO positions cannot collide.
- Exit orders map to correct Polymarket intents.
- Stop guard cannot double-fire.
- Reconciliation is idempotent.

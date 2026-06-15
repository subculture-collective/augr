# Polymarket Native Execution Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Stop routing Polymarket strategies through the stock OHLCV runner and replace that path with prediction-market-native snapshots, validators, templates, and execution.

**Architecture:** Stock strategies keep the existing OHLCV scheduler/runner. Polymarket strategies are quarantined from scheduled OHLCV execution until a native executor can consume YES/NO price, spread, liquidity, volume, whale/account activity, and resolution metadata. Price candles remain optional derived data for charts/backtests, not live execution preconditions.

**Tech Stack:** Go backend, Postgres migrations, existing `internal/scheduler`, `internal/execution/polymarket`, `internal/polymarketdiscovery`, and `internal/agent` packages.

---

## File Structure

- Modify `internal/scheduler/scheduler.go`: add a scheduler guard that skips `MarketTypePolymarket` strategies until a native executor is wired.
- Modify `internal/scheduler/scheduler_test.go`: add regression coverage that scheduled Polymarket strategies do not call the OHLCV executor/pipeline.
- Modify `internal/polymarketdiscovery/orchestrator.go`: create new discovered Polymarket strategies in paused/quarantined form instead of active scheduled form.
- Add `migrations/000048_pause_polymarket_ohlcv_strategies.up.sql`: pause existing active scheduled Polymarket paper strategies and clear their cron so they stop producing OHLCV failures immediately.
- Add `migrations/000048_pause_polymarket_ohlcv_strategies.down.sql`: reversible no-op/status restore helper for migration rollback.
- Modify `internal/repository/postgres/schema_version.go`: bump required schema to `48`.
- Later tasks will add `internal/execution/polymarket/snapshot.go`, snapshot validation tests, native strategy templates, and native executor wiring.

**Current status:** Tasks 1-5 are implemented as a native execution scaffold.
Polymarket runs route before legacy OHLCV loading, fetch native market snapshots,
build deterministic YES/NO decisions from discovery metadata, and execute through
the existing order manager. Paper trading is the default; live Polymarket only
engages when global live trading is enabled and the strategy plus `polymarket`
broker are explicitly allowlisted.

---

### Task 1: Stop Scheduled Polymarket OHLCV Execution

**Files:**
- Modify: `internal/scheduler/scheduler.go:487-542`
- Test: `internal/scheduler/scheduler_test.go`

- [x] **Step 1: Write the failing scheduler test**

Add this test near `TestRunStrategy_ActiveRunsNormally`:

```go
func TestRunStrategy_PolymarketSkipsLegacyOHLCVExecution(t *testing.T) {
    strategyID := uuid.New()
    repo := &mockStrategyRepo{strategies: []domain.Strategy{{
        ID:           strategyID,
        Ticker:       "will-example-happen",
        MarketType:   domain.MarketTypePolymarket,
        ScheduleCron: testScheduleSpec,
        Status:       domain.StrategyStatusActive,
    }}}
    pipeline := &mockPipeline{}
    executor := &mockStrategyExecutor{}
    s := NewScheduler(repo, pipeline, &mockRiskEngine{}, testLogger(), WithStrategyExecution(executor.execute))
    s.ctx = context.Background()

    s.runStrategy(repo.strategies[0])

    if got := executor.callCount(); got != 0 {
        t.Fatalf("strategy executor calls = %d, want 0 for polymarket legacy skip", got)
    }
    if got := pipeline.callCount(); got != 0 {
        t.Fatalf("pipeline calls = %d, want 0 for polymarket legacy skip", got)
    }
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scheduler -run TestRunStrategy_PolymarketSkipsLegacyOHLCVExecution -count=1`

Expected before implementation: FAIL because executor is called once.

- [x] **Step 3: Implement the minimal scheduler guard**

Add this after `SkipNextRun` handling and before kill-switch/market-open checks:

```go
if current.MarketType == domain.MarketTypePolymarket {
    s.logger.Info("scheduler: skipping polymarket strategy until native executor is enabled",
        slog.String("strategy_id", current.ID.String()),
        slog.String("ticker", current.Ticker),
        slog.String("market_type", current.MarketType.String()),
    )
    return
}
```

- [x] **Step 4: Run scheduler tests**

Run: `go test ./internal/scheduler -count=1`

Expected: PASS.

---

### Task 2: Quarantine Existing and Newly Discovered Polymarket Strategies

**Files:**
- Modify: `internal/polymarketdiscovery/orchestrator.go:220-229`
- Create: `migrations/000048_pause_polymarket_ohlcv_strategies.up.sql`
- Create: `migrations/000048_pause_polymarket_ohlcv_strategies.down.sql`
- Modify: `internal/repository/postgres/schema_version.go:12`

- [x] **Step 1: Change discovery deployment defaults**

Set discovered Polymarket paper strategies to paused and unscheduled:

```go
Status:       domain.StrategyStatusPaused,
ScheduleCron: "",
```

- [x] **Step 2: Add migration to pause existing active scheduled Polymarket paper strategies**

```sql
UPDATE strategies
SET status = 'paused',
    schedule_cron = '',
    updated_at = NOW()
WHERE market_type = 'polymarket'
  AND is_paper = TRUE
  AND status = 'active'
  AND COALESCE(schedule_cron, '') <> '';
```

- [x] **Step 3: Bump schema version**

Change `RequiredSchemaVersion` from `47` to `48`.

- [x] **Step 4: Verify migration and package tests**

Run: `go test ./internal/polymarketdiscovery ./internal/scheduler ./internal/repository/postgres -count=1`

Expected: PASS.

---

### Task 3: Add Polymarket Native Snapshot Model

**Files:**
- Create: `internal/execution/polymarket/snapshot.go`
- Test: `internal/execution/polymarket/snapshot_test.go`
- Modify: `internal/execution/polymarket/market_data.go`

- [x] **Step 1: Define snapshot types**

```go
type Snapshot struct {
    Slug string
    Question string
    Description string
    ResolutionCriteria string
    ResolutionSource string
    EndDate *time.Time
    ConditionID string
    YesTokenID string
    NoTokenID string
    YesPrice float64
    NoPrice float64
    BestBidYes float64
    BestAskYes float64
    SpreadYes float64
    Liquidity float64
    Volume24h float64
    OpenInterest float64
    FetchedAt time.Time
}
```

- [x] **Step 2: Add validation**

```go
func (s Snapshot) ValidateActivation(minLiquidity float64, now time.Time) error {
    switch {
    case strings.TrimSpace(s.Slug) == "":
        return errors.New("polymarket snapshot: slug is required")
    case strings.TrimSpace(s.YesTokenID) == "":
        return errors.New("polymarket snapshot: yes token id is required")
    case strings.TrimSpace(s.NoTokenID) == "":
        return errors.New("polymarket snapshot: no token id is required")
    case s.EndDate == nil || !s.EndDate.After(now):
        return errors.New("polymarket snapshot: valid future end date is required")
    case s.Liquidity < minLiquidity:
        return fmt.Errorf("polymarket snapshot: liquidity %.2f below minimum %.2f", s.Liquidity, minLiquidity)
    case s.BestBidYes <= 0 || s.BestAskYes <= 0 || s.BestAskYes < s.BestBidYes:
        return errors.New("polymarket snapshot: valid YES orderbook quote is required")
    }
    return nil
}
```

---

### Task 4: Replace Stock-Like Polymarket Templates

**Files:**
- Modify: `internal/polymarketdiscovery/templates.go`
- Modify: `internal/polymarketdiscovery/generator.go`
- Test: `internal/polymarketdiscovery/orchestrator_test.go`

- [x] **Step 1: Remove stock-ish template assumptions**

Replace descriptions mentioning RSI, VWAP, sigma, z-score, momentum, or mean reversion with native templates:

```go
TemplateWhaleCopy: "Follow high-performing tracked accounts buying YES/NO when wallet win rate, recency, liquidity, and spread gates pass.",
TemplateMicrostructure: "Enter when YES/NO spread, orderbook depth, and liquidity make the proposed edge executable with bounded slippage.",
TemplateResolutionEdge: "Trade when literal resolution criteria and reliable source metadata create a fair-value edge versus market price.",
```

- [x] **Step 2: Reject OHLCV language before activation**

Add a validation helper that rejects proposal text containing `rsi`, `macd`, `sma`, `ema`, `bollinger`, `atr`, `ohlcv`, `candles`, `vwap`, `z-score`, or `mean reversion`.

---

### Task 5: Native Executor Wiring

**Files:**
- Create: `internal/execution/polymarket/executor.go`
- Modify: `cmd/tradingagent/runtime.go`
- Modify: `cmd/tradingagent/prod_strategy_runner.go`

- [x] **Step 1: Add a Polymarket executor interface**

```go
type NativeExecutor interface {
    Execute(ctx context.Context, strategy domain.Strategy, snapshot Snapshot) (NativeDecision, error)
}
```

- [x] **Step 2: Route by market type**

In the production strategy runner, branch before OHLCV load:

```go
if strategy.MarketType == domain.MarketTypePolymarket {
    return r.runPolymarketNative(ctx, strategy)
}
```

- [x] **Step 3: Only remove scheduler guard after native executor tests pass**

The legacy scheduler skip remains only when no strategy-execution callback is
wired. Production scheduler callbacks route Polymarket through native execution;
fallback legacy pipeline execution is still blocked.

- [x] **Step 4: Paper trading active by default**

Discovery-created Polymarket strategies are active, scheduled, and paper by
default. Runtime also forces Polymarket to paper unless live trading is globally
enabled and the specific strategy ID plus `polymarket` broker are allowlisted.

---

## Initial Execution Decision

Implement Tasks 1 and 2 now. They stop the repeated OHLCV failures while preserving Polymarket discovery, profile, resolution, watched-market, and account-observation jobs for the observe-and-tune loop.

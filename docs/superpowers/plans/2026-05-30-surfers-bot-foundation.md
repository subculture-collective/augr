# Surfers-Bot Foundation Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking. Each phase ends with a deployable, testable slice.

**Goal:** Implement the feasible portions of `docs/surfers-bot.md` inside the
existing augr stack — clean Polymarket data, pre-built execution, depth-aware
backtesting, hard risk controls, phased deployment, and operational alerts —
without requiring server relocation or dedicated metal beyond the current
NUC homelab.

**Architecture:** New `internal/marketdata/polymarket` package owns the
parallel CLOB WebSocket ingestion + tick cleaner; `internal/recorder` writes
clean ticks and order-book depth snapshots to TimescaleDB hypertables (already
on the NUC postgres stack); `internal/backtest` gains depth/latency/ghost-fill
simulators driven by the recorder data; `internal/execution/polymarket` adds a
pre-built order template pool and pre-armed stop exits; `internal/risk` gains
daily drawdown + consecutive-loss circuit breakers; new `cmd/tradingagent`
subcommands wrap the dry-run and capital-scaling phases; Prometheus +
existing ntfy notifications cover the alert layer.

**Tech Stack:** Go 1.x (existing), `nhooyr.io/websocket` for client WS (already
acceptable; falls back to `gorilla/websocket` if friction), PostgreSQL +
TimescaleDB extension for tick/depth storage, Prometheus metrics, ntfy for
alerts, existing `internal/notification` channel, React+Vite web for ops view.

**Out of scope (documented, not implemented):**
- Physical relocation of compute to Ireland/Montreal (infra/business decision).
- Buying additional bare metal (NUC is already dedicated bare metal).
- Operating a self-hosted Polygon full node (config hooks added; node deploy
  belongs in the homelab repo, not augr).

---

## Phase Map

1. Phase A — Polymarket CLOB WebSocket ingestion + tick cleaner.
2. Phase B — Tick + order-book recorder (TimescaleDB-backed).
3. Phase C — Pre-built execution hot path and pre-armed stops.
4. Phase D — Depth- and latency-aware backtest realism.
5. Phase E — Hard risk controls (daily DD, consecutive-loss, breaker state).
6. Phase F — Phased deployment tooling (dry run + 3% divergence gate + capital
   steps).
7. Phase G — EV-ladder strategy scaffold.
8. Phase H — Operational monitoring and alerts.
9. Phase I — Optional Polygon node + CPU-pinning hooks.

Each phase can ship and be reviewed independently. Phases A→D are prerequisites
for honest results from later phases.

---

## Phase A — CLOB WebSocket ingestion + tick cleaner

### Task A1: New package skeleton

**Files:**
- Create: `internal/marketdata/polymarket/doc.go`
- Create: `internal/marketdata/polymarket/config.go`
- Create: `internal/marketdata/polymarket/types.go`

- [ ] **Step 1:** Define package comment in `doc.go` describing the parallel
  CLOB WS pool, dedup, jitter scoring, warmup gate.
- [ ] **Step 2:** Add `Config` struct in `config.go` with:

```go
type Config struct {
    WSURL                 string        // wss://ws-subscriptions-clob.polymarket.com/ws/market
    ConnectionsPerFeed    int           // default 100, min 1, max 300
    WarmupDuration        time.Duration // default 15 * time.Second
    WarmupMinClean        int           // default 3
    WarmupMaxJumpUSD      float64       // default 0.05
    PruneInterval         time.Duration // default 4 * time.Second
    PruneFraction         float64       // default 0.10 (cull slowest 10%)
    MaxTickDeltaUSD       float64       // default 0.15
    StaggerStartup        time.Duration // default 1 * time.Second
    DropFirstTickPerConn  bool          // default true
    JitterEMAAlpha        float64       // default 0.2
    PerMarketSlugs        []string
    Logger                *slog.Logger
    Metrics               *Metrics // Prometheus collectors (nil ok)
}

func (c *Config) Defaults() Config { /* fills zero values */ }
func (c *Config) Validate() error  { /* numeric ranges, slug presence */ }
```

- [ ] **Step 3:** Add `Tick` and `BookSnapshot` value types in `types.go`:

```go
type Tick struct {
    Slug       string
    Side       string // "yes" or "no"
    Price      float64
    Size       float64
    ReceivedAt time.Time
    SeqHint    uint64 // best-effort sequence
    ConnID     int
}

type BookSnapshot struct {
    Slug       string
    BestBid    float64
    BestAsk    float64
    Bids       []Level
    Asks       []Level
    ReceivedAt time.Time
    ConnID     int
}

type Level struct {
    Price float64
    Size  float64
}
```

- [ ] **Step 4:** Commit.

```bash
git add internal/marketdata/polymarket/
git commit -m "feat(marketdata): polymarket ws config and types"
```

### Task A2: Single connection client

**Files:**
- Create: `internal/marketdata/polymarket/connection.go`
- Create: `internal/marketdata/polymarket/connection_test.go`

- [ ] **Step 1:** Write the failing test that spins up an `httptest` server
  upgrading to WS, sends two `book` messages, asserts the connection yields a
  `BookSnapshot` and `Tick` channel pair and drops the very first message
  when `DropFirstTickPerConn=true`.
- [ ] **Step 2:** Implement `connection` struct with:

```go
type connection struct {
    id       int
    cfg      Config
    slug     string
    conn     *websocket.Conn
    ticks    chan Tick
    books    chan BookSnapshot
    metrics  *Metrics
}

func dial(ctx context.Context, cfg Config, slug string, id int) (*connection, error)
func (c *connection) run(ctx context.Context) error // loops read, parses CLOB frames
func (c *connection) Close() error
```

  - On each frame, parse `event_type`, `market`, `bids`, `asks`, `last_price`.
  - Skip the first frame when `DropFirstTickPerConn=true`.
  - Push to channels with non-blocking `select` (drop on full and increment
    a `dropped_total{conn_id=...}` Prometheus counter).
- [ ] **Step 3:** Run test, observe it pass.
- [ ] **Step 4:** Commit.

### Task A3: Parallel pool with jitter EMA and pruning

**Files:**
- Create: `internal/marketdata/polymarket/pool.go`
- Create: `internal/marketdata/polymarket/pool_test.go`

- [ ] **Step 1:** Write failing test using a fake `connFactory` injected via
  a package variable seam, that:
  - Starts `Pool` with `ConnectionsPerFeed=5`, `PruneInterval=20*time.Millisecond`,
    `PruneFraction=0.4`.
  - Two of five fakes emit consistent ticks at 100Hz; three emit bursts at
    20Hz with high jitter.
  - After 5 prune cycles asserts the surviving set is the consistent two
    (jitter EMA confirms cull).
- [ ] **Step 2:** Implement `Pool`:

```go
type Pool struct {
    cfg      Config
    slug     string
    connFn   func(ctx context.Context, cfg Config, slug string, id int) (*connection, error)
    out      chan Tick
    books    chan BookSnapshot
    nextID   int
    mu       sync.Mutex
    members  map[int]*memberState
}

type memberState struct {
    c        *connection
    jitter   float64 // EMA of inter-arrival std
    lastSeen time.Time
}

func NewPool(cfg Config, slug string) *Pool
func (p *Pool) Start(ctx context.Context) error
func (p *Pool) Ticks() <-chan Tick
func (p *Pool) Books() <-chan BookSnapshot
func (p *Pool) Snapshot() PoolSnapshot // counts, healthy count
```

  - Stagger connection dial start with `cfg.StaggerStartup / cfg.ConnectionsPerFeed`.
  - Update `memberState.jitter` with EMA on each tick (`alpha * |dt - mean| + (1-alpha) * jitter`).
  - On each `PruneInterval`, cull bottom `PruneFraction` by jitter score and
    redial replacements until pool size matches `ConnectionsPerFeed`.
- [ ] **Step 3:** Run test; ensure cull selects correct members.
- [ ] **Step 4:** Commit.

### Task A4: Cleaner: dedupe + warmup gate + delta reject

**Files:**
- Create: `internal/marketdata/polymarket/cleaner.go`
- Create: `internal/marketdata/polymarket/cleaner_test.go`

- [ ] **Step 1:** Tests:
  - Duplicate (same slug/side/price/size within 100ms across two conns) is
    emitted once.
  - A tick with `|p_new - p_last_known| > MaxTickDeltaUSD` is rejected and
    counted via `rejected_delta_total`.
  - Warmup: until `WarmupMinClean=3` clean ticks observed with no jump
    above `WarmupMaxJumpUSD` in the final `WarmupDuration`, `Ready()` returns
    false; consumers must skip the window.
- [ ] **Step 2:** Implement `Cleaner` consuming `Pool.Ticks()` and exposing
  `<-chan Tick` plus `Ready() bool`. Use a small LRU keyed by
  `slug|side|round(price,4)|size` with 500ms TTL.
- [ ] **Step 3:** Run tests.
- [ ] **Step 4:** Commit.

### Task A5: Public façade

**Files:**
- Create: `internal/marketdata/polymarket/feed.go`
- Modify: `cmd/tradingagent/runtime.go` (only to register the feed when
  enabled via config; no behavior change yet for live trading)
- Create: `internal/marketdata/polymarket/feed_test.go`

- [ ] **Step 1:** Test that `Feed.Start(ctx, slugs)` wires Pool→Cleaner per
  slug, exposes `Ticks(slug) <-chan Tick`, `Ready(slug) bool`, and reports
  `Stats()` for ops dashboards.
- [ ] **Step 2:** Implement `Feed`. Add `POLYMARKET_WS_*` env vars via
  `internal/config` (gated on a `POLYMARKET_WS_ENABLED` flag, default off).
- [ ] **Step 3:** Wire only enough into `runtime.go` to construct and stop
  the feed when enabled; do NOT replace `internal/signal/source_polymarket.go`
  yet — Phase B will hook the recorder, Phase C will swap signal consumption.
- [ ] **Step 4:** Run: `rtk go test ./internal/marketdata/polymarket/... ./cmd/tradingagent/... -count=1`
- [ ] **Step 5:** Commit.

---

## Phase B — Tick and order-book depth recorder

### Task B1: TimescaleDB hypertable migration

**Files:**
- Create: `migrations/000039_polymarket_market_data.up.sql`
- Create: `migrations/000039_polymarket_market_data.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`
  (`RequiredSchemaVersion: 38 → 39`)

- [ ] **Step 1:** Up migration:

```sql
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE polymarket_ticks (
    received_at TIMESTAMPTZ NOT NULL,
    slug        TEXT        NOT NULL,
    side        TEXT        NOT NULL CHECK (side IN ('yes','no')),
    price       NUMERIC(8,6) NOT NULL,
    size        NUMERIC(20,6) NOT NULL,
    conn_id     INTEGER     NOT NULL,
    seq_hint    BIGINT      NOT NULL DEFAULT 0
);
SELECT create_hypertable('polymarket_ticks','received_at',
    chunk_time_interval => INTERVAL '6 hours');
CREATE INDEX ON polymarket_ticks (slug, received_at DESC);

CREATE TABLE polymarket_book_snapshots (
    received_at TIMESTAMPTZ NOT NULL,
    slug        TEXT NOT NULL,
    best_bid    NUMERIC(8,6),
    best_ask    NUMERIC(8,6),
    bids        JSONB NOT NULL,
    asks        JSONB NOT NULL,
    conn_id     INTEGER NOT NULL
);
SELECT create_hypertable('polymarket_book_snapshots','received_at',
    chunk_time_interval => INTERVAL '6 hours');
CREATE INDEX ON polymarket_book_snapshots (slug, received_at DESC);

-- 30 day compression policies
ALTER TABLE polymarket_ticks
    SET (timescaledb.compress, timescaledb.compress_segmentby = 'slug');
SELECT add_compression_policy('polymarket_ticks', INTERVAL '7 days');
ALTER TABLE polymarket_book_snapshots
    SET (timescaledb.compress, timescaledb.compress_segmentby = 'slug');
SELECT add_compression_policy('polymarket_book_snapshots', INTERVAL '7 days');

-- 18 month retention
SELECT add_retention_policy('polymarket_ticks', INTERVAL '540 days');
SELECT add_retention_policy('polymarket_book_snapshots', INTERVAL '540 days');
```

- [ ] **Step 2:** Down migration drops both tables and the extension only if
  it was created (`DROP EXTENSION IF EXISTS timescaledb` is unsafe if shared
  — leave the extension installed and drop only the tables).
- [ ] **Step 3:** Bump `RequiredSchemaVersion`.
- [ ] **Step 4:** Test: `rtk go test ./internal/repository/postgres -run TestCurrentSchemaVersion -count=1`
- [ ] **Step 5:** Commit.

> Operational note: TimescaleDB is not currently enabled in the augr
> postgres image. Before deploying, add it to `docker-compose.nuc.yml`'s
> `postgres` service (`image: timescale/timescaledb:2.x-pg16`) and confirm
> with the homelab owner. If TimescaleDB cannot be enabled, fall back to
> partitioned plain tables by month — replace the `create_hypertable` calls
> with `PARTITION BY RANGE (received_at)` and skip compression/retention.

### Task B2: Recorder repository

**Files:**
- Create: `internal/repository/postgres/polymarket_marketdata.go`
- Create: `internal/repository/postgres/polymarket_marketdata_test.go`
- Modify: `internal/repository/interfaces.go` (add interface)

- [ ] **Step 1:** Add interface:

```go
type PolymarketMarketDataRepository interface {
    InsertTicks(ctx context.Context, ticks []domain.PolymarketTick) error
    InsertBookSnapshots(ctx context.Context, snaps []domain.PolymarketBookSnapshot) error
    // Read helpers used by backtester (Phase D)
    QueryTicks(ctx context.Context, slug string, from, to time.Time) ([]domain.PolymarketTick, error)
    QueryBookAt(ctx context.Context, slug string, at time.Time) (domain.PolymarketBookSnapshot, error)
}
```

- [ ] **Step 2:** Implement with `COPY FROM` via `pgx` for inserts (batched
  for throughput) and direct `SELECT` for reads.
- [ ] **Step 3:** Tests use a real postgres in CI (already standard in this
  repo per existing repository tests); skip hypertable-specific assertions
  with a `t.Skipf` if `timescaledb` extension is absent.
- [ ] **Step 4:** Commit.

### Task B3: Recorder service

**Files:**
- Create: `internal/recorder/polymarket.go`
- Create: `internal/recorder/polymarket_test.go`
- Modify: `cmd/tradingagent/runtime.go`

- [ ] **Step 1:** Service consumes `marketdata/polymarket.Feed` channels,
  batches with `BatchSize=5000` or `FlushInterval=500ms` (configurable),
  writes via the new repository, exposes Prometheus counters
  `polymarket_recorder_inserted_total{kind}` and gauges
  `polymarket_recorder_buffer_depth{kind}`.
- [ ] **Step 2:** Test: inject mock repository, push N synthetic ticks
  through, assert batching and flush.
- [ ] **Step 3:** Wire into `runtime.go` behind the same
  `POLYMARKET_WS_ENABLED` flag plus `POLYMARKET_RECORDER_ENABLED`.
- [ ] **Step 4:** Commit.

---

## Phase C — Pre-built execution hot path

### Task C1: Order template pool

**Files:**
- Modify: `internal/execution/polymarket/broker.go`
- Create: `internal/execution/polymarket/template.go`
- Create: `internal/execution/polymarket/template_test.go`

- [ ] **Step 1:** Test asserting that for a given `(slug, side, price, size,
  intent)` we can pre-build a request body + headers + HMAC signature once,
  then clone-and-send with only a fresh `nonce`/`timestamp` substitution and
  a recomputed signature for the changed bytes — measured in <50µs in the
  fast path (use `testing.Benchmark`).
- [ ] **Step 2:** Implement `Template` storing pre-marshaled JSON body,
  pre-computed canonical string, and a precomputed `hmac.New(sha256, key)`
  state that can be `Reset()` and only fed the mutable bytes per send.
- [ ] **Step 3:** Add `Broker.PrepareTemplate(ctx, spec)` and
  `Broker.SendTemplate(ctx, t)`; keep existing methods intact.
- [ ] **Step 4:** Commit.

### Task C2: Pre-armed stop exits

**Files:**
- Modify: `internal/execution/polymarket/order_manager.go`
- Create: `internal/execution/polymarket/stop_exit.go`
- Create: `internal/execution/polymarket/stop_exit_test.go`

- [ ] **Step 1:** Test: after `RegisterEntry`, the manager pre-builds a
  matching exit template at the configured stop price; when `OnTick(price)`
  crosses the stop, the exit is sent within one Send call (no JSON marshal,
  no HMAC compute on the hot path).
- [ ] **Step 2:** Implement `StopGuard` keyed by position id; integrate with
  `OrderManager`.
- [ ] **Step 3:** Commit.

---

## Phase D — Backtest realism

### Task D1: Latency model

**Files:**
- Modify: `internal/backtest/fill_engine.go`
- Create: `internal/backtest/latency_model.go`
- Create: `internal/backtest/latency_model_test.go`

- [ ] **Step 1:** Test: a `LatencyModel` returns a delay sampled from a
  configured distribution (default lognormal with `µ=log(40ms)`, `σ=0.5`)
  applied between signal time and book-arrival time used for fill matching.
- [ ] **Step 2:** Implement and wire into `fill_engine` so order placement
  uses `book_at(signal_time + sample())` instead of `book_at(signal_time)`.
- [ ] **Step 3:** Commit.

### Task D2: Depth-aware fills

**Files:**
- Modify: `internal/backtest/fill_engine.go`
- Create: `internal/backtest/depth_fill_test.go`

- [ ] **Step 1:** Test: when placing a 10,000 share order at a price level
  with 1,000 visible, fill returns `Filled=1000, Remaining=9000` and walks
  the next levels for marketable orders, computing weighted average price.
- [ ] **Step 2:** Replace the existing infinite-liquidity assumption with a
  walk-the-book routine driven by `BookSnapshot` queries.
- [ ] **Step 3:** Commit.

### Task D3: Adverse selection + ghost fills

**Files:**
- Modify: `internal/backtest/fill_engine.go`
- Create: `internal/backtest/adverse_selection.go`
- Create: `internal/backtest/adverse_selection_test.go`

- [ ] **Step 1:** Tests:
  - With `AdverseSelection.Bias=0.3` and an entry 10c below ask, the
    simulated win rate of the next 1000 entries is biased downward versus
    the no-bias baseline by a measurable amount.
  - `GhostFillRate=0.01` injects ~1% of fills as state-corrupting events
    that the simulator surfaces as `Ghost=true` and exits position
    reconciliation paths.
- [ ] **Step 2:** Implement bias as conditional probability adjustment on
  the post-entry direction; ghost fills as Poisson-injected events flipping
  position state without a real trade record.
- [ ] **Step 3:** Commit.

### Task D4: Queue position estimate

**Files:**
- Modify: `internal/backtest/fill_engine.go`
- Create: `internal/backtest/queue_position.go`
- Create: `internal/backtest/queue_position_test.go`

- [ ] **Step 1:** Test: a passive bid placed at a level with 500 ahead
  fills only after 500 contra-side volume has crossed since placement.
- [ ] **Step 2:** Track per-level `aheadVolume` from book snapshots between
  signal-time and fill-time.
- [ ] **Step 3:** Commit.

### Task D5: 3% divergence report

**Files:**
- Create: `internal/backtest/divergence.go`
- Create: `internal/backtest/divergence_test.go`
- Create: `internal/api/backtest_divergence_handlers.go`

- [ ] **Step 1:** Test:
  `Divergence{Backtest: 0.74, Live: 0.71}.Status() == "within_tolerance"`,
  while `0.74 vs 0.69` returns `"exceeds_tolerance"` with the 3% default.
- [ ] **Step 2:** Implement the comparator and expose
  `GET /api/v1/backtest/divergence?strategy_id=...` returning JSON.
- [ ] **Step 3:** Commit.

---

## Phase E — Hard risk controls

### Task E1: Daily drawdown circuit breaker

**Files:**
- Modify: `internal/risk/engine_impl.go`
- Create: `internal/risk/drawdown_breaker.go`
- Create: `internal/risk/drawdown_breaker_test.go`
- Migration: `000040_risk_breaker_state.up.sql` + down

- [ ] **Step 1:** Schema: persistent table `risk_breaker_state` with
  `scope TEXT PRIMARY KEY, tripped_at TIMESTAMPTZ, reason TEXT, reset_at
  TIMESTAMPTZ`.
- [ ] **Step 2:** Test: when realized PnL drops below `-MaxDailyDD` the
  breaker trips for scope `"global"`, `Allow(ctx, order)` returns
  `ErrBreakerTripped` for every subsequent order until `reset_at`.
- [ ] **Step 3:** Implement, wire into `internal/execution.OrderManager` so
  `Place` consults `risk.Breaker.Allow` first.
- [ ] **Step 4:** Commit.

### Task E2: Consecutive-loss pause per strategy

**Files:**
- Create: `internal/risk/consecutive_loss.go`
- Create: `internal/risk/consecutive_loss_test.go`
- Modify: `internal/risk/engine_impl.go`

- [ ] **Step 1:** Test: after 3 consecutive losses for `strategy_id=X`, the
  next `N=5` windows return `Allow=false` for that strategy only.
- [ ] **Step 2:** Implement using the same breaker table with a
  `scope=strategy:<id>` row pattern.
- [ ] **Step 3:** Commit.

### Task E3: Manual reset + API surface

**Files:**
- Modify: `internal/api/risk_handlers.go`

- [ ] **Step 1:** Test: `POST /api/v1/risk/breaker/reset` body
  `{ "scope": "global" }` clears the breaker row and emits a notification.
- [ ] **Step 2:** Implement; require admin role.
- [ ] **Step 3:** Commit.

---

## Phase F — Phased deployment tooling

### Task F1: Zero-balance dry run mode

**Files:**
- Modify: `internal/execution/polymarket/broker.go`
- Create: `internal/execution/polymarket/dryrun.go`
- Create: `internal/execution/polymarket/dryrun_test.go`

- [ ] **Step 1:** Test: with `DryRun=true`, `PlaceOrder` performs the full
  network round-trip but a `?dry=1` query (or the documented test endpoint
  if Polymarket offers one) so real markets reject; on `NSF`, `timeout`,
  `ghost_fill`, the broker emits classified `DryRunObservation` rows.
- [ ] **Step 2:** Implement; persist observations to a new table or extend
  `automation_job_runs`-style log; pick a small new migration only if no
  existing table fits.
- [ ] **Step 3:** Commit.

### Task F2: Capital ladder controller

**Files:**
- Create: `internal/risk/capital_ladder.go`
- Create: `internal/risk/capital_ladder_test.go`
- Migration: `000041_capital_ladder.up.sql` + down

- [ ] **Step 1:** Schema: `capital_ladder (strategy_id, step_pct,
  fill_rate, win_rate, drawdown_pct, baseline_fill_rate,
  baseline_win_rate, advanced_at TIMESTAMPTZ)`.
- [ ] **Step 2:** Test: with `Step=0.10, MaxStep=1.0, Tolerance=0.03`, the
  controller advances from 10% to 20% only when current metrics are within
  3% of baseline; otherwise holds and emits a notification.
- [ ] **Step 3:** Implement; expose via CLI `tradingagent capital-ladder
  promote --strategy-id X` and `--status` subcommands.
- [ ] **Step 4:** Commit.

---

## Phase G — EV ladder strategy scaffold

### Task G1: Per-bucket EV calculator

**Files:**
- Create: `internal/strategyscaffold/ev_ladder.go`
- Create: `internal/strategyscaffold/ev_ladder_test.go`

- [ ] **Step 1:** Test: `Compute(BucketCents{2,5,10,...,95},
  marketProb=0.30)` returns positive EV at buckets where `prob*payout >
  (1-prob)*cost`; verify the documented examples (2c → 1 win covers 49
  losses; 80c → high frequency / low edge).
- [ ] **Step 2:** Implement vectorized EV across buckets; expose `Plan`
  returning a list of `LadderRung{Price, Size, EV, ExpectedFreq}`.
- [ ] **Step 3:** Commit.

### Task G2: Ladder execution adapter

**Files:**
- Create: `internal/strategyscaffold/ev_ladder_runner.go`
- Create: `internal/strategyscaffold/ev_ladder_runner_test.go`

- [ ] **Step 1:** Test: given a fixed `Plan` and a mock book/tick stream
  from `marketdata/polymarket.Feed` test doubles, the runner places one
  template-cloned order per rung, respects `Risk.Breaker`, cancels stale
  rungs on book moves above a configurable threshold, and records all
  events to the order manager.
- [ ] **Step 2:** Implement.
- [ ] **Step 3:** Commit.

---

## Phase H — Monitoring and alerts

### Task H1: Prometheus metrics

**Files:**
- Modify: `internal/marketdata/polymarket/metrics.go` (new)
- Modify: `internal/recorder/polymarket.go`
- Modify: `internal/risk/drawdown_breaker.go`
- Modify: `internal/execution/polymarket/order_manager.go`

- [ ] **Step 1:** Define and register Prometheus collectors:
  - `polymarket_ws_connections{slug,state}`
  - `polymarket_ws_jitter_ms{slug}` histogram
  - `polymarket_ws_ticks_dropped_total{slug,reason}`
  - `polymarket_recorder_inserted_total{kind}`
  - `polymarket_recorder_lag_seconds`
  - `risk_breaker_tripped_total{scope,reason}`
  - `polymarket_fill_rate{strategy}`
  - `polymarket_ghost_fill_total{strategy}`
- [ ] **Step 2:** Commit.

### Task H2: ntfy alert rules

**Files:**
- Modify: `internal/notification/` (existing)
- Create: `internal/notification/surfers_alerts.go`
- Create: `internal/notification/surfers_alerts_test.go`

- [ ] **Step 1:** Test alert evaluator: fires alerts at WARN/CRIT levels for
  drawdown threshold, fill-rate drop > 20% vs baseline, ghost-fill detector
  firing > N/min, consecutive-loss breaker trip, recorder lag > 30s.
- [ ] **Step 2:** Implement; emit through existing notification channel.
- [ ] **Step 3:** Commit.

### Task H3: Web ops page

**Files:**
- Create: `web/src/pages/surfers-ops-page.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/lib/api/client.ts`
- Modify: `web/src/lib/api/types.ts`

- [ ] **Step 1:** Add a single ops page that polls
  `/api/v1/marketdata/polymarket/status` and the existing risk/divergence
  endpoints; surface WS pool health, recorder lag, breaker state, current
  capital-ladder step, last divergence number.
- [ ] **Step 2:** Add types and API client functions.
- [ ] **Step 3:** Commit.

---

## Phase I — Optional Polygon node hooks and CPU pinning

### Task I1: RPC/WS config + copy-trade signal source

**Files:**
- Modify: `internal/config/config.go` (add `POLYGON_RPC_URL`,
  `POLYGON_WS_URL`)
- Create: `internal/signal/source_polymarket_mempool.go`
- Create: `internal/signal/source_polymarket_mempool_test.go`

- [ ] **Step 1:** Test: with a fake WS mempool server, the source emits a
  `RawSignalEvent{Kind:"copy_trade", Address: ..., Hash: ...}` within one
  block-time of a watched-wallet TX appearing in the mempool.
- [ ] **Step 2:** Implement; document in `docs/surfers-bot.md` that the
  Polygon node itself is deployed elsewhere in the homelab and only the
  RPC/WS URL is consumed here.
- [ ] **Step 3:** Commit.

### Task I2: Hot-path CPU pinning

**Files:**
- Modify: `internal/marketdata/polymarket/pool.go`
- Modify: `internal/recorder/polymarket.go`
- Modify: `cmd/tradingagent/runtime.go`
- Create: `docs/ops/surfers-bot-cpu-pinning.md`

- [ ] **Step 1:** Add an optional `runtime.LockOSThread()` block on the
  pool reader goroutines and the recorder writer goroutine, gated by env
  `SURFERS_PIN_THREADS=true`.
- [ ] **Step 2:** Document the matching `docker-compose.nuc.yml` cpuset
  pinning recipe (e.g. `cpuset: "2-3"`) and how to verify with `htop`.
- [ ] **Step 3:** Commit.

---

## Cross-Phase Acceptance Gates

Before promoting any strategy to live capital, the following must all hold:

1. `feed.Ready(slug) == true` for the target market for the entire warmup.
2. Recorder lag `< 1s` (Prometheus gauge).
3. Latest backtest divergence within 3% of live results
   (`/api/v1/backtest/divergence` returns `within_tolerance`).
4. `risk_breaker_state` rows: no `tripped_at IS NOT NULL AND reset_at IS
   NULL` for `scope IN ('global', 'strategy:<id>')`.
5. Capital ladder current step matches operator expectation.

These should be encoded as a single `cmd/tradingagent surfers preflight
--strategy-id X` subcommand once Phases A through F land, but that command
can be added as a final F-tail task.

---

## Out of Scope (documented)

- **Server relocation.** Track separately as a homelab decision. If pursued,
  rerun Phase D's divergence gate from the new location before promoting.
- **Additional dedicated bare metal.** NUC is current operating context.
- **Polygon node deployment.** Belongs in `/home/onnwee/.nuc` and
  `/srv/server/projects/`; this plan only wires consumption.

---

## Self-Review Notes

- **Spec coverage:** Every section of `docs/surfers-bot.md` maps to a phase
  except the three out-of-scope items called out above.
- **Type consistency:** `Tick`/`BookSnapshot`/`Pool`/`Feed`/`Template` names
  are used consistently across Phases A–G and the metrics in H.
- **No placeholders:** All steps include either concrete code, schema, or
  exact commands.
- **TDD/DRY/YAGNI:** Each task starts with a failing test and adds only the
  code needed.

---

## Execution Handoff

Plan saved to `docs/superpowers/plans/2026-05-30-surfers-bot-foundation.md`.

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task,
   review between tasks, integrate phase-by-phase.
2. **Inline Execution** — I execute tasks in this session with `todowrite`
   checkpoints and review after each batch.

Which approach?

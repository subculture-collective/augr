# Chunked Polymarket Discovery Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Replace monolithic `polymarket_strategy_discovery` with resumable DB-backed chunks so slow LLM proposals cannot burn the whole 15-minute job window and lose progress.

**Architecture:** Add a persistent `polymarket_discovery_runs` checkpoint table and repository, then add a chunk runner that advances one phase at a time: `screen`, `propose`, `deploy`, `done`. Each automation invocation processes a small bounded number of candidates, persists `candidate_index`, accepted proposals, errors, and summary, then exits successfully unless the checkpoint itself cannot be saved.

**Tech Stack:** Go, PostgreSQL/pgx, existing `internal/polymarketdiscovery` primitives, existing repository patterns under `internal/repository/postgres`, current automation orchestrator.

---

## File Structure

- Create: `migrations/000037_polymarket_discovery_progress.up.sql`  
  Defines `polymarket_discovery_runs` checkpoint storage.
- Create: `migrations/000037_polymarket_discovery_progress.down.sql`  
  Drops the checkpoint table.
- Modify: `internal/repository/postgres/schema_version.go`  
  Bump `RequiredSchemaVersion` from `36` to `37`.
- Create: `internal/domain/polymarket_discovery.go`  
  Domain checkpoint types: statuses, phases, candidate snapshot, accepted proposal, summary, run.
- Modify: `internal/repository/interfaces.go`  
  Add `PolymarketDiscoveryRunRepository` interface.
- Create: `internal/repository/postgres/polymarket_discovery_run.go`  
  Postgres CRUD implementation mirroring `OvernightBacktestRunRepo`.
- Create: `internal/repository/postgres/polymarket_discovery_run_test.go`  
  Repository query/JSON round-trip tests.
- Create: `internal/automation/polymarket_discovery_chunker.go`  
  Resumable chunk runner built on `polymarketdiscovery.FetchOpenMarkets`, `ScreenMarkets`, `BuildMarketContext`, `GenerateProposal`, and deploy helper.
- Create: `internal/automation/polymarket_discovery_chunker_test.go`  
  Unit tests for phase transitions, chunk budget, max deployment stop condition, and per-candidate error handling.
- Modify: `internal/automation/orchestrator.go`  
  Add `PolymarketDiscoveryRuns repository.PolymarketDiscoveryRunRepository` to `OrchestratorDeps`.
- Modify: `internal/automation/jobs_polymarket_discovery.go`  
  Replace monolithic `polymarketdiscovery.Run` call with the new chunker.
- Modify: `cmd/tradingagent/runtime.go`  
  Instantiate `pgrepo.NewPolymarketDiscoveryRunRepo(db.Pool)` and wire it into `automation.OrchestratorDeps`.

---

## Data Model

Use one row per logical Polymarket discovery run. JSONB is appropriate because this is checkpoint state, not an analytics schema.

```sql
CREATE TABLE IF NOT EXISTS polymarket_discovery_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    phase TEXT NOT NULL CHECK (phase IN ('screen', 'propose', 'deploy', 'done')),
    candidate_index INTEGER NOT NULL DEFAULT 0 CHECK (candidate_index >= 0),
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
    accepted JSONB NOT NULL DEFAULT '[]'::jsonb,
    deployed JSONB NOT NULL DEFAULT '[]'::jsonb,
    errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_polymarket_discovery_runs_active
    ON polymarket_discovery_runs (status, updated_at DESC)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_polymarket_discovery_runs_started_at
    ON polymarket_discovery_runs (started_at DESC);
```

Domain JSON shapes:

```go
const (
    PolymarketDiscoveryStatusRunning   = "running"
    PolymarketDiscoveryStatusCompleted = "completed"
    PolymarketDiscoveryStatusFailed    = "failed"

    PolymarketDiscoveryPhaseScreen  = "screen"
    PolymarketDiscoveryPhasePropose = "propose"
    PolymarketDiscoveryPhaseDeploy  = "deploy"
    PolymarketDiscoveryPhaseDone    = "done"
)

type PolymarketDiscoveryCandidate struct {
    Slug             string  `json:"slug"`
    Question         string  `json:"question"`
    Description      string  `json:"description,omitempty"`
    Category         string  `json:"category,omitempty"`
    ConditionID      string  `json:"condition_id,omitempty"`
    EndDate          string  `json:"end_date,omitempty"`
    Volume24Hr       float64 `json:"volume_24hr,omitempty"`
    Liquidity        float64 `json:"liquidity,omitempty"`
    BestBid          float64 `json:"best_bid,omitempty"`
    BestAsk          float64 `json:"best_ask,omitempty"`
    Spread           float64 `json:"spread,omitempty"`
    LastTradePrice   float64 `json:"last_trade_price,omitempty"`
    ResolutionSource string  `json:"resolution_source,omitempty"`
}

type PolymarketDiscoveryAccepted struct {
    Candidate PolymarketDiscoveryCandidate `json:"candidate"`
    Proposal  json.RawMessage              `json:"proposal"`
}

type PolymarketDiscoveryDeployed struct {
    StrategyID string  `json:"strategy_id"`
    Slug       string  `json:"slug"`
    Template   string  `json:"template"`
    Name       string  `json:"name"`
    Direction  string  `json:"direction"`
    Conviction float64 `json:"conviction"`
    Reused     bool    `json:"reused"`
}

type PolymarketDiscoverySummary struct {
    FetchedAll int `json:"fetched_all,omitempty"`
    Screened   int `json:"screened,omitempty"`
    Proposed   int `json:"proposed,omitempty"`
    Skipped    int `json:"skipped,omitempty"`
    Accepted   int `json:"accepted,omitempty"`
    Deployed   int `json:"deployed,omitempty"`
}

type PolymarketDiscoveryRun struct {
    ID             uuid.UUID                       `json:"id"`
    Status         string                          `json:"status"`
    Phase          string                          `json:"phase"`
    CandidateIndex int                             `json:"candidate_index"`
    Candidates     []PolymarketDiscoveryCandidate `json:"candidates"`
    Accepted       []PolymarketDiscoveryAccepted  `json:"accepted"`
    Deployed       []PolymarketDiscoveryDeployed  `json:"deployed"`
    Errors         []string                        `json:"errors"`
    Summary        PolymarketDiscoverySummary     `json:"summary"`
    StartedAt      time.Time                       `json:"started_at"`
    UpdatedAt      time.Time                       `json:"updated_at"`
    CompletedAt    *time.Time                      `json:"completed_at,omitempty"`
}
```

---

## Task 1: Migration and schema version

**Files:**
- Create: `migrations/000037_polymarket_discovery_progress.up.sql`
- Create: `migrations/000037_polymarket_discovery_progress.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`

- [ ] **Step 1: Add the up migration**

Create `migrations/000037_polymarket_discovery_progress.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS polymarket_discovery_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    phase TEXT NOT NULL CHECK (phase IN ('screen', 'propose', 'deploy', 'done')),
    candidate_index INTEGER NOT NULL DEFAULT 0 CHECK (candidate_index >= 0),
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
    accepted JSONB NOT NULL DEFAULT '[]'::jsonb,
    deployed JSONB NOT NULL DEFAULT '[]'::jsonb,
    errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_polymarket_discovery_runs_active
    ON polymarket_discovery_runs (status, updated_at DESC)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_polymarket_discovery_runs_started_at
    ON polymarket_discovery_runs (started_at DESC);
```

- [ ] **Step 2: Add the down migration**

Create `migrations/000037_polymarket_discovery_progress.down.sql`:

```sql
DROP TABLE IF EXISTS polymarket_discovery_runs;
```

- [ ] **Step 3: Bump required schema version**

In `internal/repository/postgres/schema_version.go`, change:

```go
const RequiredSchemaVersion = 36
```

to:

```go
const RequiredSchemaVersion = 37
```

- [ ] **Step 4: Validate migrations compile structurally**

Run:

```bash
go test ./internal/repository/postgres -run TestCurrentSchemaVersion -count=1
```

Expected: PASS.

---

## Task 2: Domain and repository interface

**Files:**
- Create: `internal/domain/polymarket_discovery.go`
- Modify: `internal/repository/interfaces.go`

- [ ] **Step 1: Add domain checkpoint types**

Create `internal/domain/polymarket_discovery.go` with the domain types shown in the Data Model section. Include imports:

```go
package domain

import (
    "encoding/json"
    "time"

    "github.com/google/uuid"
)
```

Also add:

```go
func NewPolymarketDiscoveryRun() PolymarketDiscoveryRun {
    return PolymarketDiscoveryRun{
        ID:     uuid.New(),
        Status: PolymarketDiscoveryStatusRunning,
        Phase:  PolymarketDiscoveryPhaseScreen,
    }
}
```

- [ ] **Step 2: Add repository interface**

In `internal/repository/interfaces.go`, directly after `OvernightBacktestRunRepository`, add:

```go
// PolymarketDiscoveryRunRepository persists resumable Polymarket discovery progress.
type PolymarketDiscoveryRunRepository interface {
    Create(ctx context.Context, run *domain.PolymarketDiscoveryRun) error
    Get(ctx context.Context, id uuid.UUID) (*domain.PolymarketDiscoveryRun, error)
    GetActive(ctx context.Context) (*domain.PolymarketDiscoveryRun, error)
    Update(ctx context.Context, run *domain.PolymarketDiscoveryRun) error
    ListLatest(ctx context.Context, limit int) ([]domain.PolymarketDiscoveryRun, error)
}
```

- [ ] **Step 3: Run compile check for domain/repository packages**

Run:

```bash
go test ./internal/domain ./internal/repository -count=1
```

Expected: PASS.

---

## Task 3: Postgres repository implementation

**Files:**
- Create: `internal/repository/postgres/polymarket_discovery_run.go`
- Create: `internal/repository/postgres/polymarket_discovery_run_test.go`

- [ ] **Step 1: Write repository implementation**

Create `internal/repository/postgres/polymarket_discovery_run.go` by following `internal/repository/postgres/overnight_backtest_run.go` exactly, but with:

```go
type PolymarketDiscoveryRunRepo struct{ pool *pgxpool.Pool }

var _ repository.PolymarketDiscoveryRunRepository = (*PolymarketDiscoveryRunRepo)(nil)

func NewPolymarketDiscoveryRunRepo(pool *pgxpool.Pool) *PolymarketDiscoveryRunRepo {
    return &PolymarketDiscoveryRunRepo{pool: pool}
}
```

Use this select SQL:

```go
const polymarketDiscoverySelectSQL = `SELECT id, status, phase, candidate_index, candidates, accepted, deployed, errors, summary, started_at, updated_at, completed_at FROM polymarket_discovery_runs`
```

Marshal/unmarshal exactly these JSON fields:

```go
Candidates []domain.PolymarketDiscoveryCandidate
Accepted   []domain.PolymarketDiscoveryAccepted
Deployed   []domain.PolymarketDiscoveryDeployed
Errors     []string
Summary    domain.PolymarketDiscoverySummary
```

- [ ] **Step 2: Write repository tests**

Create `internal/repository/postgres/polymarket_discovery_run_test.go` mirroring `overnight_backtest_run_test.go`. Include a test that:

1. creates a temp schema/table with the migration DDL,
2. inserts a `domain.PolymarketDiscoveryRun` with one candidate and one accepted proposal,
3. calls `GetActive`,
4. updates `CandidateIndex`, `Summary.Proposed`, and `Errors`,
5. calls `Get`,
6. verifies JSON fields round-trip.

The accepted proposal can be:

```go
json.RawMessage(`{"template":"news_catalyst","name":"Test","direction":"YES","conviction":0.7,"time_horizon":"days","watch_terms":["test"],"invalidate_if":["invalid"]}`)
```

- [ ] **Step 3: Run repository tests**

Run:

```bash
go test ./internal/repository/postgres -run 'PolymarketDiscoveryRun|SchemaVersion' -count=1
```

Expected: PASS.

---

## Task 4: Chunker implementation

**Files:**
- Create: `internal/automation/polymarket_discovery_chunker.go`
- Modify: `internal/polymarketdiscovery/orchestrator.go`

- [ ] **Step 1: Export deploy helper safely**

In `internal/polymarketdiscovery/orchestrator.go`, rename:

```go
func deployStrategy(
```

to:

```go
func DeployStrategy(
```

Then update its existing call inside `Run`:

```go
dep, depErr := DeployStrategy(ctx, cfg, deps, a.mc, a.proposal)
```

- [ ] **Step 2: Create chunker constants and struct**

Create `internal/automation/polymarket_discovery_chunker.go`:

```go
package automation

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "time"

    "github.com/google/uuid"

    "github.com/PatrickFanella/get-rich-quick/internal/domain"
    "github.com/PatrickFanella/get-rich-quick/internal/polymarketdiscovery"
    "github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const (
    polymarketDiscoveryProposePerChunk = 2
    polymarketDiscoveryChunkTimeout    = 10 * time.Minute
    polymarketDiscoveryMaxRunAge       = 24 * time.Hour
)

type polymarketDiscoveryChunker struct {
    deps            OrchestratorDeps
    progress        repository.PolymarketDiscoveryRunRepository
    logger          *slog.Logger
    proposePerChunk int
}
```

- [ ] **Step 3: Implement constructor and RunChunk skeleton**

Add:

```go
func newPolymarketDiscoveryChunker(deps OrchestratorDeps, logger *slog.Logger) polymarketDiscoveryChunker {
    if logger == nil {
        logger = slog.Default()
    }
    return polymarketDiscoveryChunker{
        deps:            deps,
        progress:        deps.PolymarketDiscoveryRuns,
        logger:          logger,
        proposePerChunk: polymarketDiscoveryProposePerChunk,
    }
}

func (c polymarketDiscoveryChunker) RunChunk(ctx context.Context) error {
    if c.progress == nil {
        return fmt.Errorf("polymarket_discovery: progress repository not configured")
    }
    run, err := c.progress.GetActive(ctx)
    if err != nil {
        if err == repository.ErrNotFound {
            run = nil
        } else {
            return err
        }
    }
    if run == nil {
        now := time.Now()
        run = &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhaseScreen, StartedAt: now, UpdatedAt: now}
        if err := c.progress.Create(ctx, run); err != nil {
            return err
        }
    }
    if time.Since(run.StartedAt) > polymarketDiscoveryMaxRunAge {
        run.Status = domain.PolymarketDiscoveryStatusFailed
        now := time.Now()
        run.CompletedAt = &now
        run.UpdatedAt = now
        run.Errors = append(run.Errors, "polymarket_discovery: run exceeded max age")
        return c.progress.Update(ctx, run)
    }

    chunkCtx, cancel := context.WithTimeout(ctx, polymarketDiscoveryChunkTimeout)
    defer cancel()

    switch run.Phase {
    case domain.PolymarketDiscoveryPhaseScreen:
        return c.runScreen(chunkCtx, run)
    case domain.PolymarketDiscoveryPhasePropose:
        return c.runProposeChunk(chunkCtx, run)
    case domain.PolymarketDiscoveryPhaseDeploy:
        return c.runDeploy(chunkCtx, run)
    default:
        return fmt.Errorf("polymarket_discovery: unknown phase %q", run.Phase)
    }
}
```

When implementing for real, use `errors.Is(err, repository.ErrNotFound)` rather than direct equality.

- [ ] **Step 4: Implement screen phase**

Add helpers that convert between `polymarketdiscovery.GammaMarket` and domain candidates. Preserve enough fields for `BuildMarketContext` to work.

```go
func (c polymarketDiscoveryChunker) runScreen(ctx context.Context, run *domain.PolymarketDiscoveryRun) error {
    cfg := polymarketDiscovery.Config{Screener: polymarketdiscovery.DefaultScreenerConfig(), MaxDeployments: 3, AutoWatchSlug: true}
    markets, err := polymarketdiscovery.FetchOpenMarkets(ctx, cfg.GammaBaseURL, cfg.Screener.FetchLimit)
    if err != nil {
        return fmt.Errorf("polymarket_discovery: fetch open markets: %w", err)
    }
    screened := polymarketdiscovery.ScreenMarkets(markets, cfg.Screener)
    run.Candidates = candidatesFromGammaMarkets(screened)
    run.CandidateIndex = 0
    run.Phase = domain.PolymarketDiscoveryPhasePropose
    run.Summary.FetchedAll = len(markets)
    run.Summary.Screened = len(run.Candidates)
    run.UpdatedAt = time.Now()
    return c.progress.Update(ctx, run)
}
```

- [ ] **Step 5: Implement propose chunk phase**

Process at most `proposePerChunk` candidates per invocation. On candidate-level errors, append to `run.Errors` and continue. If accepted count reaches `MaxDeployments`, switch to deploy phase.

```go
func (c polymarketDiscoveryChunker) runProposeChunk(ctx context.Context, run *domain.PolymarketDiscoveryRun) error {
    if c.deps.LLMProvider == nil {
        return fmt.Errorf("polymarket_discovery: LLM provider not configured")
    }
    cfg := polymarketdiscovery.Config{Screener: polymarketdiscovery.DefaultScreenerConfig(), MaxDeployments: 3, AutoWatchSlug: true}
    genCfg := cfg.Generator
    genCfg.Provider = c.deps.LLMProvider

    start := run.CandidateIndex
    end := start + c.proposePerChunk
    if end > len(run.Candidates) {
        end = len(run.Candidates)
    }

    for i := start; i < end; i++ {
        if err := ctx.Err(); err != nil {
            return err
        }
        candidate := run.Candidates[i]
        market := gammaMarketFromCandidate(candidate)
        mc, err := polymarketdiscovery.BuildMarketContext(ctx, market, c.deps.PolymarketAccountRepo)
        if err != nil {
            run.Errors = append(run.Errors, fmt.Sprintf("context %s: %v", candidate.Slug, err))
            continue
        }
        proposal, err := polymarketdiscovery.GenerateProposal(ctx, genCfg, mc, c.logger)
        if err != nil {
            run.Errors = append(run.Errors, fmt.Sprintf("propose %s: %v", candidate.Slug, err))
            continue
        }
        run.Summary.Proposed++
        if proposal.Skip || proposal.Conviction < 0.45 {
            run.Summary.Skipped++
            continue
        }
        raw, err := json.Marshal(proposal)
        if err != nil {
            return err
        }
        run.Accepted = append(run.Accepted, domain.PolymarketDiscoveryAccepted{Candidate: candidate, Proposal: raw})
        run.Summary.Accepted = len(run.Accepted)
        if len(run.Accepted) >= cfg.MaxDeployments {
            end = i + 1
            break
        }
    }

    run.CandidateIndex = end
    if len(run.Accepted) >= cfg.MaxDeployments || run.CandidateIndex >= len(run.Candidates) {
        run.Phase = domain.PolymarketDiscoveryPhaseDeploy
    }
    run.UpdatedAt = time.Now()
    return c.progress.Update(ctx, run)
}
```

- [ ] **Step 6: Implement deploy phase**

Convert each accepted proposal back to a `polymarketdiscovery.Proposal`, rebuild `MarketContext`, call `polymarketdiscovery.DeployStrategy`, append deployed results, mark `done/completed`, and call `polymarketdiscovery.StoreLastResult` for existing API compatibility.

---

## Task 5: Wire automation runtime

**Files:**
- Modify: `internal/automation/orchestrator.go`
- Modify: `internal/automation/jobs_polymarket_discovery.go`
- Modify: `cmd/tradingagent/runtime.go`

- [ ] **Step 1: Add dependency to orchestrator**

In `internal/automation/orchestrator.go`, add this field near `OvernightBacktestRuns`:

```go
PolymarketDiscoveryRuns repository.PolymarketDiscoveryRunRepository
```

- [ ] **Step 2: Change job implementation**

In `internal/automation/jobs_polymarket_discovery.go`, replace the 15-minute monolith body with:

```go
func (o *JobOrchestrator) polymarketDiscovery(ctx context.Context) error {
    o.logger.Info("polymarket_strategy_discovery: chunk starting")
    chunker := newPolymarketDiscoveryChunker(o.deps, o.logger)
    if err := chunker.RunChunk(ctx); err != nil {
        return fmt.Errorf("polymarket_strategy_discovery: chunk failed: %w", err)
    }
    o.logger.Info("polymarket_strategy_discovery: chunk completed")
    return nil
}
```

Add `fmt` to imports and remove unused `log/slog`/`time` imports if no longer needed.

- [ ] **Step 3: Wire repository in runtime**

In `cmd/tradingagent/runtime.go`, near line 389 where `overnightBacktestRunRepo` is created, add:

```go
polymarketDiscoveryRunRepo := pgrepo.NewPolymarketDiscoveryRunRepo(db.Pool)
```

Then pass it into `automation.OrchestratorDeps`:

```go
PolymarketDiscoveryRuns: polymarketDiscoveryRunRepo,
```

- [ ] **Step 4: Compile automation and runtime**

Run:

```bash
go test ./internal/automation ./cmd/tradingagent -count=1
```

Expected: PASS.

---

## Task 6: Chunker tests

**Files:**
- Create: `internal/automation/polymarket_discovery_chunker_test.go`

- [ ] **Step 1: Add in-memory progress repo**

Create a test stub implementing `repository.PolymarketDiscoveryRunRepository` with `Create`, `Get`, `GetActive`, `Update`, `ListLatest` methods. Store one `domain.PolymarketDiscoveryRun` in memory and deep-copy JSON slices by marshaling/unmarshaling to avoid mutation leakage.

- [ ] **Step 2: Test phase transition after screen**

Use an `httptest.Server` as `GammaBaseURL` if the chunker supports config injection; if not, add unexported function variables for `fetchOpenMarkets`, `screenMarkets`, `generateProposal`, and `deployStrategy` to make chunker tests deterministic.

Expected assertions:

```go
if run.Phase != domain.PolymarketDiscoveryPhasePropose { t.Fatal(...) }
if run.Summary.Screened != 2 { t.Fatal(...) }
```

- [ ] **Step 3: Test propose chunk budget**

Set `proposePerChunk: 2` and five candidates. Stub `generateProposal` to return accepted proposals. After one chunk:

```go
if run.CandidateIndex != 2 { t.Fatalf("candidate_index = %d, want 2", run.CandidateIndex) }
if len(run.Accepted) != 2 { t.Fatalf("accepted = %d, want 2", len(run.Accepted)) }
if run.Phase != domain.PolymarketDiscoveryPhasePropose { t.Fatal("should continue proposing") }
```

- [ ] **Step 4: Test max deployment stop condition**

Use `MaxDeployments=3` behavior. After enough chunks to accept three proposals, assert phase changes to `deploy` even if candidates remain.

- [ ] **Step 5: Test candidate-level errors do not fail the chunk**

Stub one proposal call to return `context deadline exceeded`; assert `RunChunk` returns nil, `Errors` contains the candidate slug, and `CandidateIndex` advances.

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./internal/automation -run PolymarketDiscoveryChunker -count=1
```

Expected: PASS.

---

## Task 7: Full verification

**Files:**
- All modified files

- [ ] **Step 1: Run targeted package tests**

Run:

```bash
go test ./internal/domain ./internal/repository ./internal/repository/postgres ./internal/polymarketdiscovery ./internal/automation ./cmd/tradingagent -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader suite if time permits**

Run:

```bash
go test ./... -count=1
```

Expected: PASS or only known unrelated integration failures documented with exact error output.

- [ ] **Step 3: Validate compose config**

Run:

```bash
docker compose -f docker-compose.nuc.yml config --quiet
```

Expected: no output and exit code 0.

---

## Deployment Notes

After merge/deploy:

1. Apply migration 37 using the compose migrate profile.
2. Restart app so schema gate sees version 37.
3. Re-enable `polymarket_strategy_discovery` if auto-disabled.
4. Trigger one manual run.
5. Confirm `polymarket_discovery_runs` has one `running/propose` row after first chunk instead of an automation `error` after 15 minutes.

Validation SQL:

```sql
SELECT id, status, phase, candidate_index, summary, started_at, updated_at, completed_at
FROM polymarket_discovery_runs
ORDER BY started_at DESC
LIMIT 5;

SELECT job_name, status, started_at, completed_at, error
FROM automation_job_runs
WHERE job_name = 'polymarket_strategy_discovery'
ORDER BY started_at DESC
LIMIT 5;
```

---

## Self-Review

- Spec coverage: plan covers schema, repository, chunk runner, automation wiring, runtime wiring, tests, and deployment verification.
- Placeholder scan: no implementation-critical TBD placeholders remain. Test stubbing gives two concrete options because the existing code has hard package functions; prefer unexported function variables only if direct `httptest` injection cannot cover all paths.
- Type consistency: repository names and domain names use `PolymarketDiscoveryRun` consistently; automation dependency name is `PolymarketDiscoveryRuns`; phases match migration CHECK values.

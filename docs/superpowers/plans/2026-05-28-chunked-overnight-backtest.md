# Chunked Overnight Backtest Implementation Plan

> **For agentic workers:** Execute this plan task-by-task. Recommended path:
> dispatch a fresh subagent per task, review each result with `review-quality`,
> then continue. For complex multi-agent splits, use
> `parallel-feature-development`, `team-composition-patterns`, and
> `team-communication-protocols`. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** Replace the monolithic `overnight_backtest` LLM-heavy run with resumable chunks so it releases the GPU after a small batch and resumes on the next cron tick.

**Architecture:** Add persistent overnight discovery progress in PostgreSQL, then add a small chunk runner that advances exactly one phase at a time: screen, generate a limited candidate batch, sweep/validate once generation is complete, and deploy. The automation job becomes a short repeating resumable job instead of one 4-hour blocking run.

**Tech Stack:** Go, PostgreSQL/pgx, robfig cron automation jobs, existing `internal/discovery` pipeline primitives, existing repository patterns under `internal/repository/postgres`.

---

## File Structure

- Create: `migrations/000036_overnight_backtest_progress.up.sql`  
  Defines `overnight_backtest_runs` to persist resumable state.
- Create: `migrations/000036_overnight_backtest_progress.down.sql`  
  Drops the progress table.
- Modify: `internal/repository/postgres/schema_version.go`  
  Bump `RequiredSchemaVersion` from `35` to `36`.
- Create: `internal/domain/overnight_backtest.go`  
  Domain types for run status, phase, candidate records, generated configs, and progress snapshots.
- Modify: `internal/repository/interfaces.go`  
  Add `OvernightBacktestRunRepository` interface.
- Create: `internal/repository/postgres/overnight_backtest_run.go`  
  Postgres implementation for create/get-active/update/list-latest progress.
- Create: `internal/repository/postgres/overnight_backtest_run_test.go`  
  Unit tests for JSON marshaling/query behavior and integration CRUD tests.
- Create: `internal/automation/overnight_backtest_chunker.go`  
  Resumable orchestration logic with small GPU-budgeted chunks.
- Create: `internal/automation/overnight_backtest_chunker_test.go`  
  Tests for phase transitions and chunk budgets.
- Modify: `internal/automation/orchestrator.go`  
  Add progress repository to `OrchestratorDeps`.
- Modify: `internal/automation/jobs_overnight.go`  
  Change cron cadence and make `overnightBacktest` call the chunk runner.
- Modify: `cmd/tradingagent/runtime.go`  
  Wire `pgrepo.NewOvernightBacktestRunRepo(db.Pool)` into `automation.OrchestratorDeps`.
- Optional modify: `docs/reference/architecture.md`  
  Document chunked overnight behavior if this repo expects architecture docs to track scheduler changes.

---

## Data Model

Use one row per logical overnight run. JSONB is acceptable here because this is job checkpoint state, not analytics-facing relational data.

```sql
CREATE TABLE overnight_backtest_runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
  phase TEXT NOT NULL CHECK (phase IN ('screen', 'generate', 'sweep_validate_deploy', 'done')),
  candidate_index INTEGER NOT NULL DEFAULT 0,
  candidates JSONB NOT NULL DEFAULT '[]',
  generated JSONB NOT NULL DEFAULT '[]',
  errors JSONB NOT NULL DEFAULT '[]',
  summary JSONB NOT NULL DEFAULT '{}',
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMPTZ
);

CREATE INDEX idx_overnight_backtest_runs_active
  ON overnight_backtest_runs (status, updated_at DESC)
  WHERE status = 'running';

CREATE INDEX idx_overnight_backtest_runs_started_at
  ON overnight_backtest_runs (started_at DESC);
```

Domain JSON shapes:

```go
type OvernightBacktestCandidate struct {
	Ticker     string             `json:"ticker"`
	Bars       []domain.OHLCV     `json:"bars"`
	Indicators []domain.Indicator `json:"indicators"`
	Close      float64            `json:"close"`
	ADV        float64            `json:"adv"`
	ATR        float64            `json:"atr"`
}

type OvernightBacktestGenerated struct {
	Ticker string                  `json:"ticker"`
	Config rules.RulesEngineConfig `json:"config"`
}
```

---

## Constants and Budgets

Use conservative defaults first. These can become config later if needed.

```go
const (
	overnightBacktestWatchlistLimit       = 50
	overnightBacktestGeneratePerChunk     = 2
	overnightBacktestChunkTimeout         = 20 * time.Minute
	overnightBacktestMaxRunAge            = 18 * time.Hour
	overnightBacktestGenerationMaxRetries = 1
)
```

Schedule the chunk job every 30 minutes during the overnight window:

```go
overnightBacktestSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron,
	Cron: "*/30 1-5 * * 2-6",
	SkipWeekends: false,
	SkipHolidays: false,
}
```

Why this shape:
- each run uses at most 2 LLM generations, then exits;
- sweep/validation is CPU/data work and runs only after generation is complete;
- the automation overlap guard prevents two chunks of the same job running concurrently;
- other GPU jobs get natural gaps between chunks.

---

## Task 1: Add the progress migration

**Files:**
- Create: `migrations/000036_overnight_backtest_progress.up.sql`
- Create: `migrations/000036_overnight_backtest_progress.down.sql`
- Modify: `internal/repository/postgres/schema_version.go`

- [ ] **Step 1: Write the up migration**

Create `migrations/000036_overnight_backtest_progress.up.sql`:

```sql
CREATE TABLE IF NOT EXISTS overnight_backtest_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    phase TEXT NOT NULL CHECK (phase IN ('screen', 'generate', 'sweep_validate_deploy', 'done')),
    candidate_index INTEGER NOT NULL DEFAULT 0 CHECK (candidate_index >= 0),
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
    generated JSONB NOT NULL DEFAULT '[]'::jsonb,
    errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_overnight_backtest_runs_active
    ON overnight_backtest_runs (status, updated_at DESC)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_overnight_backtest_runs_started_at
    ON overnight_backtest_runs (started_at DESC);
```

- [ ] **Step 2: Write the down migration**

Create `migrations/000036_overnight_backtest_progress.down.sql`:

```sql
DROP TABLE IF EXISTS overnight_backtest_runs;
```

- [ ] **Step 3: Bump schema version**

Modify `internal/repository/postgres/schema_version.go`:

```go
const RequiredSchemaVersion = 36
```

- [ ] **Step 4: Run schema sync tests**

Run:

```bash
go test -count=1 ./cmd/tradingagent -run SchemaVersion
```

Expected: PASS.

---

## Task 2: Add domain types and repository interface

**Files:**
- Create: `internal/domain/overnight_backtest.go`
- Modify: `internal/repository/interfaces.go`

- [ ] **Step 1: Add domain types**

Create `internal/domain/overnight_backtest.go`:

```go
package domain

import (
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
)

const (
	OvernightBacktestStatusRunning   = "running"
	OvernightBacktestStatusCompleted = "completed"
	OvernightBacktestStatusFailed    = "failed"

	OvernightBacktestPhaseScreen              = "screen"
	OvernightBacktestPhaseGenerate            = "generate"
	OvernightBacktestPhaseSweepValidateDeploy = "sweep_validate_deploy"
	OvernightBacktestPhaseDone                = "done"
)

type OvernightBacktestCandidate struct {
	Ticker     string      `json:"ticker"`
	Bars       []OHLCV     `json:"bars"`
	Indicators []Indicator `json:"indicators"`
	Close      float64     `json:"close"`
	ADV        float64     `json:"adv"`
	ATR        float64     `json:"atr"`
}

type OvernightBacktestGenerated struct {
	Ticker string                  `json:"ticker"`
	Config rules.RulesEngineConfig `json:"config"`
}

type OvernightBacktestSummary struct {
	Candidates int `json:"candidates,omitempty"`
	Generated  int `json:"generated,omitempty"`
	Swept      int `json:"swept,omitempty"`
	Validated  int `json:"validated,omitempty"`
	Deployed   int `json:"deployed,omitempty"`
}

type OvernightBacktestRun struct {
	ID             uuid.UUID                    `json:"id"`
	Status         string                       `json:"status"`
	Phase          string                       `json:"phase"`
	CandidateIndex int                          `json:"candidate_index"`
	Candidates     []OvernightBacktestCandidate `json:"candidates"`
	Generated      []OvernightBacktestGenerated `json:"generated"`
	Errors         []string                     `json:"errors"`
	Summary        OvernightBacktestSummary     `json:"summary"`
	StartedAt      time.Time                    `json:"started_at"`
	UpdatedAt      time.Time                    `json:"updated_at"`
	CompletedAt    *time.Time                   `json:"completed_at,omitempty"`
}

func NewOvernightBacktestRun() OvernightBacktestRun {
	return OvernightBacktestRun{
		ID:     uuid.New(),
		Status: OvernightBacktestStatusRunning,
		Phase:  OvernightBacktestPhaseScreen,
	}
}
```

- [ ] **Step 2: Add repository interface**

Modify `internal/repository/interfaces.go`. Add this near the backtest repository interfaces:

```go
// OvernightBacktestRunRepository persists resumable overnight backtest progress.
type OvernightBacktestRunRepository interface {
	Create(ctx context.Context, run *domain.OvernightBacktestRun) error
	Get(ctx context.Context, id uuid.UUID) (*domain.OvernightBacktestRun, error)
	GetActive(ctx context.Context) (*domain.OvernightBacktestRun, error)
	Update(ctx context.Context, run *domain.OvernightBacktestRun) error
	ListLatest(ctx context.Context, limit int) ([]domain.OvernightBacktestRun, error)
}
```

- [ ] **Step 3: Run compile check for affected packages**

Run:

```bash
go test -run '^$' ./internal/domain ./internal/repository
```

Expected: PASS.

---

## Task 3: Implement the Postgres progress repository

**Files:**
- Create: `internal/repository/postgres/overnight_backtest_run.go`
- Create: `internal/repository/postgres/overnight_backtest_run_test.go`

- [ ] **Step 1: Write JSON helper tests first**

Create `internal/repository/postgres/overnight_backtest_run_test.go` with these tests first:

```go
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestMarshalOvernightBacktestJSONSlices(t *testing.T) {
	run := domain.NewOvernightBacktestRun()
	run.Candidates = []domain.OvernightBacktestCandidate{{Ticker: "AAPL", Close: 200}}
	run.Generated = []domain.OvernightBacktestGenerated{{Ticker: "AAPL"}}
	run.Errors = []string{"sample error"}
	run.Summary = domain.OvernightBacktestSummary{Candidates: 1, Generated: 1}

	candidates, generated, errs, summary, err := marshalOvernightBacktestRunJSON(run)
	if err != nil {
		t.Fatalf("marshalOvernightBacktestRunJSON() error = %v", err)
	}
	if len(candidates) == 0 || len(generated) == 0 || len(errs) == 0 || len(summary) == 0 {
		t.Fatalf("expected non-empty json blobs")
	}
}

func TestBuildOvernightBacktestListLatestLimit(t *testing.T) {
	query, args := buildOvernightBacktestListLatestQuery(0)
	if len(args) != 1 || args[0] != 20 {
		t.Fatalf("args = %#v, want default limit 20", args)
	}
	assertContains(t, query, "FROM overnight_backtest_runs")
	assertContains(t, query, "ORDER BY started_at DESC")
	assertContains(t, query, "LIMIT $1")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test -count=1 ./internal/repository/postgres -run 'OvernightBacktest'
```

Expected: FAIL because repository helpers do not exist.

- [ ] **Step 3: Implement the repository**

Create `internal/repository/postgres/overnight_backtest_run.go`:

```go
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type OvernightBacktestRunRepo struct {
	pool *pgxpool.Pool
}

var _ repository.OvernightBacktestRunRepository = (*OvernightBacktestRunRepo)(nil)

func NewOvernightBacktestRunRepo(pool *pgxpool.Pool) *OvernightBacktestRunRepo {
	return &OvernightBacktestRunRepo{pool: pool}
}

func (r *OvernightBacktestRunRepo) Create(ctx context.Context, run *domain.OvernightBacktestRun) error {
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	candidates, generated, errs, summary, err := marshalOvernightBacktestRunJSON(*run)
	if err != nil {
		return err
	}
	row := r.pool.QueryRow(ctx, `INSERT INTO overnight_backtest_runs
		(id, status, phase, candidate_index, candidates, generated, errors, summary, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING started_at, updated_at`,
		run.ID, run.Status, run.Phase, run.CandidateIndex, candidates, generated, errs, summary, run.CompletedAt,
	)
	if err := row.Scan(&run.StartedAt, &run.UpdatedAt); err != nil {
		return fmt.Errorf("postgres: create overnight backtest run: %w", err)
	}
	return nil
}

func (r *OvernightBacktestRunRepo) Get(ctx context.Context, id uuid.UUID) (*domain.OvernightBacktestRun, error) {
	run, err := scanOvernightBacktestRun(r.pool.QueryRow(ctx, overnightBacktestSelectSQL+` WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("postgres: get overnight backtest run %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("postgres: get overnight backtest run: %w", err)
	}
	return run, nil
}

func (r *OvernightBacktestRunRepo) GetActive(ctx context.Context) (*domain.OvernightBacktestRun, error) {
	run, err := scanOvernightBacktestRun(r.pool.QueryRow(ctx, overnightBacktestSelectSQL+` WHERE status = 'running' ORDER BY updated_at DESC LIMIT 1`))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get active overnight backtest run: %w", err)
	}
	return run, nil
}

func (r *OvernightBacktestRunRepo) Update(ctx context.Context, run *domain.OvernightBacktestRun) error {
	candidates, generated, errs, summary, err := marshalOvernightBacktestRunJSON(*run)
	if err != nil {
		return err
	}
	row := r.pool.QueryRow(ctx, `UPDATE overnight_backtest_runs
		SET status = $2,
		    phase = $3,
		    candidate_index = $4,
		    candidates = $5,
		    generated = $6,
		    errors = $7,
		    summary = $8,
		    completed_at = $9,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING updated_at`,
		run.ID, run.Status, run.Phase, run.CandidateIndex, candidates, generated, errs, summary, run.CompletedAt,
	)
	if err := row.Scan(&run.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: update overnight backtest run %s: %w", run.ID, ErrNotFound)
		}
		return fmt.Errorf("postgres: update overnight backtest run: %w", err)
	}
	return nil
}

func (r *OvernightBacktestRunRepo) ListLatest(ctx context.Context, limit int) ([]domain.OvernightBacktestRun, error) {
	query, args := buildOvernightBacktestListLatestQuery(limit)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list overnight backtest runs: %w", err)
	}
	defer rows.Close()

	var runs []domain.OvernightBacktestRun
	for rows.Next() {
		run, err := scanOvernightBacktestRun(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan overnight backtest run: %w", err)
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

const overnightBacktestSelectSQL = `SELECT id, status, phase, candidate_index, candidates, generated, errors, summary, started_at, updated_at, completed_at
	FROM overnight_backtest_runs`

func buildOvernightBacktestListLatestQuery(limit int) (string, []any) {
	if limit <= 0 {
		limit = 20
	}
	return overnightBacktestSelectSQL + ` ORDER BY started_at DESC LIMIT $1`, []any{limit}
}

func marshalOvernightBacktestRunJSON(run domain.OvernightBacktestRun) ([]byte, []byte, []byte, []byte, error) {
	candidates, err := json.Marshal(run.Candidates)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight candidates: %w", err)
	}
	generated, err := json.Marshal(run.Generated)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight generated: %w", err)
	}
	errs, err := json.Marshal(run.Errors)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight errors: %w", err)
	}
	summary, err := json.Marshal(run.Summary)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("postgres: marshal overnight summary: %w", err)
	}
	return candidates, generated, errs, summary, nil
}

func scanOvernightBacktestRun(sc scanner) (*domain.OvernightBacktestRun, error) {
	var (
		run          domain.OvernightBacktestRun
		candidates   []byte
		generated    []byte
		errs         []byte
		summary      []byte
		completedAt  *time.Time
	)
	if err := sc.Scan(&run.ID, &run.Status, &run.Phase, &run.CandidateIndex, &candidates, &generated, &errs, &summary, &run.StartedAt, &run.UpdatedAt, &completedAt); err != nil {
		return nil, err
	}
	run.CompletedAt = completedAt
	if err := json.Unmarshal(candidates, &run.Candidates); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight candidates: %w", err)
	}
	if err := json.Unmarshal(generated, &run.Generated); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight generated: %w", err)
	}
	if err := json.Unmarshal(errs, &run.Errors); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight errors: %w", err)
	}
	if err := json.Unmarshal(summary, &run.Summary); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal overnight summary: %w", err)
	}
	return &run, nil
}
```

Important: add `"time"` to imports in this file because `scanOvernightBacktestRun` uses `*time.Time`.

- [ ] **Step 4: Add integration test**

Append to `internal/repository/postgres/overnight_backtest_run_test.go`:

```go
func TestOvernightBacktestRunRepoIntegration_CRUD(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPositionIntegrationPool(t, ctx)
	defer cleanup()
	ensureOvernightBacktestRunTable(t, ctx, pool)

	repo := NewOvernightBacktestRunRepo(pool)
	run := domain.NewOvernightBacktestRun()
	run.Candidates = []domain.OvernightBacktestCandidate{{Ticker: "MSFT", Close: 300}}

	if err := repo.Create(ctx, &run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Candidates[0].Ticker != "MSFT" {
		t.Fatalf("candidate ticker = %q, want MSFT", got.Candidates[0].Ticker)
	}

	active, err := repo.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive() error = %v", err)
	}
	if active.ID != run.ID {
		t.Fatalf("active ID = %s, want %s", active.ID, run.ID)
	}

	run.Phase = domain.OvernightBacktestPhaseGenerate
	run.CandidateIndex = 1
	run.Generated = []domain.OvernightBacktestGenerated{{Ticker: "MSFT"}}
	if err := repo.Update(ctx, &run); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get() updated error = %v", err)
	}
	if updated.Phase != domain.OvernightBacktestPhaseGenerate || updated.CandidateIndex != 1 {
		t.Fatalf("updated phase/index = %s/%d", updated.Phase, updated.CandidateIndex)
	}

	now := time.Now().UTC()
	run.Status = domain.OvernightBacktestStatusCompleted
	run.Phase = domain.OvernightBacktestPhaseDone
	run.CompletedAt = &now
	if err := repo.Update(ctx, &run); err != nil {
		t.Fatalf("complete Update() error = %v", err)
	}
	_, err = repo.GetActive(ctx)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetActive() error = %v, want ErrNotFound", err)
	}

	latest, err := repo.ListLatest(ctx, 5)
	if err != nil {
		t.Fatalf("ListLatest() error = %v", err)
	}
	if len(latest) != 1 || latest[0].ID != run.ID {
		t.Fatalf("latest = %#v, want completed run", latest)
	}
}

func ensureOvernightBacktestRunTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `CREATE TABLE overnight_backtest_runs (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		status TEXT NOT NULL,
		phase TEXT NOT NULL,
		candidate_index INTEGER NOT NULL DEFAULT 0,
		candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
		generated JSONB NOT NULL DEFAULT '[]'::jsonb,
		errors JSONB NOT NULL DEFAULT '[]'::jsonb,
		summary JSONB NOT NULL DEFAULT '{}'::jsonb,
		started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		completed_at TIMESTAMPTZ
	)`)
	if err != nil {
		t.Fatalf("create overnight_backtest_runs table: %v", err)
	}
}
```

- [ ] **Step 5: Fix imports and run repository tests**

Run:

```bash
gofmt -w internal/repository/postgres/overnight_backtest_run.go internal/repository/postgres/overnight_backtest_run_test.go
go test -count=1 ./internal/repository/postgres -run 'OvernightBacktest'
```

Expected: PASS.

---

## Task 4: Add discovery chunk helper seams

**Files:**
- Modify: `internal/discovery/orchestrator.go`
- Create/modify: `internal/discovery/orchestrator_test.go`

This task extracts small exported helpers from `RunDiscovery` without changing existing behavior.

- [ ] **Step 1: Add helper function tests**

If `internal/discovery/orchestrator_test.go` does not exist, create it. Add tests for converting between domain checkpoint candidates and existing `ScreenResult` values:

```go
package discovery

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestCheckpointCandidateRoundTrip(t *testing.T) {
	screened := []ScreenResult{{
		Ticker: "AAPL",
		Bars: []domain.OHLCV{{
			Timestamp: time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
			Open: 1, High: 2, Low: 1, Close: 2, Volume: 100,
		}},
		Indicators: []domain.Indicator{{Name: "rsi_14", Value: 55}},
		Close: 2,
		ADV: 100,
		ATR: 1,
	}}

	checkpoint := CheckpointCandidatesFromScreenResults(screened)
	got := ScreenResultsFromCheckpointCandidates(checkpoint)
	if len(got) != 1 || got[0].Ticker != "AAPL" || got[0].Indicators[0].Name != "rsi_14" {
		t.Fatalf("round trip failed: %#v", got)
	}
}
```

- [ ] **Step 2: Implement conversion helpers**

Append to `internal/discovery/orchestrator.go`:

```go
func CheckpointCandidatesFromScreenResults(in []ScreenResult) []domain.OvernightBacktestCandidate {
	out := make([]domain.OvernightBacktestCandidate, 0, len(in))
	for _, c := range in {
		out = append(out, domain.OvernightBacktestCandidate{
			Ticker: c.Ticker,
			Bars: c.Bars,
			Indicators: c.Indicators,
			Close: c.Close,
			ADV: c.ADV,
			ATR: c.ATR,
		})
	}
	return out
}

func ScreenResultsFromCheckpointCandidates(in []domain.OvernightBacktestCandidate) []ScreenResult {
	out := make([]ScreenResult, 0, len(in))
	for _, c := range in {
		out = append(out, ScreenResult{
			Ticker: c.Ticker,
			Bars: c.Bars,
			Indicators: c.Indicators,
			Close: c.Close,
			ADV: c.ADV,
			ATR: c.ATR,
		})
	}
	return out
}
```

- [ ] **Step 3: Run discovery tests**

Run:

```bash
gofmt -w internal/discovery/orchestrator.go internal/discovery/orchestrator_test.go
go test -count=1 ./internal/discovery
```

Expected: PASS.

---

## Task 5: Implement the chunk runner

**Files:**
- Create: `internal/automation/overnight_backtest_chunker.go`
- Create: `internal/automation/overnight_backtest_chunker_test.go`

- [ ] **Step 1: Write phase/budget tests**

Create `internal/automation/overnight_backtest_chunker_test.go`:

```go
package automation

import (
	"context"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type fakeOvernightProgressRepo struct {
	active *domain.OvernightBacktestRun
	created int
	updated int
}

func (f *fakeOvernightProgressRepo) Create(ctx context.Context, run *domain.OvernightBacktestRun) error {
	f.created++
	copy := *run
	f.active = &copy
	return nil
}

func (f *fakeOvernightProgressRepo) Get(ctx context.Context, id uuid.UUID) (*domain.OvernightBacktestRun, error) {
	if f.active != nil && f.active.ID == id {
		copy := *f.active
		return &copy, nil
	}
	return nil, repository.ErrNotFound
}

func (f *fakeOvernightProgressRepo) GetActive(ctx context.Context) (*domain.OvernightBacktestRun, error) {
	if f.active == nil || f.active.Status != domain.OvernightBacktestStatusRunning {
		return nil, repository.ErrNotFound
	}
	copy := *f.active
	return &copy, nil
}

func (f *fakeOvernightProgressRepo) Update(ctx context.Context, run *domain.OvernightBacktestRun) error {
	f.updated++
	copy := *run
	f.active = &copy
	return nil
}

func (f *fakeOvernightProgressRepo) ListLatest(ctx context.Context, limit int) ([]domain.OvernightBacktestRun, error) {
	if f.active == nil {
		return nil, nil
	}
	return []domain.OvernightBacktestRun{*f.active}, nil
}

func TestOvernightBacktestChunkerGenerateBudget(t *testing.T) {
	run := domain.NewOvernightBacktestRun()
	run.Phase = domain.OvernightBacktestPhaseGenerate
	run.Candidates = []domain.OvernightBacktestCandidate{{Ticker: "A"}, {Ticker: "B"}, {Ticker: "C"}}
	repo := &fakeOvernightProgressRepo{active: &run}

	chunker := overnightBacktestChunker{progress: repo, generatePerChunk: 2}
	processed := chunker.nextGenerateEnd(0, len(run.Candidates))
	if processed != 2 {
		t.Fatalf("nextGenerateEnd = %d, want 2", processed)
	}
}

func TestOvernightBacktestChunkerAdvancesToSweepAfterFinalGenerate(t *testing.T) {
	run := domain.NewOvernightBacktestRun()
	run.Phase = domain.OvernightBacktestPhaseGenerate
	run.CandidateIndex = 2
	run.Candidates = []domain.OvernightBacktestCandidate{{Ticker: "A"}, {Ticker: "B"}}

	chunker := overnightBacktestChunker{generatePerChunk: 2}
	chunker.advanceAfterGenerate(&run)
	if run.Phase != domain.OvernightBacktestPhaseSweepValidateDeploy {
		t.Fatalf("phase = %s, want sweep_validate_deploy", run.Phase)
	}
}
```

Add missing imports as needed. The test references `uuid.UUID`, so include `"github.com/google/uuid"`.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test -count=1 ./internal/automation -run 'OvernightBacktestChunker'
```

Expected: FAIL because `overnightBacktestChunker` does not exist.

- [ ] **Step 3: Implement chunker skeleton and budget helpers**

Create `internal/automation/overnight_backtest_chunker.go`:

```go
package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type overnightBacktestChunker struct {
	deps             OrchestratorDeps
	progress         repository.OvernightBacktestRunRepository
	logger           *slog.Logger
	generatePerChunk int
}

func newOvernightBacktestChunker(deps OrchestratorDeps, logger *slog.Logger) overnightBacktestChunker {
	if logger == nil {
		logger = slog.Default()
	}
	return overnightBacktestChunker{
		deps:             deps,
		progress:         deps.OvernightBacktestRuns,
		logger:           logger,
		generatePerChunk: overnightBacktestGeneratePerChunk,
	}
}

func (c overnightBacktestChunker) nextGenerateEnd(start, total int) int {
	limit := c.generatePerChunk
	if limit <= 0 {
		limit = overnightBacktestGeneratePerChunk
	}
	end := start + limit
	if end > total {
		end = total
	}
	return end
}

func (c overnightBacktestChunker) advanceAfterGenerate(run *domain.OvernightBacktestRun) {
	if run.CandidateIndex >= len(run.Candidates) {
		run.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
	}
}
```

- [ ] **Step 4: Run skeleton tests**

Run:

```bash
gofmt -w internal/automation/overnight_backtest_chunker.go internal/automation/overnight_backtest_chunker_test.go
go test -count=1 ./internal/automation -run 'OvernightBacktestChunker'
```

Expected: PASS for helper tests.

- [ ] **Step 5: Implement `RunChunk`**

Append to `internal/automation/overnight_backtest_chunker.go`:

```go
func (c overnightBacktestChunker) RunChunk(ctx context.Context) error {
	if c.progress == nil {
		return fmt.Errorf("overnight_backtest: progress repository not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, overnightBacktestChunkTimeout)
	defer cancel()

	run, err := c.progress.GetActive(ctx)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			fresh := domain.NewOvernightBacktestRun()
			if err := c.progress.Create(ctx, &fresh); err != nil {
				return err
			}
			run = &fresh
		} else {
			return err
		}
	}

	if time.Since(run.StartedAt) > overnightBacktestMaxRunAge && !run.StartedAt.IsZero() {
		run.Status = domain.OvernightBacktestStatusFailed
		run.Errors = append(run.Errors, "run exceeded max age")
		now := time.Now().UTC()
		run.CompletedAt = &now
		return c.progress.Update(ctx, run)
	}

	switch run.Phase {
	case domain.OvernightBacktestPhaseScreen:
		return c.runScreen(ctx, run)
	case domain.OvernightBacktestPhaseGenerate:
		return c.runGenerateChunk(ctx, run)
	case domain.OvernightBacktestPhaseSweepValidateDeploy:
		return c.runSweepValidateDeploy(ctx, run)
	case domain.OvernightBacktestPhaseDone:
		return nil
	default:
		return fmt.Errorf("overnight_backtest: unknown phase %q", run.Phase)
	}
}
```

- [ ] **Step 6: Implement screen chunk phase**

Append:

```go
func (c overnightBacktestChunker) runScreen(ctx context.Context, run *domain.OvernightBacktestRun) error {
	if c.deps.Universe == nil {
		run.Status = domain.OvernightBacktestStatusCompleted
		run.Phase = domain.OvernightBacktestPhaseDone
		now := time.Now().UTC()
		run.CompletedAt = &now
		return c.progress.Update(ctx, run)
	}
	if c.deps.DataService == nil {
		return fmt.Errorf("overnight_backtest: data service not configured")
	}

	watchlist, err := c.deps.Universe.GetWatchlist(ctx, overnightBacktestWatchlistLimit)
	if err != nil {
		return fmt.Errorf("overnight_backtest: get watchlist: %w", err)
	}
	tickers := make([]string, 0, len(watchlist))
	for _, item := range watchlist {
		tickers = append(tickers, item.Ticker)
	}

	now := time.Now()
	histFrom := now.AddDate(-5, 0, 0)
	if _, err := c.deps.DataService.DownloadHistoricalOHLCV(ctx, domain.MarketTypeStock, tickers, data.Timeframe1d, histFrom, now, true); err != nil {
		run.Errors = append(run.Errors, fmt.Sprintf("history download: %v", err))
	}

	candidates, err := discovery.Screen(ctx, c.deps.DataService, discovery.ScreenerConfig{
		Tickers:    tickers,
		MarketType: domain.MarketTypeStock,
	}, c.logger)
	if err != nil {
		return fmt.Errorf("overnight_backtest: screen: %w", err)
	}

	run.Candidates = discovery.CheckpointCandidatesFromScreenResults(candidates)
	run.Summary.Candidates = len(run.Candidates)
	run.Phase = domain.OvernightBacktestPhaseGenerate
	run.CandidateIndex = 0
	c.logger.Info("overnight_backtest: screened chunk run", slog.Int("candidates", len(run.Candidates)))
	return c.progress.Update(ctx, run)
}
```

- [ ] **Step 7: Implement generate chunk phase**

Append:

```go
func (c overnightBacktestChunker) runGenerateChunk(ctx context.Context, run *domain.OvernightBacktestRun) error {
	if c.deps.LLMProvider == nil {
		return fmt.Errorf("overnight_backtest: LLM provider not configured")
	}
	candidates := discovery.ScreenResultsFromCheckpointCandidates(run.Candidates)
	start := run.CandidateIndex
	end := c.nextGenerateEnd(start, len(candidates))
	genCfg := discovery.GeneratorConfig{
		Provider:   c.deps.LLMProvider,
		MaxRetries: overnightBacktestGenerationMaxRetries,
	}

	for i := start; i < end; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate := candidates[i]
		cfg, err := discovery.GenerateStrategy(ctx, genCfg, candidate, c.logger)
		if err != nil {
			run.Errors = append(run.Errors, fmt.Sprintf("generate %s: %v", candidate.Ticker, err))
			run.CandidateIndex = i + 1
			continue
		}
		run.Generated = append(run.Generated, domain.OvernightBacktestGenerated{
			Ticker: candidate.Ticker,
			Config: *cfg,
		})
		run.CandidateIndex = i + 1
	}

	run.Summary.Generated = len(run.Generated)
	c.advanceAfterGenerate(run)
	c.logger.Info("overnight_backtest: generated chunk",
		slog.Int("candidate_index", run.CandidateIndex),
		slog.Int("total_candidates", len(run.Candidates)),
		slog.Int("generated", len(run.Generated)),
	)
	return c.progress.Update(ctx, run)
}
```

- [ ] **Step 8: Implement sweep/validate/deploy phase**

Append:

```go
func (c overnightBacktestChunker) runSweepValidateDeploy(ctx context.Context, run *domain.OvernightBacktestRun) error {
	if c.deps.StrategyRepo == nil {
		return fmt.Errorf("overnight_backtest: strategy repository not configured")
	}

	candidatesByTicker := map[string]domain.OvernightBacktestCandidate{}
	for _, candidate := range run.Candidates {
		candidatesByTicker[candidate.Ticker] = candidate
	}

	type generated struct {
		candidate domain.OvernightBacktestCandidate
		config    rules.RulesEngineConfig
	}
	generatedConfigs := make([]generated, 0, len(run.Generated))
	for _, gen := range run.Generated {
		candidate, ok := candidatesByTicker[gen.Ticker]
		if !ok {
			run.Errors = append(run.Errors, fmt.Sprintf("missing candidate for generated ticker %s", gen.Ticker))
			continue
		}
		generatedConfigs = append(generatedConfigs, generated{candidate: candidate, config: gen.Config})
	}

	// Keep this phase identical to discovery.RunDiscovery steps 3-6. If this
	// grows too much, extract shared helpers from discovery.RunDiscovery instead
	// of maintaining divergent copies.
	result, err := c.runGeneratedDiscoveryTail(ctx, generatedConfigs)
	if err != nil {
		return err
	}
	run.Summary.Swept = result.Swept
	run.Summary.Validated = result.Validated
	run.Summary.Deployed = result.Deployed
	run.Errors = append(run.Errors, result.Errors...)
	run.Status = domain.OvernightBacktestStatusCompleted
	run.Phase = domain.OvernightBacktestPhaseDone
	now := time.Now().UTC()
	run.CompletedAt = &now
	return c.progress.Update(ctx, run)
}
```

Then implement `runGeneratedDiscoveryTail` by copying `discovery.RunDiscovery` steps 3 through 6 into this method, using:

```go
type overnightGeneratedConfig struct {
	candidate domain.OvernightBacktestCandidate
	config    rules.RulesEngineConfig
}
```

Keep behavior aligned with `internal/discovery/orchestrator.go:124-344`:
- call `c.deps.DataService.DownloadHistoricalOHLCV` for 5 years;
- call `discovery.RunSweep`;
- call `discovery.FilterAndRank`;
- call `discovery.ValidateOutOfSample`;
- create/reuse strategies with `discovery.CreateOrReusePaperStrategy`;
- create backtest configs only if `c.deps.BacktestConfigRepo` is later added to `OrchestratorDeps`; otherwise skip this optional behavior for now.

- [ ] **Step 9: Run automation tests**

Run:

```bash
gofmt -w internal/automation/overnight_backtest_chunker.go internal/automation/overnight_backtest_chunker_test.go
go test -count=1 ./internal/automation -run 'OvernightBacktest'
```

Expected: PASS.

---

## Task 6: Wire the chunker into automation deps and runtime

**Files:**
- Modify: `internal/automation/orchestrator.go`
- Modify: `internal/automation/jobs_overnight.go`
- Modify: `cmd/tradingagent/runtime.go`

- [ ] **Step 1: Add repository dependency**

Modify `internal/automation/orchestrator.go`, `OrchestratorDeps`:

```go
OvernightBacktestRuns repository.OvernightBacktestRunRepository // optional; enables chunked overnight_backtest
```

- [ ] **Step 2: Replace monolithic overnight backtest body**

Modify `internal/automation/jobs_overnight.go` so `overnightBacktest` becomes:

```go
func (o *JobOrchestrator) overnightBacktest(ctx context.Context) error {
	o.logger.Info("overnight_backtest: chunk starting")
	chunker := newOvernightBacktestChunker(o.deps, o.logger)
	if err := chunker.RunChunk(ctx); err != nil {
		return fmt.Errorf("overnight_backtest: chunk failed: %w", err)
	}
	o.logger.Info("overnight_backtest: chunk completed")
	return nil
}
```

Do not delete `overnightSweep`, `overnightGenerate`, `historyRefresh`, or `optionsDiscovery`.

- [ ] **Step 3: Update cron cadence**

Modify `overnightBacktestSpec` in `internal/automation/jobs_overnight.go`:

```go
overnightBacktestSpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "*/30 1-5 * * 2-6", SkipWeekends: false, SkipHolidays: false}
```

- [ ] **Step 4: Wire Postgres repo in runtime**

Modify `cmd/tradingagent/runtime.go`, after `backtestRunRepo := pgrepo.NewBacktestRunRepo(db.Pool)`:

```go
overnightBacktestRunRepo := pgrepo.NewOvernightBacktestRunRepo(db.Pool)
```

Then when constructing `automation.OrchestratorDeps`, include:

```go
OvernightBacktestRuns: overnightBacktestRunRepo,
```

- [ ] **Step 5: Run compile tests**

Run:

```bash
gofmt -w internal/automation/orchestrator.go internal/automation/jobs_overnight.go cmd/tradingagent/runtime.go
go test -run '^$' ./internal/automation ./cmd/tradingagent
```

Expected: PASS compile.

---

## Task 7: Add end-to-end chunk flow tests with stubs

**Files:**
- Modify: `internal/automation/overnight_backtest_chunker_test.go`

- [ ] **Step 1: Add fake LLM provider**

Append:

```go
type fakeChunkLLM struct{}

func (f fakeChunkLLM) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return &llm.CompletionResponse{Content: `{
		"version":1,
		"name":"chunk-test",
		"description":"chunk test",
		"entry":{"operator":"AND","conditions":[{"field":"rsi_14","op":"lt","value":45}]},
		"exit":{"operator":"AND","conditions":[{"field":"rsi_14","op":"gt","value":55}]},
		"position_sizing":{"method":"fixed_fraction","fraction_pct":0.1},
		"stop_loss":{"method":"fixed_pct","pct":0.05},
		"take_profit":{"method":"fixed_pct","pct":0.1},
		"filters":{"min_volume":100000,"min_atr":0.5}
	}`}, nil
}
```

Add import:

```go
"github.com/PatrickFanella/get-rich-quick/internal/llm"
```

- [ ] **Step 2: Add generate chunk integration-ish test**

Append:

```go
func TestOvernightBacktestChunkerRunGenerateChunkPersistsProgress(t *testing.T) {
	run := domain.NewOvernightBacktestRun()
	run.Phase = domain.OvernightBacktestPhaseGenerate
	run.Candidates = []domain.OvernightBacktestCandidate{
		{Ticker: "A", Bars: []domain.OHLCV{{Close: 10, Volume: 1000}}, Close: 10},
		{Ticker: "B", Bars: []domain.OHLCV{{Close: 20, Volume: 1000}}, Close: 20},
		{Ticker: "C", Bars: []domain.OHLCV{{Close: 30, Volume: 1000}}, Close: 30},
	}
	repo := &fakeOvernightProgressRepo{active: &run}
	chunker := overnightBacktestChunker{
		deps: OrchestratorDeps{LLMProvider: fakeChunkLLM{}},
		progress: repo,
		logger: slogDiscardLogger(),
		generatePerChunk: 2,
	}

	if err := chunker.runGenerateChunk(context.Background(), &run); err != nil {
		t.Fatalf("runGenerateChunk() error = %v", err)
	}
	if run.CandidateIndex != 2 {
		t.Fatalf("CandidateIndex = %d, want 2", run.CandidateIndex)
	}
	if len(run.Generated) != 2 {
		t.Fatalf("Generated = %d, want 2", len(run.Generated))
	}
	if run.Phase != domain.OvernightBacktestPhaseGenerate {
		t.Fatalf("Phase = %s, want generate", run.Phase)
	}
	if repo.updated == 0 {
		t.Fatal("expected repo update")
	}
}
```

Add import:

```go
"log/slog"
```

If `slogDiscardLogger()` is not available in package tests, use:

```go
slog.New(slog.NewTextHandler(io.Discard, nil))
```

and import `io`.

- [ ] **Step 3: Run tests**

Run:

```bash
gofmt -w internal/automation/overnight_backtest_chunker_test.go
go test -count=1 ./internal/automation -run 'OvernightBacktestChunker'
```

Expected: PASS.

---

## Task 8: Verify the whole backend

**Files:**
- No edits unless tests expose real issues.

- [ ] **Step 1: Run targeted packages**

Run:

```bash
go test -count=1 ./internal/domain ./internal/repository ./internal/repository/postgres ./internal/discovery ./internal/automation ./internal/scheduler
```

Expected: PASS.

- [ ] **Step 2: Run full short suite**

Run:

```bash
go test -short -count=1 ./...
```

Expected: PASS, except if the known `cmd/tradingagent/runtime_test.go` hardcoded schema-version string failures still exist from before this feature. If they fail, update those tests to use `pgrepo.RequiredSchemaVersion` in expected strings instead of literal version numbers.

- [ ] **Step 3: Optional local migration smoke**

Run if Postgres dev DB is available:

```bash
task migrate:up
docker compose restart app
docker compose logs --no-color app
```

Expected:
- app starts without schema mismatch;
- automation logs show `overnight_backtest` scheduled at `*/30 1-5 * * 2-6`;
- no `progress repository not configured` error.

---

## Task 9: Operational follow-up

**Files:**
- Optional modify: `docs/reference/architecture.md`

- [ ] **Step 1: Document chunk behavior**

Add a short section to `docs/reference/architecture.md`:

```markdown
### Chunked overnight backtest

The `overnight_backtest` automation job is resumable. It persists progress in
`overnight_backtest_runs` and advances through `screen`, `generate`,
`sweep_validate_deploy`, and `done` phases. The generation phase processes a
small fixed number of candidates per cron tick so local GPU-backed LLM inference
is released between chunks.
```

- [ ] **Step 2: Add admin SQL for inspection**

Keep this command in the PR/commit notes:

```sql
SELECT id, status, phase, candidate_index, jsonb_array_length(candidates) AS candidates,
       jsonb_array_length(generated) AS generated, started_at, updated_at, completed_at
FROM overnight_backtest_runs
ORDER BY started_at DESC
LIMIT 10;
```

---

## Self-Review Notes

- Spec coverage: The plan breaks the LLM generation into bounded chunks, persists cursor state, avoids a single 4-hour GPU monopolizer, and resumes across cron ticks.
- Placeholder scan: No task uses “TBD” or asks for unspecified tests; the only deliberate copy instruction is for `RunDiscovery` tail behavior and includes exact source range and required calls.
- Type consistency: Domain constants, repository interface names, and dependency names match across tasks.

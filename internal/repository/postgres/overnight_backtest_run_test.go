package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestMarshalOvernightBacktestJSONSlices(t *testing.T) {
	run := domain.NewOvernightBacktestRun()
	run.Candidates = []domain.OvernightBacktestCandidate{{Ticker: "AAPL", Close: 200}}
	run.Generated = []domain.OvernightBacktestGenerated{{Ticker: "AAPL", Config: json.RawMessage(`{}`), Evidence: json.RawMessage(`{"attempts":[]}`)}}
	run.Errors = []string{"sample error"}
	run.Summary = domain.OvernightBacktestSummary{Candidates: 1, Generated: 1}
	_, _, _, _, err := marshalOvernightBacktestRunJSON(run)
	if err != nil {
		t.Fatalf("marshalOvernightBacktestRunJSON() error = %v", err)
	}
}

func TestBuildOvernightBacktestListLatestLimit(t *testing.T) {
	query, args := buildOvernightBacktestListLatestQuery(0)
	if len(args) != 1 || args[0] != 20 {
		t.Fatalf("args = %#v, want default limit 20", args)
	}
	assertContains(t, query, "FROM overnight_backtest_runs")
	assertContains(t, query, "ORDER BY started_at DESC, id DESC")
	assertContains(t, query, "LIMIT $1")
}

func TestOvernightBacktestRunRepoIntegration_CRUD(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOvernightBacktestIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewOvernightBacktestRunRepo(pool)
	run := domain.NewOvernightBacktestRun()
	run.Candidates = []domain.OvernightBacktestCandidate{{Ticker: "MSFT", Close: 300}}
	run.Generated = []domain.OvernightBacktestGenerated{{Ticker: "MSFT", Config: json.RawMessage(`{}`), Evidence: json.RawMessage(`{"attempts":[]}`)}}
	if err := repo.Create(ctx, &run); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if run.StartedAt.IsZero() || run.UpdatedAt.IsZero() {
		t.Fatalf("created timestamps should be populated: started=%v updated=%v", run.StartedAt, run.UpdatedAt)
	}
	if time.Since(run.StartedAt) > time.Minute {
		t.Fatalf("StartedAt = %v, want recent timestamp", run.StartedAt)
	}
	got, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Candidates[0].Ticker != "MSFT" {
		t.Fatalf("candidate ticker = %q, want MSFT", got.Candidates[0].Ticker)
	}
	var evidence struct {
		Attempts []json.RawMessage `json:"attempts"`
	}
	if err := json.Unmarshal(got.Generated[0].Evidence, &evidence); err != nil || evidence.Attempts == nil || len(evidence.Attempts) != 0 {
		t.Fatalf("generated evidence = %s, err=%v", got.Generated[0].Evidence, err)
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
	run.Summary = domain.OvernightBacktestSummary{Candidates: 2, Generated: 2, Swept: 2, Validated: 2, Deployed: 1, Created: 1, Reused: 1}
	if err := repo.SaveIfRunning(ctx, &run); err != nil {
		t.Fatalf("SaveIfRunning() error = %v", err)
	}
	updated, err := repo.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get() updated error = %v", err)
	}
	if updated.Phase != domain.OvernightBacktestPhaseGenerate || updated.CandidateIndex != 1 {
		t.Fatalf("updated phase/index = %s/%d", updated.Phase, updated.CandidateIndex)
	}
	if updated.Summary.Created != 1 || updated.Summary.Reused != 1 || updated.Summary.Deployed != 1 {
		t.Fatalf("updated deployment summary = %+v", updated.Summary)
	}
	now := time.Now().UTC()
	run.Status = domain.OvernightBacktestStatusCompleted
	run.Phase = domain.OvernightBacktestPhaseDone
	run.CompletedAt = &now
	if err := repo.SaveIfRunning(ctx, &run); err != nil {
		t.Fatalf("complete SaveIfRunning() error = %v", err)
	}
	if err := repo.SaveIfRunning(ctx, &run); !errors.Is(err, repository.ErrOvernightBacktestRunClosed) {
		t.Fatalf("second terminal SaveIfRunning() error = %v, want closed", err)
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

func TestOvernightBacktestRunRepoIntegration_ReconcileActiveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOvernightBacktestIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewOvernightBacktestRunRepo(pool)
	active := domain.NewOvernightBacktestRun()
	active.Errors = []string{"existing"}
	if err := repo.Create(ctx, &active); err != nil {
		t.Fatal(err)
	}
	completed := domain.NewOvernightBacktestRun()
	completed.Status = domain.OvernightBacktestStatusCompleted
	completed.Phase = domain.OvernightBacktestPhaseDone
	completedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	completed.CompletedAt = &completedAt
	if err := repo.Create(ctx, &completed); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	count, err := repo.ReconcileActive(ctx, now, DiscoveryDeploymentUnavailableReason)
	if err != nil || count != 1 {
		t.Fatalf("ReconcileActive() = %d, %v", count, err)
	}
	got, err := repo.Get(ctx, active.ID)
	if err != nil || got.Status != domain.OvernightBacktestStatusFailed || got.Phase != domain.OvernightBacktestPhaseDone || got.CompletedAt == nil || !got.CompletedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("reconciled run = %+v, err = %v", got, err)
	}
	if len(got.Errors) != 2 || got.Errors[0] != "existing" || got.Errors[1] != DiscoveryDeploymentUnavailableReason {
		t.Fatalf("reconciled errors = %#v", got.Errors)
	}
	untouched, err := repo.Get(ctx, completed.ID)
	if err != nil || untouched.Status != domain.OvernightBacktestStatusCompleted || untouched.CompletedAt == nil || !untouched.CompletedAt.Equal(completedAt) {
		t.Fatalf("terminal run mutated = %+v, err = %v", untouched, err)
	}
	count, err = repo.ReconcileActive(ctx, now.Add(time.Minute), DiscoveryDeploymentUnavailableReason)
	if err != nil || count != 0 {
		t.Fatalf("idempotent ReconcileActive() = %d, %v", count, err)
	}
	active.Phase = domain.OvernightBacktestPhaseGenerate
	if err := repo.SaveIfRunning(ctx, &active); !errors.Is(err, repository.ErrOvernightBacktestRunClosed) {
		t.Fatalf("stale SaveIfRunning() error = %v, want closed", err)
	}
}

func TestOvernightBacktestRunRepoIntegration_CommitAndReconcileTerminalRaceOrders(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOvernightBacktestIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewOvernightBacktestRunRepo(pool)
	completedAt := time.Now().UTC().Truncate(time.Microsecond)

	reconcileWins := domain.NewOvernightBacktestRun()
	reconcileWins.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
	if err := repo.Create(ctx, &reconcileWins); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.ReconcileActive(ctx, completedAt, "unavailable"); err != nil || count != 1 {
		t.Fatalf("reconcile first = %d, %v", count, err)
	}
	if _, _, err := repo.CommitIfRunning(ctx, reconcileWins.ID, completedAt.Add(time.Second), domain.OvernightBacktestSummary{}, []domain.Strategy{preparedOvernightStrategy("AAPL", "one")}); !errors.Is(err, repository.ErrOvernightBacktestRunClosed) {
		t.Fatalf("commit after reconcile error = %v, want closed", err)
	}

	commitWins := domain.NewOvernightBacktestRun()
	commitWins.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
	if err := repo.Create(ctx, &commitWins); err != nil {
		t.Fatal(err)
	}
	summary, persistedAt, err := repo.CommitIfRunning(ctx, commitWins.ID, completedAt, domain.OvernightBacktestSummary{Validated: 1}, []domain.Strategy{preparedOvernightStrategy("MSFT", "two")})
	if err != nil || summary.Created != 1 || summary.Reused != 0 || summary.Deployed != 1 || !persistedAt.Equal(completedAt) {
		t.Fatalf("commit first = %+v, %v, %v", summary, persistedAt, err)
	}
	if count, err := repo.ReconcileActive(ctx, completedAt.Add(time.Minute), "unavailable"); err != nil || count != 0 {
		t.Fatalf("reconcile after commit = %d, %v", count, err)
	}
}

func TestOvernightBacktestRunRepoIntegration_CommitRollsBackAndReuses(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOvernightBacktestIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewOvernightBacktestRunRepo(pool)

	failed := domain.NewOvernightBacktestRun()
	failed.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
	if err := repo.Create(ctx, &failed); err != nil {
		t.Fatal(err)
	}
	bad := preparedOvernightStrategy("BBB", "bad")
	bad.Config = json.RawMessage(`{`)
	if _, _, err := repo.CommitIfRunning(ctx, failed.ID, time.Now(), domain.OvernightBacktestSummary{}, []domain.Strategy{preparedOvernightStrategy("AAA", "good"), bad}); err == nil {
		t.Fatal("CommitIfRunning() error = nil, want second insert failure")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM strategies`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("strategies after rollback = %d, %v", count, err)
	}

	first := domain.NewOvernightBacktestRun()
	first.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
	if err := repo.Create(ctx, &first); err != nil {
		t.Fatal(err)
	}
	strategy := preparedOvernightStrategy("AAPL", "reuse")
	if _, _, err := repo.CommitIfRunning(ctx, first.ID, time.Now(), domain.OvernightBacktestSummary{}, []domain.Strategy{strategy}); err != nil {
		t.Fatal(err)
	}
	second := domain.NewOvernightBacktestRun()
	second.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
	if err := repo.Create(ctx, &second); err != nil {
		t.Fatal(err)
	}
	summary, _, err := repo.CommitIfRunning(ctx, second.ID, time.Now(), domain.OvernightBacktestSummary{}, []domain.Strategy{strategy})
	if err != nil || summary.Created != 0 || summary.Reused != 1 || summary.Deployed != 1 {
		t.Fatalf("reuse summary = %+v, err = %v", summary, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM strategies`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("strategies after reuse = %d, %v", count, err)
	}
}

func preparedOvernightStrategy(ticker, suffix string) domain.Strategy {
	return domain.Strategy{ID: uuid.New(), Name: "discovery: " + ticker + " " + suffix, Ticker: ticker, MarketType: domain.MarketTypeStock, IsPaper: true, Status: domain.StrategyStatusInactive, Config: json.RawMessage(`{"research_lifecycle":{"stage":"idea"}}`)}
}

func newOvernightBacktestIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	connString := os.Getenv("DB_URL")
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		t.Skip("skipping integration test: DB_URL or DATABASE_URL is not set")
	}
	adminPool, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatalf("failed to create admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto`); err != nil {
		adminPool.Close()
		t.Fatalf("failed to ensure pgcrypto extension: %v", err)
	}
	schemaName := "integration_overnight_backtest_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	if _, err := adminPool.Exec(ctx, `CREATE SCHEMA `+pqQuoteIdent(schemaName)); err != nil {
		adminPool.Close()
		t.Fatalf("failed to create test schema: %v", err)
	}
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to parse pool config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to create test pool: %v", err)
	}
	ddl := `CREATE TABLE overnight_backtest_runs (
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
	CREATE TABLE strategies (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT NOT NULL, description TEXT,
		ticker TEXT NOT NULL, market_type TEXT NOT NULL, schedule_cron TEXT, config JSONB NOT NULL DEFAULT '{}',
		status TEXT NOT NULL DEFAULT 'inactive', skip_next_run BOOLEAN NOT NULL DEFAULT false,
		is_paper BOOLEAN NOT NULL DEFAULT true, is_active BOOLEAN NOT NULL DEFAULT false,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE UNIQUE INDEX idx_strategies_discovery_unique ON strategies (ticker, market_type, is_paper, name)
		WHERE is_paper = true AND (name LIKE 'discovery:%' OR name LIKE 'options:%')`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to apply test schema DDL: %v", err)
	}
	return pool, func() {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
	}
}

func pqQuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

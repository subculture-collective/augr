package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

func TestSortPreparedStrategiesForReuseUsesSameOrderForOppositeInputs(t *testing.T) {
	ascending := []domain.Strategy{
		preparedOvernightStrategy("AAA", "z"),
		preparedOvernightStrategy("AAA", "a"),
		preparedOvernightStrategy("ZZZ", "a"),
	}
	descending := []domain.Strategy{ascending[2], ascending[1], ascending[0]}
	sortPreparedStrategiesForReuse(ascending)
	sortPreparedStrategiesForReuse(descending)
	for i := range ascending {
		if strategyReuseKey(ascending[i]) != strategyReuseKey(descending[i]) {
			t.Fatalf("opposite inputs lock in different order: %#v / %#v", ascending, descending)
		}
	}
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

func TestOvernightBacktestRunRepoIntegration_CommitBindsPersistedExecutionVersion(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOvernightBacktestIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewOvernightBacktestRunRepo(pool)
	run := domain.NewOvernightBacktestRun()
	run.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
	if err := repo.Create(ctx, &run); err != nil {
		t.Fatal(err)
	}

	strategy := preparedOvernightStrategy("AAPL", "versioned")
	if _, _, err := repo.CommitIfRunning(ctx, run.ID, time.Now(), domain.OvernightBacktestSummary{}, []domain.Strategy{strategy}); err != nil {
		t.Fatal(err)
	}

	var versionID uuid.UUID
	var canonicalConfig string
	if err := pool.QueryRow(ctx, `SELECT s.execution_strategy_version_id,convert_from(v.config_bytes,'UTF8')
		FROM strategies s JOIN strategy_versions v ON v.id=s.execution_strategy_version_id WHERE s.id=$1`, strategy.ID).
		Scan(&versionID, &canonicalConfig); err != nil {
		t.Fatal(err)
	}
	if versionID == uuid.Nil || canonicalConfig != `{"research_lifecycle":{"stage":"idea"}}` {
		t.Fatalf("execution version = %s, config = %s", versionID, canonicalConfig)
	}
}

func TestOvernightBacktestRunRepoIntegration_ReuseRejectsInvalidExecutionBinding(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOvernightBacktestIntegrationPool(t, ctx)
	defer cleanup()
	runRepo := NewOvernightBacktestRunRepo(pool)
	strategyRepo := NewStrategyRepo(pool)

	tests := []struct {
		name   string
		mutate func(*testing.T, domain.Strategy)
		want   string
	}{
		{name: "missing", want: "missing", mutate: func(t *testing.T, strategy domain.Strategy) {
			_, err := pool.Exec(ctx, `UPDATE strategies SET execution_strategy_version_id=NULL WHERE id=$1`, strategy.ID)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "foreign family", want: "family mismatch", mutate: func(t *testing.T, strategy domain.Strategy) {
			foreign := preparedOvernightStrategy("FOREIGN", "foreign")
			foreign.ID = uuid.New()
			foreignVersionID, err := strategyRepo.CreateWithExecutionVersion(ctx, &foreign)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE strategies SET execution_strategy_version_id=$1 WHERE id=$2`, foreignVersionID, strategy.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "stale", want: "stale", mutate: func(t *testing.T, strategy domain.Strategy) {
			if _, err := pool.Exec(ctx, `UPDATE strategies SET config='{"research_lifecycle":{"stage":"candidate"}}'::jsonb WHERE id=$1`, strategy.ID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			strategy := preparedOvernightStrategy(fmt.Sprintf("BAD%d", i), tc.name)
			if _, err := strategyRepo.CreateWithExecutionVersion(ctx, &strategy); err != nil {
				t.Fatal(err)
			}
			originalBinding := *strategy.ExecutionStrategyVersionID
			tc.mutate(t, strategy)
			var bindingBefore *uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT execution_strategy_version_id FROM strategies WHERE id=$1`, strategy.ID).Scan(&bindingBefore); err != nil {
				t.Fatal(err)
			}

			run := domain.NewOvernightBacktestRun()
			run.Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
			if err := runRepo.Create(ctx, &run); err != nil {
				t.Fatal(err)
			}
			_, _, err := runRepo.CommitIfRunning(ctx, run.ID, time.Now(), domain.OvernightBacktestSummary{}, []domain.Strategy{strategy})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CommitIfRunning() error = %v, want %q", err, tc.want)
			}
			var bindingAfter *uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT execution_strategy_version_id FROM strategies WHERE id=$1`, strategy.ID).Scan(&bindingAfter); err != nil {
				t.Fatal(err)
			}
			if (bindingBefore == nil) != (bindingAfter == nil) || bindingBefore != nil && *bindingBefore != *bindingAfter {
				t.Fatalf("binding repaired from %v to %v; original valid binding was %s", bindingBefore, bindingAfter, originalBinding)
			}
		})
	}
}

func TestOvernightBacktestRunRepoIntegration_ConcurrentReusePreservesBinding(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOvernightBacktestIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewOvernightBacktestRunRepo(pool)
	strategies := []domain.Strategy{preparedOvernightStrategy("RACE", "first"), preparedOvernightStrategy("RACE", "second")}
	for i := range strategies {
		strategies[i].MarketType = domain.MarketTypePolymarket
	}
	runs := []domain.OvernightBacktestRun{domain.NewOvernightBacktestRun(), domain.NewOvernightBacktestRun()}
	for i := range runs {
		runs[i].Phase = domain.OvernightBacktestPhaseSweepValidateDeploy
		if err := repo.Create(ctx, &runs[i]); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(runs))
	for i := range runs {
		wg.Add(1)
		go func(runID uuid.UUID, strategy domain.Strategy) {
			defer wg.Done()
			_, _, err := repo.CommitIfRunning(ctx, runID, time.Now(), domain.OvernightBacktestSummary{}, []domain.Strategy{strategy})
			errCh <- err
		}(runs[i].ID, strategies[i])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var strategyCount, boundCount int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(execution_strategy_version_id) FROM strategies WHERE ticker='RACE'`).Scan(&strategyCount, &boundCount); err != nil {
		t.Fatal(err)
	}
	if strategyCount != 1 || boundCount != 1 {
		t.Fatalf("strategies/bound = %d/%d, want 1/1", strategyCount, boundCount)
	}
}

func preparedOvernightStrategy(ticker, suffix string) domain.Strategy {
	return domain.Strategy{ID: uuid.New(), Name: "discovery: " + ticker + " " + suffix, Ticker: ticker, MarketType: domain.MarketTypeStock, IsPaper: true, Status: domain.StrategyStatusInactive, Config: json.RawMessage(`{"research_lifecycle":{"stage":"idea"}}`)}
}

func newOvernightBacktestIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	return newStrategyIntegrationPool(t, ctx)
}

func pqQuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

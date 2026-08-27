package automation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
	"github.com/google/uuid"
)

func TestEasternDayStartUTCUsesTradingDayAcrossUTCMidnight(t *testing.T) {
	got := easternDayStartUTC(time.Date(2026, time.August, 6, 0, 30, 0, 0, time.UTC))
	want := time.Date(2026, time.August, 5, 4, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("easternDayStartUTC() = %s, want %s", got, want)
	}
}

func TestPostMarketCompletionErrorsExposePartialCoverage(t *testing.T) {
	t.Parallel()

	if err := strategyResweepCompletionError(map[string]int{"supported": 107, "swept": 105, "failed": 2, "stale": 2}); err == nil || !IsDegraded(err) || !strings.Contains(err.Error(), "coverage_bps=9813") || !strings.Contains(err.Error(), "stale=2") {
		t.Fatalf("strategyResweepCompletionError(live) = %v, want detailed degraded result", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 100, "swept": 79}); err == nil || IsDegraded(err) {
		t.Fatalf("strategyResweepCompletionError(79%%) = %v, want true error", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 100, "swept": 80, "failed": 20, "config_failed": 20}); err == nil || !IsDegraded(err) {
		t.Fatalf("strategyResweepCompletionError(80%%) = %v, want degraded", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 5, "failed": 5, "config_failed": 5}); err == nil || IsDegraded(err) {
		t.Fatalf("strategyResweepCompletionError(all invalid) = %v, want true error", err)
	}
	if err := strategyResweepCompletionError(map[string]int{"supported": 100, "swept": 100}); err != nil {
		t.Fatalf("strategyResweepCompletionError(complete) = %v, want nil", err)
	}

	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 24, "chains": 24}); err == nil || IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(24%%) = %v, want true error", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 25, "chains": 20}); err == nil || !IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(exact floors) = %v, want degraded", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 27, "chains": 22, "price_empty": 73, "fetch_failed": 5}); err == nil || !IsDegraded(err) || !strings.Contains(err.Error(), "optionable_coverage_bps=2700") || !strings.Contains(err.Error(), "chain_coverage_bps=8148") {
		t.Fatalf("optionsScanCompletionError(live) = %v, want detailed degraded result", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 25, "chains": 19}); err == nil || IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(under chain floor) = %v, want true error", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 100, "chains": 100, "persist_failed": 1}); err == nil || IsDegraded(err) {
		t.Fatalf("optionsScanCompletionError(persistence) = %v, want true error", err)
	}
	if err := optionsScanCompletionError(map[string]int{"universe": 100, "optionable": 100, "chains": 100}); err != nil {
		t.Fatalf("optionsScanCompletionError(complete) = %v, want nil", err)
	}
}

func TestDailyReviewCompletionErrorPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		summary      map[string]int
		wantDegraded bool
		wantTrueErr  bool
		wantMessage  string
	}{
		{name: "failed degrades", summary: map[string]int{"failed": 2}, wantDegraded: true, wantMessage: "failed=2"},
		{name: "running snapshot degrades", summary: map[string]int{"running": 1}, wantDegraded: true, wantMessage: "running=1"},
		{name: "cancelled degrades", summary: map[string]int{"cancelled": 1}, wantDegraded: true, wantMessage: "cancelled=1"},
		{name: "no run degrades", summary: map[string]int{"strategies_without_runs": 1}, wantDegraded: true, wantMessage: "strategies_without_runs=1"},
		{name: "query errors take true error precedence", summary: map[string]int{"query_errors": 1, "failed": 2}, wantTrueErr: true, wantMessage: "query_errors=1"},
		{name: "completed without signal takes true error precedence", summary: map[string]int{"completed_without_signal": 1, "running": 1}, wantTrueErr: true, wantMessage: "completed_without_signal=1"},
		{
			name:         "mixed snapshot findings degrade",
			summary:      map[string]int{"failed": 2, "running": 1},
			wantDegraded: true,
			wantMessage:  "failed=2 running=1",
		},
		{name: "zero succeeds", summary: map[string]int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := dailyReviewCompletionError(tt.summary)
			if tt.wantDegraded != IsDegraded(err) {
				t.Fatalf("dailyReviewCompletionError(%v) = %v, want degraded %v", tt.summary, err, tt.wantDegraded)
			}
			if tt.wantTrueErr != (err != nil && !IsDegraded(err)) {
				t.Fatalf("dailyReviewCompletionError(%v) = %v, want true error %v", tt.summary, err, tt.wantTrueErr)
			}
			if tt.wantMessage != "" && !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("dailyReviewCompletionError(%v) = %q, want substring %q", tt.summary, err, tt.wantMessage)
			}
		})
	}
}

func TestDailyReviewUsesFixedAsOfAndReportsLiveScope(t *testing.T) {
	asOf := time.Date(2026, time.August, 6, 20, 30, 0, 0, time.UTC)
	strategyA, strategyB, strategyC := uuid.New(), uuid.New(), uuid.New()
	runRepo := &dailyReviewRunRepo{runs: map[uuid.UUID][]domain.PipelineRun{
		strategyA: {
			{StrategyID: strategyA, StartedAt: asOf.Add(-time.Hour), Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy},
			{StrategyID: strategyA, StartedAt: asOf.Add(time.Second), Status: domain.PipelineStatusFailed},
		},
		strategyB: {
			{StrategyID: strategyB, StartedAt: asOf.Add(-3 * time.Hour), Status: domain.PipelineStatusFailed},
			{StrategyID: strategyB, StartedAt: asOf.Add(-2 * time.Hour), Status: domain.PipelineStatusRunning},
			{StrategyID: strategyB, StartedAt: asOf.Add(-time.Hour), Status: domain.PipelineStatusCancelled},
		},
	}}
	metricSink := &dailyReviewMetrics{stubAutomationMetrics: &stubAutomationMetrics{}}
	orch := NewJobOrchestrator(OrchestratorDeps{
		StrategyRepo: &kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{ID: strategyA, Name: "a", Ticker: "AAA", Status: "active"},
			{ID: strategyB, Name: "b", Ticker: "BBB", Status: "active"},
			{ID: strategyC, Name: "c", Ticker: "CCC", Status: "active"},
		}},
		RunRepo: runRepo,
	})
	nowCalls := 0
	orch.now = func() time.Time {
		nowCalls++
		return asOf.Add(time.Duration(nowCalls-1) * time.Hour)
	}
	orch.WithJobMetrics(metricSink)
	orch.Register("daily_review", "test", dailyReviewSpec, orch.dailyReview)

	err := orch.dailyReview(context.Background())
	if err == nil || !IsDegraded(err) {
		t.Fatalf("dailyReview() = %v, want degraded", err)
	}
	if nowCalls != 1 {
		t.Fatalf("now calls = %d, want 1", nowCalls)
	}
	dayStart := time.Date(2026, time.August, 6, 4, 0, 0, 0, time.UTC)
	for i, filter := range runRepo.filters {
		if filter.StartedAfter == nil || !filter.StartedAfter.Equal(dayStart) {
			t.Fatalf("filter %d StartedAfter = %v, want %v", i, filter.StartedAfter, dayStart)
		}
		if filter.StartedBefore == nil || !filter.StartedBefore.Equal(asOf) {
			t.Fatalf("filter %d StartedBefore = %v, want %v", i, filter.StartedBefore, asOf)
		}
	}
	summary := singleJobStatus(t, orch, "daily_review").LastSummary
	want := map[string]int{
		"generated_at_unix": int(asOf.Unix()), "scope_active_strategies": 1,
		"strategies": 3, "strategies_without_runs": 1, "runs": 4,
		"completed": 1, "buy": 1, "failed": 1, "running": 1, "cancelled": 1,
	}
	for key, value := range want {
		if summary[key] != value {
			t.Fatalf("summary[%q] = %d, want %d (summary=%v)", key, summary[key], value, summary)
		}
	}
	if metricSink.degraded != 1 {
		t.Fatalf("degraded metrics = %d, want 1", metricSink.degraded)
	}
}

func TestDailyReviewNoRunDegradedResetsFailureStreakAndStoresDetail(t *testing.T) {
	strategyID := uuid.New()
	orch := NewJobOrchestrator(OrchestratorDeps{
		StrategyRepo: &kalshiStrategyRepoStub{strategies: []domain.Strategy{{ID: strategyID, Name: "idle", Ticker: "IDLE", Status: "active"}}},
		RunRepo:      &dailyReviewRunRepo{runs: map[uuid.UUID][]domain.PipelineRun{}},
	})
	orch.now = func() time.Time { return time.Date(2026, time.August, 6, 20, 30, 0, 0, time.UTC) }
	orch.Register("daily_review", "test", dailyReviewSpec, orch.dailyReview)
	job := orch.jobs["daily_review"]
	job.ConsecutiveFailures = 2
	job.LastError = "prior failure"

	orch.runDirect(job)

	status := singleJobStatus(t, orch, "daily_review")
	if status.LastResult != "degraded" || status.ConsecutiveFailures != 0 || status.LastError != "" {
		t.Fatalf("daily review status = %+v, want degraded with reset failure state", status)
	}
	if !strings.Contains(status.LastDetail, "strategies_without_runs=1") {
		t.Fatalf("LastDetail = %q, want no-run finding", status.LastDetail)
	}
	if status.LastSummary["strategies_without_runs"] != 1 || status.LastSummary["runs"] != 0 {
		t.Fatalf("LastSummary = %v, want no fabricated runs", status.LastSummary)
	}
}

type dailyReviewMetrics struct {
	*stubAutomationMetrics
	degraded int
}

func (m *dailyReviewMetrics) RecordAutomationJobDegraded(string) { m.degraded++ }

type dailyReviewRunRepo struct {
	runs    map[uuid.UUID][]domain.PipelineRun
	filters []repository.PipelineRunFilter
	err     error
}

func (r *dailyReviewRunRepo) Create(context.Context, *domain.PipelineRun) error { return nil }
func (r *dailyReviewRunRepo) GetByID(context.Context, uuid.UUID) (*domain.PipelineRun, error) {
	return nil, repository.ErrNotFound
}

func (r *dailyReviewRunRepo) Get(context.Context, uuid.UUID, time.Time) (*domain.PipelineRun, error) {
	return nil, repository.ErrNotFound
}

func (r *dailyReviewRunRepo) Count(_ context.Context, filter repository.PipelineRunFilter) (int, error) {
	r.filters = append(r.filters, filter)
	if r.err != nil {
		return 0, r.err
	}
	return len(r.filtered(filter)), nil
}

func (r *dailyReviewRunRepo) List(_ context.Context, filter repository.PipelineRunFilter, limit, offset int) ([]domain.PipelineRun, error) {
	r.filters = append(r.filters, filter)
	if r.err != nil {
		return nil, r.err
	}
	runs := r.filtered(filter)
	if offset >= len(runs) {
		return nil, nil
	}
	end := min(offset+limit, len(runs))
	return append([]domain.PipelineRun(nil), runs[offset:end]...), nil
}

func (r *dailyReviewRunRepo) Finalize(context.Context, uuid.UUID, time.Time, repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	return repository.PipelineRunFinalizationReceipt{}, errors.New("not implemented")
}

func (r *dailyReviewRunRepo) RefineCompletedSignal(context.Context, uuid.UUID, time.Time, domain.PipelineSignal, domain.PipelineSignal) (repository.PipelineRunFinalizationReceipt, error) {
	return repository.PipelineRunFinalizationReceipt{}, errors.New("not implemented")
}

func (r *dailyReviewRunRepo) filtered(filter repository.PipelineRunFilter) []domain.PipelineRun {
	if filter.StrategyID == nil {
		return nil
	}
	var filtered []domain.PipelineRun
	for _, run := range r.runs[*filter.StrategyID] {
		if filter.StartedAfter != nil && run.StartedAt.Before(*filter.StartedAfter) {
			continue
		}
		if filter.StartedBefore != nil && run.StartedAt.After(*filter.StartedBefore) {
			continue
		}
		filtered = append(filtered, run)
	}
	return filtered
}

func TestClassifyResweepScoresSeparatesUnqualifiedSentinelsFromInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []discovery.SweepResult
		state   string
		wantErr bool
	}{
		{
			name:    "comparable",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}, {Label: "base", Score: 1}},
			state:   "comparable",
		},
		{
			name:    "base unqualified",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}, {Label: "base", Score: math.Inf(-1)}},
			state:   "base_unqualified",
		},
		{
			name:    "all unqualified",
			results: []discovery.SweepResult{{Label: "base", Score: math.Inf(-1)}, {Label: "variant_1", Score: math.Inf(-1)}},
			state:   "all_unqualified",
		},
		{
			name:    "missing base",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}},
			state:   "missing_base",
			wantErr: true,
		},
		{
			name:    "nan base",
			results: []discovery.SweepResult{{Label: "variant_1", Score: 2}, {Label: "base", Score: math.NaN()}},
			state:   "invalid_scores",
			wantErr: true,
		},
		{
			name:    "positive infinite best",
			results: []discovery.SweepResult{{Label: "variant_1", Score: math.Inf(1)}, {Label: "base", Score: 1}},
			state:   "invalid_scores",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, state, err := classifyResweepScores(tt.results)
			if state != tt.state {
				t.Fatalf("state = %q, want %q", state, tt.state)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOptionsScanTickersNormalizesDeduplicatesAndCapsWatchlist(t *testing.T) {
	t.Parallel()

	watchlist := []universe.TrackedTicker{{Ticker: " aapl "}, {Ticker: "AAPL"}, {Ticker: ""}}
	for i := 0; i < optionsScanWatchlistLimit+5; i++ {
		watchlist = append(watchlist, universe.TrackedTicker{Ticker: fmt.Sprintf("t%03d", i)})
	}

	got := optionsScanTickers(watchlist)
	if len(got) != optionsScanWatchlistLimit {
		t.Fatalf("ticker count = %d, want %d", len(got), optionsScanWatchlistLimit)
	}
	if got[0] != "AAPL" || got[1] != "T000" || got[len(got)-1] != "T098" {
		t.Fatalf("normalized capped tickers = %#v", got)
	}
}

func TestSummarizePipelineRunsSeparatesStatusFromDecision(t *testing.T) {
	t.Parallel()

	got := summarizePipelineRuns([]domain.PipelineRun{
		{Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy},
		{Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalHold},
		{Status: domain.PipelineStatusCompleted},
		{Status: domain.PipelineStatusFailed, Signal: domain.PipelineSignalHold},
		{Status: domain.PipelineStatusRunning},
	})

	want := map[string]int{
		"runs": 5, "completed": 3, "failed": 1, "running": 1,
		"buy": 1, "hold": 1, "completed_without_signal": 1,
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("summary[%q] = %d, want %d (summary=%v)", key, got[key], value, got)
		}
	}
	if got["sell"] != 0 {
		t.Fatalf("summary[sell] = %d, want 0", got["sell"])
	}
}

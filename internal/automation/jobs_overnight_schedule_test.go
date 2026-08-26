package automation

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestOvernightScheduleRefreshesHistoryBeforeConsumers(t *testing.T) {
	t.Parallel()

	if historyRefreshSpec.Cron != "0 0 * * 2-6" {
		t.Fatalf("history refresh cron = %q, want midnight before overnight consumers", historyRefreshSpec.Cron)
	}
	if overnightBacktestSpec.Cron != "*/30 1-5 * * 2-6" {
		t.Fatalf("overnight backtest cron = %q", overnightBacktestSpec.Cron)
	}
	if overnightSweepSpec.Cron != "30 0 * * 2-6" || overnightGenerateSpec.Cron != "0 6 * * 2-6" {
		t.Fatalf("overnight consumer crons = sweep %q generate %q", overnightSweepSpec.Cron, overnightGenerateSpec.Cron)
	}
	if optionsDiscoverySpec.Cron != "30 6 * * 2-6" {
		t.Fatalf("options discovery cron = %q, want after overnight generation", optionsDiscoverySpec.Cron)
	}
	// Ten backtest slots are available. With a 20-candidate cap, the default
	// chunk size needs at most one screen + seven generate + one sweep slots.
	neededSlots := 1 + (overnightBacktestWatchlistLimit+overnightBacktestGeneratePerChunk-1)/overnightBacktestGeneratePerChunk + 1
	if neededSlots > 10 {
		t.Fatalf("overnight backtest needs %d slots, only 10 are scheduled", neededSlots)
	}
}

func TestOvernightCompletionErrorsExposePartialCoverage(t *testing.T) {
	t.Parallel()

	err := overnightSweepCompletionError(map[string]int{"supported": 107, "swept": 105, "failed": 2, "stale": 2})
	if err == nil || !IsDegraded(err) || !strings.Contains(err.Error(), "coverage_bps=9813") || !strings.Contains(err.Error(), "stale=2") {
		t.Fatalf("overnightSweepCompletionError(live) = %v, want detailed degraded result", err)
	}
	if err := overnightSweepCompletionError(map[string]int{"supported": 100, "swept": 79}); err == nil || IsDegraded(err) {
		t.Fatalf("overnightSweepCompletionError(79%%) = %v, want true error", err)
	}
	if err := overnightSweepCompletionError(map[string]int{"supported": 5, "swept": 0, "config_failed": 5}); err == nil || IsDegraded(err) || !strings.Contains(err.Error(), "zero supported strategies swept") {
		t.Fatalf("overnightSweepCompletionError(zero output) = %v, want true error", err)
	}
	if err := overnightSweepCompletionError(map[string]int{"supported": 100, "swept": 80, "failed": 20, "fetch_failed": 20}); err == nil || !IsDegraded(err) {
		t.Fatalf("overnightSweepCompletionError(80%%) = %v, want degraded", err)
	}
	if err := overnightSweepCompletionError(map[string]int{"supported": 100, "swept": 100, "invalid_scores": 1}); err == nil || !IsDegraded(err) {
		t.Fatalf("overnightSweepCompletionError(finding) = %v, want degraded", err)
	}
	if err := overnightGenerateCompletionError(1); err == nil || !strings.Contains(err.Error(), "1 index groups failed") {
		t.Fatalf("overnightGenerateCompletionError() = %v, want generation coverage error", err)
	}
	if err := overnightSweepCompletionError(map[string]int{}); err != nil {
		t.Fatalf("overnightSweepCompletionError(unsupported) = %v, want nil", err)
	}
	if err := overnightSweepCompletionError(map[string]int{"supported": 100, "swept": 100}); err != nil {
		t.Fatalf("overnightSweepCompletionError(complete) = %v, want nil", err)
	}
	if err := overnightGenerateCompletionError(0); err != nil {
		t.Fatalf("overnightGenerateCompletionError(0) = %v, want nil", err)
	}
}

func TestOvernightSweepSkipsUnsupportedStrategiesWithoutDataService(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{StrategyRepo: &kalshiStrategyRepoStub{strategies: []domain.Strategy{
		{Name: "event", Ticker: "KX:YES", Status: domain.StrategyStatusActive, MarketType: domain.MarketTypeKalshi},
	}}})
	orch.Register("overnight_sweep", "sweep", overnightSweepSpec, orch.overnightSweep)

	if err := orch.overnightSweep(t.Context()); err != nil {
		t.Fatalf("overnightSweep() error = %v", err)
	}
	summary := singleJobStatus(t, orch, "overnight_sweep").LastSummary
	if summary["strategies"] != 1 || summary["supported"] != 0 || summary["skipped"] != 1 || summary["coverage_bps"] != 0 {
		t.Fatalf("overnightSweep() summary = %#v, want explicit unsupported success", summary)
	}
}

func TestOvernightDependenciesAcceptDegradedSweepAndResetFailureStreak(t *testing.T) {
	now := time.Date(2026, time.August, 26, 2, 0, 0, 0, easternTime)
	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.now = func() time.Time { return now }
	orch.Register("overnight_sweep", "sweep", overnightSweepSpec, func(context.Context) error {
		summary := map[string]int{"supported": 107, "swept": 105, "failed": 2, "stale": 2}
		orch.SetLastSummary("overnight_sweep", summary)
		return overnightSweepCompletionError(summary)
	})
	orch.Register("overnight_backtest", "backtest", overnightBacktestSpec, func(context.Context) error { return nil }, "overnight_sweep")
	orch.Register("overnight_generate", "generate", overnightGenerateSpec, func(context.Context) error { return nil }, "overnight_sweep", "overnight_backtest")
	orch.SetConsecutiveFailures("overnight_sweep", 4)

	orch.runDirect(orch.jobs["overnight_sweep"])
	sweep := singleJobStatus(t, orch, "overnight_sweep")
	if sweep.LastResult != "degraded" || sweep.ConsecutiveFailures != 0 || sweep.LastSummary["stale"] != 2 {
		t.Fatalf("overnight sweep status = %+v, want degraded with reset streak", sweep)
	}
	if dep, reason := orch.dependencyBlocker(orch.jobs["overnight_backtest"], now); dep != "" || reason != "" {
		t.Fatalf("overnight_backtest blocked by degraded sweep: dep=%q reason=%q", dep, reason)
	}

	backtest := orch.jobs["overnight_backtest"]
	backtest.LastRun = &now
	backtest.LastResult = "success"
	if dep, reason := orch.dependencyBlocker(orch.jobs["overnight_generate"], now); dep != "" || reason != "" {
		t.Fatalf("overnight_generate blocked by degraded sweep: dep=%q reason=%q", dep, reason)
	}
}

func TestOptionsDiscoveryCompletionErrorRejectsReportedErrors(t *testing.T) {
	t.Parallel()

	if err := optionsDiscoveryCompletionError(nil); err != nil {
		t.Fatalf("optionsDiscoveryCompletionError(nil) = %v, want nil", err)
	}
	err := optionsDiscoveryCompletionError([]string{"screen AAPL", "generate MSFT"})
	if err == nil || !strings.Contains(err.Error(), "2 pipeline errors") {
		t.Fatalf("optionsDiscoveryCompletionError(errors) = %v", err)
	}
}

func TestHistoryRefreshCompletionErrorCoveragePolicy(t *testing.T) {
	t.Parallel()

	if err := historyRefreshCompletionError(map[string]int{}); err == nil {
		t.Fatal("historyRefreshCompletionError(empty) = nil, want error")
	}
	if err := historyRefreshCompletionError(map[string]int{"selected": 10, "updated": 4, "empty": 6}); err == nil || IsDegraded(err) {
		t.Fatalf("historyRefreshCompletionError(below floor) = %v, want true error", err)
	}
	err := historyRefreshCompletionError(map[string]int{"selected": 10, "updated": 5, "empty": 3, "stale": 2})
	if !IsDegraded(err) || !strings.Contains(err.Error(), "coverage=50%") {
		t.Fatalf("historyRefreshCompletionError(at floor) = %v, want degraded", err)
	}
	if err := historyRefreshCompletionError(map[string]int{"selected": 10, "updated": 10}); err != nil {
		t.Fatalf("historyRefreshCompletionError(full) = %v, want nil", err)
	}
}

func TestOvernightGenerationCoversEveryUniverseIndexGroup(t *testing.T) {
	for _, group := range []string{"nasdaq", "nyse", "other"} {
		if !slices.Contains(overnightIndexGroups, group) {
			t.Fatalf("overnight index groups %v omit %q", overnightIndexGroups, group)
		}
	}
}

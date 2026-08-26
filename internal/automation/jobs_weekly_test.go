package automation

import (
	"math"
	"strings"
	"testing"
)

func TestStrategyTournamentDescriptionMatchesReadOnlyBehavior(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.registerWeeklyJobs()

	status := singleJobStatus(t, orch, "strategy_tournament")
	if status.Description != "Rank active strategies and recommend review candidates" {
		t.Fatalf("strategy_tournament description = %q", status.Description)
	}
}

func TestValidTournamentSharpeRejectsNonfiniteValues(t *testing.T) {
	t.Parallel()
	if !validTournamentSharpe(1.25) {
		t.Fatal("finite Sharpe rejected")
	}
	for _, sharpe := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if validTournamentSharpe(sharpe) {
			t.Fatalf("non-finite Sharpe %v accepted", sharpe)
		}
	}
}

func TestUniverseRefreshCompletionErrorRejectsEmptyProviderResult(t *testing.T) {
	t.Parallel()

	if err := universeRefreshCompletionError(1); err != nil {
		t.Fatalf("universeRefreshCompletionError(1) = %v, want nil", err)
	}
	if err := universeRefreshCompletionError(0); err == nil {
		t.Fatal("universeRefreshCompletionError(0) = nil, want error")
	}
}

func TestStrategyTournamentCompletionCoveragePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		summary  map[string]int
		degraded bool
		wantErr  bool
		contains string
	}{
		{name: "complete", summary: map[string]int{"supported": 100, "ranked": 100, "provider_contacted": 100}},
		{name: "below threshold", summary: map[string]int{"supported": 100, "ranked": 69, "provider_contacted": 69}, wantErr: true, contains: "below 70%"},
		{name: "exact threshold", summary: map[string]int{"supported": 100, "ranked": 70, "provider_contacted": 70, "failed": 30}, wantErr: true, degraded: true},
		{name: "zero ranked", summary: map[string]int{"supported": 10}, wantErr: true, contains: "zero ranking"},
		{name: "provider untouched", summary: map[string]int{"supported": 10, "ranked": 10}, wantErr: true, contains: "provider"},
		{name: "all invalid configs", summary: map[string]int{"supported": 10, "failed": 10, "config_failed": 10}, wantErr: true, contains: "config_failed=10"},
		{name: "live shape", summary: map[string]int{"supported": 107, "ranked": 78, "provider_contacted": 90, "failed": 29, "stale": 12}, wantErr: true, degraded: true, contains: "coverage_bps=7289"},
		{name: "nonfinite Sharpe", summary: map[string]int{"supported": 10, "ranked": 9, "provider_contacted": 10, "failed": 1, "nonfinite": 1}, wantErr: true, degraded: true, contains: "nonfinite=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := strategyTournamentCompletionError(tt.summary)
			if (err != nil) != tt.wantErr || IsDegraded(err) != tt.degraded {
				t.Fatalf("strategyTournamentCompletionError(%v) = %v, degraded=%v", tt.summary, err, IsDegraded(err))
			}
			if tt.contains != "" && !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("error %q does not contain %q", err, tt.contains)
			}
		})
	}
}

func TestStrategyCoverageJobsRejectNilDependencies(t *testing.T) {
	t.Parallel()
	o := NewJobOrchestrator(OrchestratorDeps{})
	if err := o.strategyTournament(t.Context()); err == nil || IsDegraded(err) {
		t.Fatalf("strategyTournament(nil deps) = %v, want true error", err)
	}
	if err := o.strategyResweep(t.Context()); err == nil || IsDegraded(err) {
		t.Fatalf("strategyResweep(nil deps) = %v, want true error", err)
	}
}

package automation

import "testing"

import pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"

func TestResearchReportTypeConstantsAreStable(t *testing.T) {
	t.Parallel()

	const (
		wantWalletIntel      = "wallet_intelligence"
		wantEventCalibration = "event_calibration"
		wantSolverArb        = "solver_arbitrage"
		wantLatencyResearch  = "latency_research"
	)

	if got := reportTypeWalletIntel; got != wantWalletIntel {
		t.Fatalf("reportTypeWalletIntel = %q, want %q", got, wantWalletIntel)
	}
	if got := reportTypeEventCalibration; got != wantEventCalibration {
		t.Fatalf("reportTypeEventCalibration = %q, want %q", got, wantEventCalibration)
	}
	if got := reportTypeSolverArb; got != wantSolverArb {
		t.Fatalf("reportTypeSolverArb = %q, want %q", got, wantSolverArb)
	}
	if got := reportTypeLatencyResearch; got != wantLatencyResearch {
		t.Fatalf("reportTypeLatencyResearch = %q, want %q", got, wantLatencyResearch)
	}
}

func TestRegisterReportJobsDoesNotEnableResearchReportTypesAutomatically(t *testing.T) {
	t.Parallel()

	var reportRepo pgrepo.ReportArtifactRepo
	orch := NewJobOrchestrator(OrchestratorDeps{ReportArtifactRepo: &reportRepo})
	orch.registerReportJobs()

	if _, ok := orch.jobs["paper_validation_report"]; !ok {
		t.Fatal("paper_validation_report job not registered")
	}

	for _, name := range []string{
		reportTypeWalletIntel,
		reportTypeEventCalibration,
		reportTypeSolverArb,
		reportTypeLatencyResearch,
	} {
		if _, ok := orch.jobs[name]; ok {
			t.Fatalf("report type %q should not be registered as a scheduled job without explicit registration", name)
		}
	}
}

package automation

import (
	"testing"

	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

func TestRegisterReportJobs_NoReportArtifactRepo(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.registerReportJobs()

	if _, ok := orch.jobs["paper_validation_report"]; ok {
		t.Fatal("expected paper_validation_report job to NOT be registered when repo is nil")
	}
}

func TestRegisterReportJobs_WithReportArtifactRepo(t *testing.T) {
	t.Parallel()

	var reportRepo pgrepo.ReportArtifactRepo
	orch := NewJobOrchestrator(OrchestratorDeps{ReportArtifactRepo: &reportRepo})
	orch.registerReportJobs()

	if _, ok := orch.jobs["paper_validation_report"]; !ok {
		t.Fatal("paper_validation_report job not registered")
	}
	if orch.reportWorker == nil {
		t.Fatal("expected report worker to be initialized")
	}
}

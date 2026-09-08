package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

type generatedRobustnessStub struct {
	summary generativestrategy.BatchSummary
	calls   int
}

func (stub *generatedRobustnessStub) RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.BatchSummary, error) {
	stub.calls++
	return stub.summary, nil
}

func TestGeneratedRobustnessRegistrationIsFailClosedAndDependsOnEvaluation(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true}, CanonicalAccountID: accountID, DiscoveryScopeID: scopeID,
	})
	orchestrator.registerGeneratedRobustnessJob()
	if orchestrator.jobs["generated_robustness"] != nil || !hasUnavailableJob(orchestrator.UnavailableJobs(), "generated_robustness") {
		t.Fatal("generated robustness registered without assessment service")
	}

	stub := &generatedRobustnessStub{summary: generativestrategy.BatchSummary{Eligible: 1, Completed: 1}}
	orchestrator = NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true}, CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, GeneratedRobustness: stub,
	})
	orchestrator.registerGeneratedRobustnessJob()
	job := orchestrator.jobs["generated_robustness"]
	if job == nil || len(job.DependsOn) != 1 || job.DependsOn[0] != "generated_evaluation" {
		t.Fatalf("generated robustness dependency = %+v", job)
	}
	if err := job.Fn(context.Background()); err != nil || stub.calls != 1 {
		t.Fatalf("run error=%v calls=%d", err, stub.calls)
	}
	if job.LastSummary["completed"] != 1 {
		t.Fatalf("summary=%v", job.LastSummary)
	}
}

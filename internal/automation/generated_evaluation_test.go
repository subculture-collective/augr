package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

type generatedEvaluationStub struct {
	summary generativestrategy.EvaluationBatchSummary
	calls   int
}

func (stub *generatedEvaluationStub) RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.EvaluationBatchSummary, error) {
	stub.calls++
	return stub.summary, nil
}

func TestGeneratedEvaluationRegistrationIsFailClosedAndDependsOnResearch(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true}, CanonicalAccountID: accountID, DiscoveryScopeID: scopeID,
	})
	orchestrator.registerGeneratedEvaluationJob()
	if orchestrator.jobs["generated_evaluation"] != nil || !hasUnavailableJob(orchestrator.UnavailableJobs(), "generated_evaluation") {
		t.Fatal("generated evaluation registered without evidence service")
	}

	stub := &generatedEvaluationStub{summary: generativestrategy.EvaluationBatchSummary{Eligible: 2, Completed: 2}}
	orchestrator = NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true}, CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, GeneratedEvaluation: stub,
	})
	orchestrator.registerGeneratedEvaluationJob()
	job := orchestrator.jobs["generated_evaluation"]
	if job == nil || len(job.DependsOn) != 1 || job.DependsOn[0] != "generated_research" {
		t.Fatalf("generated evaluation dependency = %+v", job)
	}
	if err := job.Fn(context.Background()); err != nil || stub.calls != 1 {
		t.Fatalf("run error=%v calls=%d", err, stub.calls)
	}
	if job.LastSummary["completed"] != 2 {
		t.Fatalf("summary=%v", job.LastSummary)
	}
}

package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

type generatedDeploymentStub struct {
	summary generativestrategy.BatchSummary
	calls   int
}

func (stub *generatedDeploymentStub) RunEligible(context.Context, uuid.UUID, uuid.UUID, int) (generativestrategy.BatchSummary, error) {
	stub.calls++
	return stub.summary, nil
}

func TestGeneratedDeploymentRegistrationIsFailClosedAndDependsOnRobustness(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true}, CanonicalAccountID: accountID, DiscoveryScopeID: scopeID,
	})
	orchestrator.registerGeneratedDeploymentJob()
	if orchestrator.jobs["generated_deployment"] != nil || !hasUnavailableJob(orchestrator.UnavailableJobs(), "generated_deployment") {
		t.Fatal("generated deployment registered without proposal service")
	}
	stub := &generatedDeploymentStub{summary: generativestrategy.BatchSummary{Eligible: 1, Completed: 1}}
	orchestrator = NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true}, CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, GeneratedDeployment: stub,
	})
	orchestrator.registerGeneratedDeploymentJob()
	job := orchestrator.jobs["generated_deployment"]
	if job == nil || len(job.DependsOn) != 1 || job.DependsOn[0] != "generated_robustness" {
		t.Fatalf("generated deployment dependency=%+v", job)
	}
	if err := job.Fn(context.Background()); err != nil || stub.calls != 1 || job.LastSummary["completed"] != 1 {
		t.Fatalf("run error=%v calls=%d summary=%v", err, stub.calls, job.LastSummary)
	}
}

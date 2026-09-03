package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

type generatedResearchPreparationStub struct {
	accountID uuid.UUID
	scopeID   uuid.UUID
	limit     int
	summary   generativestrategy.PreparationBatchSummary
}

func (stub *generatedResearchPreparationStub) RunEligible(_ context.Context, accountID, scopeID uuid.UUID, limit int) (generativestrategy.PreparationBatchSummary, error) {
	stub.accountID, stub.scopeID, stub.limit = accountID, scopeID, limit
	return stub.summary, nil
}

func TestGeneratedResearchPreparationRegistersAndRunsOnlyWithExactCapability(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	stub := &generatedResearchPreparationStub{summary: generativestrategy.PreparationBatchSummary{Eligible: 1, Completed: 1}}
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true},
		CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, GeneratedResearchPreparation: stub,
	})
	orchestrator.registerGeneratedResearchPreparationJob()
	job := orchestrator.jobs["generated_research_prepare"]
	if job == nil || len(job.DependsOn) != 1 || job.DependsOn[0] != "generated_proposal" {
		t.Fatalf("generated research preparation job = %+v", job)
	}
	if err := job.Fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.accountID != accountID || stub.scopeID != scopeID || stub.limit != generatedResearchPreparationBatchLimit || job.LastSummary["completed"] != 1 {
		t.Fatalf("stub=%+v summary=%+v", stub, job.LastSummary)
	}
}

func TestGeneratedResearchPreparationUnavailableWithoutPreparer(t *testing.T) {
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true},
		CanonicalAccountID: uuid.New(), DiscoveryScopeID: uuid.New(),
	})
	orchestrator.registerGeneratedResearchPreparationJob()
	if orchestrator.jobs["generated_research_prepare"] != nil || !hasUnavailableJob(orchestrator.UnavailableJobs(), "generated_research_prepare") {
		t.Fatalf("jobs=%v unavailable=%+v", orchestrator.jobs, orchestrator.UnavailableJobs())
	}
}

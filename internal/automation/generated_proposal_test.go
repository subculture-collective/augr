package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

type generatedProposalStub struct {
	accountID uuid.UUID
	scopeID   uuid.UUID
	limit     int
	summary   generativestrategy.ProposalBatchSummary
	err       error
}

func (stub *generatedProposalStub) RunEligible(_ context.Context, accountID, scopeID uuid.UUID, limit int) (generativestrategy.ProposalBatchSummary, error) {
	stub.accountID, stub.scopeID, stub.limit = accountID, scopeID, limit
	return stub.summary, stub.err
}

func TestGeneratedProposalRegistersAndRunsOnlyWithExactStockCapability(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	stub := &generatedProposalStub{summary: generativestrategy.ProposalBatchSummary{Eligible: 1, Completed: 1}}
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true},
		CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, GeneratedProposal: stub,
	})
	orchestrator.registerGeneratedProposalJob()
	job := orchestrator.jobs["generated_proposal"]
	if job == nil {
		t.Fatal("generated proposal job was not registered")
	}
	if err := job.Fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.accountID != accountID || stub.scopeID != scopeID || stub.limit != 1 || job.LastSummary["completed"] != 1 {
		t.Fatalf("stub=%+v summary=%+v", stub, job.LastSummary)
	}
}

func TestGeneratedProposalUnavailableWithoutGeneratorAndAbsentWithoutStockEvidence(t *testing.T) {
	missing := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true},
		CanonicalAccountID: uuid.New(), DiscoveryScopeID: uuid.New(),
	})
	missing.registerGeneratedProposalJob()
	if missing.jobs["generated_proposal"] != nil || !hasUnavailableJob(missing.UnavailableJobs(), "generated_proposal") {
		t.Fatalf("missing jobs=%v unavailable=%+v", missing.jobs, missing.UnavailableJobs())
	}
	blocked := NewJobOrchestrator(OrchestratorDeps{DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: false}})
	blocked.registerGeneratedProposalJob()
	if blocked.jobs["generated_proposal"] != nil || hasUnavailableJob(blocked.UnavailableJobs(), "generated_proposal") {
		t.Fatalf("blocked jobs=%v unavailable=%+v", blocked.jobs, blocked.UnavailableJobs())
	}
}

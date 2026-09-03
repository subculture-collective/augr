package automation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
)

type generatedResearchStub struct {
	accountID uuid.UUID
	scopeID   uuid.UUID
	limit     int
	now       time.Time
	summary   generativestrategy.BatchSummary
	err       error
}

func (stub *generatedResearchStub) RunEligible(_ context.Context, accountID, scopeID uuid.UUID, limit int, now time.Time) (generativestrategy.BatchSummary, error) {
	stub.accountID, stub.scopeID, stub.limit, stub.now = accountID, scopeID, limit, now
	return stub.summary, stub.err
}

func TestGeneratedResearchRegistersAndRunsOnlyWithExactCapability(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	stub := &generatedResearchStub{summary: generativestrategy.BatchSummary{Eligible: 2, Completed: 2}}
	orchestrator := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true},
		CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, GeneratedResearch: stub,
	})
	orchestrator.registerGeneratedResearchJob()
	job := orchestrator.jobs["generated_research"]
	if job == nil {
		t.Fatal("generated research job was not registered")
	}
	if len(job.DependsOn) != 1 || job.DependsOn[0] != "generated_proposal" {
		t.Fatalf("generated research dependencies = %v", job.DependsOn)
	}
	now := time.Date(2026, 9, 3, 12, 0, 0, 123456789, time.UTC)
	orchestrator.now = func() time.Time { return now }
	if err := job.Fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.accountID != accountID || stub.scopeID != scopeID || stub.limit != generatedResearchBatchLimit || !stub.now.Equal(now.Truncate(time.Microsecond)) {
		t.Fatalf("runner arguments = %s/%s/%d/%s", stub.accountID, stub.scopeID, stub.limit, stub.now)
	}
	if job.LastSummary["completed"] != 2 || job.LastSummary["failed"] != 0 {
		t.Fatalf("summary = %+v", job.LastSummary)
	}
}

func TestGeneratedResearchIsUnavailableWithoutRunnerAndAbsentWithoutStockEvidence(t *testing.T) {
	missingRunner := NewJobOrchestrator(OrchestratorDeps{
		DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: true},
		CanonicalAccountID: uuid.New(), DiscoveryScopeID: uuid.New(),
	})
	missingRunner.registerGeneratedResearchJob()
	if missingRunner.jobs["generated_research"] != nil || !hasUnavailableJob(missingRunner.UnavailableJobs(), "generated_research") {
		t.Fatalf("missing runner jobs=%v unavailable=%+v", missingRunner.jobs, missingRunner.UnavailableJobs())
	}
	blocked := NewJobOrchestrator(OrchestratorDeps{DiscoveryReadiness: &DiscoveryReadiness{CapabilitiesEvaluated: true, StockReady: false}})
	blocked.registerGeneratedResearchJob()
	if blocked.jobs["generated_research"] != nil || hasUnavailableJob(blocked.UnavailableJobs(), "generated_research") {
		t.Fatalf("blocked capability jobs=%v unavailable=%+v", blocked.jobs, blocked.UnavailableJobs())
	}
}

func hasUnavailableJob(values []UnavailableJob, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

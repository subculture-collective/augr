package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

type promotionActivationStub struct {
	calls   int
	summary pgrepo.PromotionActivationBatch
	err     error
}

func (stub *promotionActivationStub) ProjectEligibleActivations(context.Context, uuid.UUID, uuid.UUID, bool) (pgrepo.PromotionActivationBatch, error) {
	stub.calls++
	return stub.summary, stub.err
}

func TestPromotionActivationJobRequiresExplicitEnableAndExactScope(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	stub := &promotionActivationStub{summary: pgrepo.PromotionActivationBatch{Eligible: 2, Activated: 1, Noop: 1}}
	disabled := NewJobOrchestrator(OrchestratorDeps{CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, PromotionActivation: stub})
	disabled.RegisterAll()
	if _, exists := disabled.jobs["promotion_activation"]; exists {
		t.Fatal("promotion activation registered without explicit enable")
	}

	enabled := NewJobOrchestrator(OrchestratorDeps{
		CanonicalAccountID: accountID, DiscoveryScopeID: scopeID, PromotionActivation: stub, AutomaticShadowPromotion: true,
	})
	enabled.RegisterAll()
	job, exists := enabled.jobs["promotion_activation"]
	if !exists {
		t.Fatal("promotion activation was not registered")
	}
	if err := job.Fn(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 || job.LastSummary["activated"] != 1 || job.LastSummary["noop"] != 1 {
		t.Fatalf("calls=%d summary=%v", stub.calls, job.LastSummary)
	}

	missingScope := NewJobOrchestrator(OrchestratorDeps{
		CanonicalAccountID: accountID, PromotionActivation: stub, AutomaticShadowPromotion: true,
	})
	missingScope.RegisterAll()
	if _, exists := missingScope.jobs["promotion_activation"]; exists {
		t.Fatal("promotion activation registered without exact scope")
	}
	found := false
	for _, unavailable := range missingScope.UnavailableJobs() {
		found = found || unavailable.Name == "promotion_activation"
	}
	if !found {
		t.Fatal("missing scope was not exposed in unavailable diagnostics")
	}
}

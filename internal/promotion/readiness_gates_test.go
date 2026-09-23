package promotion

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestExpectedReconciliationExternalAccountIDForInternalVenue(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	if got := expectedReconciliationExternalAccountID(&domain.Account{ID: id, Venue: InternalLedgerVenue}); got != id.String() {
		t.Fatalf("internal venue identity = %q, want account id", got)
	}
	if got := expectedReconciliationExternalAccountID(&domain.Account{ID: id, Venue: "alpaca", ExternalAccountID: "PA123"}); got != "PA123" {
		t.Fatalf("alpaca identity = %q", got)
	}
	if got := expectedReconciliationExternalAccountID(nil); got != "" {
		t.Fatalf("nil account identity = %q", got)
	}
}

func TestPaperValidationGateDefaultsOffAndBlocksWhenRequired(t *testing.T) {
	t.Parallel()
	base := ReadinessInput{ReconciliationAvailable: true}
	reasons := EvaluateReadiness(base).BlockReasons()
	for _, reason := range reasons {
		if reason == BlockPaperValidationPending {
			t.Fatal("default policy must not evaluate the paper validation gate")
		}
	}
	required := base
	required.PaperValidationPolicy = PaperValidationPolicy{RequirePaperValidation: true}
	if !containsBlock(EvaluateReadiness(required).BlockReasons(), BlockPaperValidationPending) {
		t.Fatal("required gate without evidence must block")
	}
	required.PaperValidation = PaperValidationEvidence{Available: true, ClosedTrades: 19, ElapsedDays: 60, GoDecision: true}
	if !containsBlock(EvaluateReadiness(required).BlockReasons(), BlockPaperValidationPending) {
		t.Fatal("fewer than 20 closed trades must block")
	}
	required.PaperValidation = PaperValidationEvidence{Available: true, ClosedTrades: 25, ElapsedDays: 59, GoDecision: true}
	if !containsBlock(EvaluateReadiness(required).BlockReasons(), BlockPaperValidationPending) {
		t.Fatal("fewer than 60 days must block")
	}
	required.PaperValidation = PaperValidationEvidence{Available: true, ClosedTrades: 25, ElapsedDays: 61, GoDecision: true}
	if containsBlock(EvaluateReadiness(required).BlockReasons(), BlockPaperValidationPending) {
		t.Fatal("satisfied gate must not block")
	}
	if policy := DefaultPaperValidationPolicy(); policy.RequirePaperValidation || policy.MinClosedTrades != 20 || policy.MinCalendarDays != 60 {
		t.Fatalf("default policy = %+v", policy)
	}
	_ = time.Now
}

func containsBlock(reasons []string, target string) bool {
	for _, reason := range reasons {
		if reason == target {
			return true
		}
	}
	return false
}

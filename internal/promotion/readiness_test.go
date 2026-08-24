package promotion

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func trustedReadinessInput() ReadinessInput {
	accountID := uuid.New()
	generatedAt := time.Now().UTC().Truncate(time.Microsecond)
	return ReadinessInput{
		ConfiguredAccountID: accountID,
		Account:             &domain.Account{ID: accountID, Name: "scored", Environment: domain.AccountEnvironmentPaperScored, Venue: "kalshi", ExternalAccountID: "provider-account", BaseCurrency: "USD", StorageNamespace: "paper_scored/test", EvidenceClass: domain.PaperEvidenceClassPromotion, StartingCapital: decimal.NewFromInt(500), BuyingPowerMultiplier: decimal.NewFromInt(1), MarginProfile: domain.MarginProfileCash, Status: domain.AccountStatusActive, CreatedBy: "test", CreationMetadata: []byte(`{}`), CreatedAt: generatedAt},
		ScopeID:             uuid.New(), ScopeAccountID: accountID, EvidenceImmutable: true,
		OpenLots: 2, FreshMarks: 2, ReconciliationAvailable: true, ReconciliationPassed: true,
		ReconciliationAccountID: accountID, ReconciliationVenue: "kalshi", ReconciliationExternalAccountID: "provider-account",
		GeneratedAt: generatedAt, CheckpointGeneratedAt: generatedAt.Add(-time.Minute), ReconciliationGeneratedAt: generatedAt.Add(-time.Minute),
		CheckpointMaxAge: 5 * time.Minute, ReconciliationMaxAge: 5 * time.Minute,
	}
}

func TestEffectiveReadinessRejectsStaleEvidenceAgainstGeneratedAt(t *testing.T) {
	input := trustedReadinessInput()
	input.CheckpointGeneratedAt = input.GeneratedAt.Add(-input.CheckpointMaxAge - time.Microsecond)
	input.ReconciliationGeneratedAt = input.GeneratedAt.Add(-input.ReconciliationMaxAge - time.Microsecond)
	got := EvaluateReadiness(input)
	if got.Ready() || len(got.BlockReasons()) != 2 {
		t.Fatalf("readiness = %+v", got)
	}
}

func TestEffectiveReadinessRejectsProviderBindingMismatch(t *testing.T) {
	input := trustedReadinessInput()
	input.ReconciliationExternalAccountID = "other-account"
	if got := EvaluateReadiness(input); got.Ready() || len(got.BlockReasons()) != 1 || got.BlockReasons()[0] != BlockReconciliationMismatch {
		t.Fatalf("readiness = %+v", got)
	}
}

func TestEffectiveReadinessRejectsNegativeAndInconsistentInventory(t *testing.T) {
	input := trustedReadinessInput()
	input.FreshMarks = -1
	if got := EvaluateReadiness(input); got.Ready() || !containsReason(got.BlockReasons(), BlockInventoryInvalid) {
		t.Fatalf("readiness = %+v", got)
	}
}

func containsReason(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestEffectiveReadinessRequiresEveryTrustedEvidenceGate(t *testing.T) {
	input := trustedReadinessInput()
	if got := EvaluateReadiness(input); !got.Ready() || len(got.BlockReasons()) != 0 {
		t.Fatalf("readiness = %+v", got)
	}

	input.ScopeAccountID = uuid.New()
	input.StaleMarks = 1
	input.ReconciliationVenue = "alpaca"
	input.UnavailableReasons = []string{"marks_stale"}
	got := EvaluateReadiness(input)
	if got.Ready() || len(got.BlockReasons()) < 4 {
		t.Fatalf("readiness = %+v", got)
	}
}

func TestEffectiveReadinessRejectsStressAccountAndIncompleteLinks(t *testing.T) {
	input := trustedReadinessInput()
	input.Account.Environment = domain.AccountEnvironmentPaperStress
	input.Account.EvidenceClass = domain.PaperEvidenceClassSynthetic
	input.MissingCanonicalLinks = 1
	input.FreshMarks = 1
	input.UnavailableMarks = 1
	got := EvaluateReadiness(input)
	if got.Ready() || len(got.BlockReasons()) < 4 {
		t.Fatalf("readiness = %+v", got)
	}
}

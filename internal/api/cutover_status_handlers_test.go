package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type stubCutoverEvidence struct {
	inventory *repository.CutoverEvidenceInventory
}

func (s stubCutoverEvidence) GetCutoverEvidenceInventory(context.Context, uuid.UUID) (*repository.CutoverEvidenceInventory, error) {
	return s.inventory, nil
}

func TestCutoverStatusRequiresTrustedScopedFreshReconciledEvidence(t *testing.T) {
	accountID, scopeID, markID := uuid.New(), uuid.New(), uuid.New()
	generatedAt := time.Now().UTC().Truncate(time.Microsecond)
	account := domain.Account{ID: accountID, Name: "scored", Environment: domain.AccountEnvironmentPaperScored, Venue: "kalshi", ExternalAccountID: "provider-account", BaseCurrency: "USD", StorageNamespace: "paper_scored/test", EvidenceClass: domain.PaperEvidenceClassPromotion, StartingCapital: decimal.NewFromInt(500), BuyingPowerMultiplier: decimal.NewFromInt(1), MarginProfile: domain.MarginProfileCash, Status: domain.AccountStatusActive, CreatedBy: "test", CreationMetadata: []byte(`{}`), CreatedAt: generatedAt}
	projection := &stubProjectionReader{snapshot: &repository.ProjectionSnapshot{
		Checkpoint:              &ledger.ProjectionCheckpoint{AccountID: accountID, AsOf: generatedAt.Add(-time.Minute), LotCount: 1},
		Valuation:               &ledger.ProjectionValuation{Positions: []ledger.ProjectionPosition{{Open: true, MarkObservationID: markID}}},
		ReconciliationAvailable: true, ReconciliationPassed: true,
		ReconciliationAccountID: accountID, ReconciliationProvider: "kalshi", ReconciliationExternalAccountID: "provider-account", ReconciliationGeneratedAt: generatedAt.Add(-time.Minute),
		FreshMarks: 1,
	}}
	server := &Server{logger: slog.Default(), projectionAccountID: &accountID, economicAccounts: &stubEconomicAccountReader{accounts: []domain.Account{account}}, projections: projection, cutoverEvidence: stubCutoverEvidence{inventory: &repository.CutoverEvidenceInventory{ScopeID: scopeID, ScopeAccountID: accountID, ScopedArtifacts: 1, LegacyArtifacts: 9}}}
	rr := httptest.NewRecorder()
	server.handleCutoverStatus(rr, httptest.NewRequest("GET", "/api/v1/release/cutover-status", nil))
	var got CutoverStatus
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.PromotionReady || !got.AccountTrusted || got.FreshMarks != 1 || got.QuarantinedLegacyRows != 9 || len(got.PromotionBlockReasons) != 0 {
		t.Fatalf("status = %+v", got)
	}
}

func TestCutoverStatusSurfacesAndRequiresObservedProviderBinding(t *testing.T) {
	accountID, scopeID := uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	account := domain.Account{ID: accountID, Name: "scored", Environment: domain.AccountEnvironmentPaperScored, Venue: "kalshi", ExternalAccountID: "expected", BaseCurrency: "USD", StorageNamespace: "paper_scored/test", EvidenceClass: domain.PaperEvidenceClassPromotion, StartingCapital: decimal.NewFromInt(500), BuyingPowerMultiplier: decimal.NewFromInt(1), MarginProfile: domain.MarginProfileCash, Status: domain.AccountStatusActive, CreatedBy: "test", CreationMetadata: []byte(`{}`), CreatedAt: now}
	projection := &stubProjectionReader{snapshot: &repository.ProjectionSnapshot{Checkpoint: &ledger.ProjectionCheckpoint{AccountID: accountID, AsOf: now.Add(-time.Minute)}, Valuation: &ledger.ProjectionValuation{}, ReconciliationAvailable: true, ReconciliationPassed: true, ReconciliationAccountID: accountID, ReconciliationProvider: "kalshi", ReconciliationExternalAccountID: "observed-other", ReconciliationGeneratedAt: now.Add(-time.Minute)}}
	server := &Server{logger: slog.Default(), projectionAccountID: &accountID, economicAccounts: &stubEconomicAccountReader{accounts: []domain.Account{account}}, projections: projection, cutoverEvidence: stubCutoverEvidence{inventory: &repository.CutoverEvidenceInventory{ScopeID: scopeID, ScopeAccountID: accountID, ScopedArtifacts: 1}}}
	rr := httptest.NewRecorder()
	server.handleCutoverStatus(rr, httptest.NewRequest("GET", "/api/v1/release/cutover-status", nil))
	var got CutoverStatus
	_ = json.NewDecoder(rr.Body).Decode(&got)
	if got.PromotionReady || got.ReconciliationExternalAccountID != "observed-other" {
		t.Fatalf("status = %+v", got)
	}
}

func TestCutoverStatusFailsClosedWithoutConfiguredAccount(t *testing.T) {
	server := &Server{logger: slog.Default()}
	rr := httptest.NewRecorder()
	server.handleCutoverStatus(rr, httptest.NewRequest("GET", "/api/v1/release/cutover-status", nil))
	var got CutoverStatus
	_ = json.NewDecoder(rr.Body).Decode(&got)
	if got.PromotionReady || len(got.PromotionBlockReasons) != 1 {
		t.Fatalf("status = %+v", got)
	}
}

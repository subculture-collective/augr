package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/promotion"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const (
	cutoverCheckpointMaxAge     = 5 * time.Minute
	cutoverReconciliationMaxAge = 5 * time.Minute
)

type CutoverStatus struct {
	GeneratedAt                     time.Time `json:"generated_at"`
	PromotionReady                  bool      `json:"promotion_ready"`
	AccountTrusted                  bool      `json:"account_trusted"`
	AccountID                       string    `json:"account_id,omitempty"`
	ScopeID                         string    `json:"scope_id,omitempty"`
	ScopedArtifacts                 int       `json:"scoped_artifacts"`
	QuarantinedLegacyRows           int       `json:"quarantined_legacy_rows"`
	CanonicalLots                   int       `json:"canonical_lots"`
	ScopeMismatches                 int       `json:"scope_mismatches"`
	MissingCanonicalLinks           int       `json:"missing_canonical_links"`
	FreshMarks                      int       `json:"fresh_marks"`
	StaleMarks                      int       `json:"stale_marks"`
	UnavailableMarks                int       `json:"unavailable_marks"`
	ReconciliationAvailable         bool      `json:"reconciliation_available"`
	ReconciliationPassed            bool      `json:"reconciliation_passed"`
	ReconciliationVenue             string    `json:"reconciliation_venue,omitempty"`
	ReconciliationExternalAccountID string    `json:"reconciliation_external_account_id,omitempty"`
	UnavailableReasons              []string  `json:"unavailable_reasons"`
	PromotionBlockReasons           []string  `json:"promotion_block_reasons"`
}

func (s *Server) handleCutoverStatus(w http.ResponseWriter, r *http.Request) {
	generatedAt := time.Now().UTC()
	status := CutoverStatus{GeneratedAt: generatedAt, UnavailableReasons: []string{}, PromotionBlockReasons: []string{}}
	if s.projectionAccountID == nil {
		status.PromotionBlockReasons = []string{promotion.BlockConfiguredAccountUnavailable}
		respondJSON(w, http.StatusOK, status)
		return
	}
	status.AccountID = s.projectionAccountID.String()
	if s.economicAccounts == nil || s.projections == nil || s.cutoverEvidence == nil {
		status.PromotionBlockReasons = []string{promotion.BlockConfiguredAccountUnavailable}
		respondJSON(w, http.StatusOK, status)
		return
	}
	account, accountErr := s.economicAccounts.GetByID(r.Context(), *s.projectionAccountID)
	snapshot, projectionErr := s.projections.GetLatestPortfolioProjection(r.Context(), *s.projectionAccountID, generatedAt)
	inventory, inventoryErr := s.cutoverEvidence.GetCutoverEvidenceInventory(r.Context(), *s.projectionAccountID)
	if accountErr != nil || projectionErr != nil || inventoryErr != nil || account == nil || snapshot == nil || snapshot.Checkpoint == nil || snapshot.Valuation == nil || inventory == nil {
		if projectionErr != nil && !errors.Is(projectionErr, repository.ErrNotFound) {
			s.logger.Error("read cutover projection", "account_id", status.AccountID, "error", projectionErr.Error())
		}
		status.PromotionBlockReasons = []string{promotion.BlockConfiguredAccountUnavailable}
		respondJSON(w, http.StatusOK, status)
		return
	}
	status.AccountTrusted = account.ID == *s.projectionAccountID && account.Validate() == nil && account.PromotionEligible() && account.Status == domain.AccountStatusActive
	status.ScopeID = inventory.ScopeID.String()
	status.ScopedArtifacts = inventory.ScopedArtifacts
	status.QuarantinedLegacyRows = inventory.LegacyArtifacts
	status.CanonicalLots = snapshot.Checkpoint.LotCount
	status.ScopeMismatches = inventory.ScopeMismatchCount
	status.MissingCanonicalLinks = inventory.MissingCanonicalLinks
	status.FreshMarks, status.StaleMarks, status.UnavailableMarks = snapshot.FreshMarks, snapshot.StaleMarks, snapshot.UnavailableMarks
	status.ReconciliationAvailable = snapshot.ReconciliationAvailable
	status.ReconciliationPassed = snapshot.ReconciliationPassed
	status.ReconciliationVenue = snapshot.ReconciliationProvider
	status.ReconciliationExternalAccountID = snapshot.ReconciliationExternalAccountID
	valuation := s.loadPortfolioValuation(r.Context(), s.projectionAccountID, generatedAt)
	status.UnavailableReasons = valuation.UnavailableReasons
	readiness := promotion.EvaluateReadiness(promotion.ReadinessInput{
		ConfiguredAccountID: *s.projectionAccountID, Account: account, ScopeID: inventory.ScopeID, ScopeAccountID: inventory.ScopeAccountID,
		EvidenceImmutable: inventory.ScopedArtifacts > 0, ScopedArtifacts: inventory.ScopedArtifacts, LegacyArtifacts: inventory.LegacyArtifacts, ScopeMismatchCount: inventory.ScopeMismatchCount, MissingCanonicalLinks: inventory.MissingCanonicalLinks,
		OpenLots: status.FreshMarks + status.StaleMarks + status.UnavailableMarks, FreshMarks: status.FreshMarks, StaleMarks: status.StaleMarks, UnavailableMarks: status.UnavailableMarks,
		ReconciliationAvailable: snapshot.ReconciliationAvailable, ReconciliationPassed: snapshot.ReconciliationPassed,
		ReconciliationAccountID: snapshot.ReconciliationAccountID, ReconciliationVenue: snapshot.ReconciliationProvider, ReconciliationExternalAccountID: snapshot.ReconciliationExternalAccountID,
		GeneratedAt: generatedAt, CheckpointGeneratedAt: snapshot.Checkpoint.AsOf, ReconciliationGeneratedAt: snapshot.ReconciliationGeneratedAt,
		CheckpointMaxAge: cutoverCheckpointMaxAge, ReconciliationMaxAge: cutoverReconciliationMaxAge, UnavailableReasons: status.UnavailableReasons,
	})
	status.PromotionReady, status.PromotionBlockReasons = readiness.Ready(), readiness.BlockReasons()
	respondJSON(w, http.StatusOK, status)
}

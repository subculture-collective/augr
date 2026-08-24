package promotion

import (
	"sort"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

const (
	BlockConfiguredAccountUnavailable = "configured_projection_account_unavailable"
	BlockConfiguredAccountUntrusted   = "configured_projection_account_untrusted"
	BlockEvidenceUnscoped             = "promotion_evidence_unscoped"
	BlockEvidenceMutable              = "promotion_evidence_mutable"
	BlockScopeMismatch                = "scope_mismatch"
	BlockCanonicalLinksMissing        = "canonical_links_missing"
	BlockMarksIncomplete              = "marks_incomplete"
	BlockMarksStale                   = "marks_stale"
	BlockMarksUnavailable             = "marks_unavailable"
	BlockReconciliationMissing        = "reconciliation_missing"
	BlockReconciliationMismatch       = "reconciliation_mismatch"
	BlockCheckpointStale              = "checkpoint_stale"
	BlockReconciliationStale          = "reconciliation_stale"
	BlockInventoryInvalid             = "inventory_invalid"
	BlockUnavailableReason            = "valuation_unavailable"
)

// ReadinessInput contains only explicit, account-scoped evidence. Callers must
// load Account by ConfiguredAccountID; no default-account inference is valid.
type ReadinessInput struct {
	ConfiguredAccountID             uuid.UUID
	Account                         *domain.Account
	ScopeID                         uuid.UUID
	ScopeAccountID                  uuid.UUID
	EvidenceImmutable               bool
	ScopedArtifacts                 int
	LegacyArtifacts                 int
	ScopeMismatchCount              int
	MissingCanonicalLinks           int
	OpenLots                        int
	FreshMarks                      int
	StaleMarks                      int
	UnavailableMarks                int
	ReconciliationAvailable         bool
	ReconciliationPassed            bool
	ReconciliationAccountID         uuid.UUID
	ReconciliationVenue             string
	ReconciliationExternalAccountID string
	CheckpointGeneratedAt           time.Time
	ReconciliationGeneratedAt       time.Time
	GeneratedAt                     time.Time
	CheckpointMaxAge                time.Duration
	ReconciliationMaxAge            time.Duration
	UnavailableReasons              []string
}

type Readiness struct {
	accountID    uuid.UUID
	scopeID      uuid.UUID
	ready        bool
	blockReasons []string
}

func (r Readiness) AccountID() uuid.UUID   { return r.accountID }
func (r Readiness) ScopeID() uuid.UUID     { return r.scopeID }
func (r Readiness) Ready() bool            { return r.ready }
func (r Readiness) BlockReasons() []string { return append([]string(nil), r.blockReasons...) }

// EvaluateReadiness is the single fail-closed promotion/cutover predicate.
func EvaluateReadiness(input ReadinessInput) Readiness {
	reasons := make([]string, 0, 12)
	add := func(reason string) { reasons = append(reasons, reason) }
	if input.ConfiguredAccountID == uuid.Nil || input.Account == nil || input.Account.ID != input.ConfiguredAccountID {
		add(BlockConfiguredAccountUnavailable)
	} else if err := input.Account.Validate(); err != nil || !input.Account.PromotionEligible() || input.Account.Status != domain.AccountStatusActive {
		add(BlockConfiguredAccountUntrusted)
	}
	if input.ScopeID == uuid.Nil || input.ScopeAccountID != input.ConfiguredAccountID {
		add(BlockEvidenceUnscoped)
	}
	if !input.EvidenceImmutable {
		add(BlockEvidenceMutable)
	}
	if input.ScopeMismatchCount != 0 {
		add(BlockScopeMismatch)
	}
	if input.ScopedArtifacts < 0 || input.LegacyArtifacts < 0 || input.ScopeMismatchCount < 0 || input.MissingCanonicalLinks < 0 || input.OpenLots < 0 || input.FreshMarks < 0 || input.StaleMarks < 0 || input.UnavailableMarks < 0 ||
		input.FreshMarks+input.StaleMarks+input.UnavailableMarks != input.OpenLots {
		add(BlockInventoryInvalid)
	}
	if input.MissingCanonicalLinks != 0 {
		add(BlockCanonicalLinksMissing)
	}
	if input.FreshMarks != input.OpenLots {
		add(BlockMarksIncomplete)
	}
	if input.StaleMarks != 0 {
		add(BlockMarksStale)
	}
	if input.UnavailableMarks != 0 {
		add(BlockMarksUnavailable)
	}
	if !input.ReconciliationAvailable {
		add(BlockReconciliationMissing)
	} else if !input.ReconciliationPassed || input.ReconciliationAccountID != input.ConfiguredAccountID || input.Account == nil || input.ReconciliationVenue != input.Account.Venue || input.ReconciliationExternalAccountID != input.Account.ExternalAccountID {
		add(BlockReconciliationMismatch)
	}
	if input.GeneratedAt.IsZero() || input.CheckpointMaxAge <= 0 || input.CheckpointGeneratedAt.IsZero() || input.CheckpointGeneratedAt.After(input.GeneratedAt) || input.GeneratedAt.Sub(input.CheckpointGeneratedAt) > input.CheckpointMaxAge {
		add(BlockCheckpointStale)
	}
	if input.GeneratedAt.IsZero() || input.ReconciliationMaxAge <= 0 || input.ReconciliationGeneratedAt.IsZero() || input.ReconciliationGeneratedAt.After(input.GeneratedAt) || input.GeneratedAt.Sub(input.ReconciliationGeneratedAt) > input.ReconciliationMaxAge {
		add(BlockReconciliationStale)
	}
	if len(input.UnavailableReasons) != 0 {
		add(BlockUnavailableReason)
	}
	sort.Strings(reasons)
	return Readiness{accountID: input.ConfiguredAccountID, scopeID: input.ScopeID, ready: len(reasons) == 0, blockReasons: reasons}
}

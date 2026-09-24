package venuerecon

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/venue"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

// InternalLedgerProvider is the venue of the canonical internal paper ledger
// account. It has no external broker, so its reconciliation compares the
// projection checkpoint against a self-capture of the same checkpoint.
const InternalLedgerProvider venue.Provider = "internal"

// InternalLedgerNamespace is the authoritative fill namespace for the internal
// provider. No lifecycle fills are compared: the internal ledger is the only
// source of its own fills, so the run attests checkpoint self-consistency and
// binds the checkpoint identity, not an external fill audit.
const InternalLedgerNamespace = "internal/ledger/self"

// internalLedgerRule is the provider rule used when the reviewed policy has
// no entry for the provider. It is not part of the pinned policy artifact.
var internalLedgerRule = ProviderRule{Provider: InternalLedgerProvider, AuthoritativeFillNamespace: InternalLedgerNamespace, SupportsRevisions: false}

// providerRuleFor resolves the reviewed policy rule for external venues and the
// internal-ledger rule for the internal provider.
func providerRuleFor(policy *Policy, provider venue.Provider) (ProviderRule, bool) {
	if policy != nil {
		if rule, ok := policy.ProviderRule(provider); ok {
			return rule, true
		}
	} else if rule, ok := mustPolicyProvider(provider); ok {
		return rule, true
	}
	if provider == InternalLedgerProvider {
		return internalLedgerRule, true
	}
	return ProviderRule{}, false
}

// InternalLedgerInput scopes one internal-ledger self-reconciliation to an
// exact verified checkpoint and its transaction membership.
type InternalLedgerInput struct {
	AccountID      uuid.UUID
	Checkpoint     *ledger.ProjectionCheckpoint
	TransactionIDs []uuid.UUID
	// HorizonStart must precede the checkpoint as-of; the checkpoint as-of is
	// the horizon end so readers can bind the run to the checkpoint.
	HorizonStart time.Time
	// CapturedAt is the UTC microsecond process time of the self-capture.
	CapturedAt time.Time
}

// InternalLedgerReconciliation is the complete evidence graph for one
// internal-ledger checkpoint: policy, provider self-capture, local snapshot,
// and the comparer's run.
type InternalLedgerReconciliation struct {
	Policy   *Policy
	Provider *StableProviderSnapshot
	Local    *LocalSnapshot
	Run      *Run
}

type internalLedgerPage struct {
	Schema           string `json:"schema"`
	CheckpointID     string `json:"checkpoint_id"`
	AsOf             string `json:"as_of"`
	InputChecksum    string `json:"input_checksum"`
	OutputChecksum   string `json:"output_checksum"`
	TransactionCount int    `json:"transaction_count"`
}

// NewInternalLedgerReconciliation builds a clean-or-not run for the internal
// ledger. The provider side is derived from the same checkpoint bytes as the
// local side, so the run passes only when the checkpoint is self-consistent
// and the transaction membership matches the checkpoint frontier.
func NewInternalLedgerReconciliation(input InternalLedgerInput) (*InternalLedgerReconciliation, error) {
	if input.Checkpoint == nil || input.AccountID == uuid.Nil {
		return nil, fmt.Errorf("internal ledger reconciliation requires an account and checkpoint")
	}
	if input.Checkpoint.AccountID != input.AccountID {
		return nil, fmt.Errorf("internal ledger checkpoint belongs to another account")
	}
	if !validEvidenceTime(input.CapturedAt) || !validEvidenceTime(input.HorizonStart) || !input.HorizonStart.Before(input.Checkpoint.AsOf) {
		return nil, fmt.Errorf("internal ledger reconciliation times must be UTC microseconds with horizon start before checkpoint as-of")
	}
	policy, err := NewPolicy(ReviewedPolicyV1Input())
	if err != nil {
		return nil, err
	}
	local, err := NewLocalSnapshot(LocalSnapshotInput{
		AccountID: input.AccountID, Provider: InternalLedgerProvider, Namespace: InternalLedgerNamespace,
		HorizonStart: input.HorizonStart, HorizonEnd: input.Checkpoint.AsOf, Checkpoint: input.Checkpoint,
		TransactionIDs: input.TransactionIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("internal ledger local snapshot: %w", err)
	}
	capture, err := newInternalLedgerCapture(input, local)
	if err != nil {
		return nil, err
	}
	admission, err := AdmitStableProviderSnapshot(capture, capture)
	if err != nil {
		return nil, err
	}
	if admission.Snapshot == nil {
		return nil, fmt.Errorf("internal ledger self-capture was not admitted")
	}
	run, err := Compare(CompareInput{Policy: policy, Provider: admission, Local: local, EquityBasisEquivalent: true})
	if err != nil {
		return nil, fmt.Errorf("internal ledger compare: %w", err)
	}
	return &InternalLedgerReconciliation{Policy: policy, Provider: admission.Snapshot, Local: local, Run: run}, nil
}

// newInternalLedgerCapture mirrors the local snapshot economics as a provider
// capture. The single page carries the checkpoint identity so the provider
// state digest changes whenever the checkpoint does. The internal account has
// no external account ID, so its own account UUID is the provider identity;
// promotion readiness accepts that identity for the internal venue.
func newInternalLedgerCapture(input InternalLedgerInput, local *LocalSnapshot) (*ProviderCapture, error) {
	checkpoint := input.Checkpoint
	raw, err := json.Marshal(internalLedgerPage{
		Schema: "internal-ledger-self-capture-v1", CheckpointID: checkpoint.ID.String(), AsOf: canonicalTime(checkpoint.AsOf),
		InputChecksum: checkpoint.InputChecksum, OutputChecksum: checkpoint.OutputChecksum, TransactionCount: checkpoint.TransactionCount,
	})
	if err != nil {
		return nil, err
	}
	pages, err := normalizePages([]RawPage{{Cursor: "", NextCursor: "", Terminal: true, Raw: raw}})
	if err != nil {
		return nil, err
	}
	positions := make([]ProviderPosition, 0, len(local.canonical.Positions))
	for _, position := range local.canonical.Positions {
		positions = append(positions, ProviderPosition{
			InstrumentID: position.InstrumentID, VenueContract: position.InstrumentID, ContractID: position.InstrumentID.String(),
			Quantity: position.Quantity, Currency: checkpoint.BaseCurrency, SourceAt: canonicalTime(checkpoint.AsOf),
		})
	}
	canonical := captureCanonical{
		Schema: providerCaptureSchemaV1, Provider: InternalLedgerProvider, Namespace: InternalLedgerNamespace,
		AccountID: input.AccountID.String(), Currency: checkpoint.BaseCurrency,
		HorizonStart: canonicalTime(input.HorizonStart), HorizonEnd: canonicalTime(checkpoint.AsOf),
		ProviderAsOf: canonicalTime(checkpoint.AsOf), Cash: local.canonical.Cash, Equity: local.canonical.Equity,
		Pages: pages, Positions: positions, Fills: []ProviderFill{},
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("marshal internal ledger capture: %w", err)
	}
	digest := sha256Hex(encoded)
	return &ProviderCapture{
		canonical: canonical, start: input.CapturedAt, end: input.CapturedAt, bytes: encoded, digest: digest,
		id: economicid.DeterministicUUID(providerCaptureDomain, providerCaptureSchemaV1+"@sha256:"+digest),
	}, nil
}

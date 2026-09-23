package venuerecon

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func validInternalLedgerInput(t *testing.T) InternalLedgerInput {
	t.Helper()
	return InternalLedgerInput{
		AccountID: localAccountID, Checkpoint: validLocalCheckpoint(t),
		TransactionIDs: []uuid.UUID{localTransaction2, localTransaction1},
		HorizonStart:   localAsOf.Add(-24 * time.Hour), CapturedAt: localAsOf.Add(time.Minute),
	}
}

func TestInternalLedgerReconciliationIsCleanAndBoundToCheckpoint(t *testing.T) {
	t.Parallel()
	input := validInternalLedgerInput(t)
	recon, err := NewInternalLedgerReconciliation(input)
	if err != nil {
		t.Fatal(err)
	}
	if !recon.Run.Clean || len(recon.Run.Incidents) != 0 {
		t.Fatalf("self-reconciliation must be clean: %+v", recon.Run)
	}
	if err := ValidatePersistableRun(recon.Run); err != nil {
		t.Fatalf("run must be persistable: %v", err)
	}
	if err := ValidateStableProviderSnapshot(recon.Provider); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLocalSnapshot(recon.Local); err != nil {
		t.Fatal(err)
	}
	if recon.Local.Provider() != InternalLedgerProvider || recon.Local.Namespace() != InternalLedgerNamespace {
		t.Fatalf("local scope = %s/%s", recon.Local.Provider(), recon.Local.Namespace())
	}
	if recon.Local.CheckpointID() != input.Checkpoint.ID || !recon.Local.HorizonEnd().Equal(input.Checkpoint.AsOf) {
		t.Fatal("local snapshot must bind the checkpoint identity at horizon end")
	}
	capture := recon.Provider.Capture()
	if capture.AccountID() != localAccountID.String() || capture.Provider() != InternalLedgerProvider || len(capture.Pages()) != 1 || len(capture.Positions()) != len(recon.Local.Positions()) {
		t.Fatalf("internal capture scope is wrong: %+v", capture.canonical)
	}
	if recon.Run.ProviderSnapshotID != recon.Provider.ID() || recon.Run.LocalSnapshotID != recon.Local.ID() {
		t.Fatal("run must reference the internal provider and local snapshots")
	}
	matchedSnapshot := false
	for _, result := range recon.Run.Results {
		if result.Key == "snapshot" && result.Reason == ReasonSnapshotMatched {
			matchedSnapshot = true
		}
	}
	if !matchedSnapshot {
		t.Fatalf("expected snapshot_matched result: %+v", recon.Run.Results)
	}

	again, err := NewInternalLedgerReconciliation(input)
	if err != nil || again.Run.ID != recon.Run.ID || again.Provider.ID() != recon.Provider.ID() || again.Local.ID() != recon.Local.ID() {
		t.Fatal("internal reconciliation identities must be deterministic")
	}
}

func TestInternalLedgerReconciliationRejectsScopeDrift(t *testing.T) {
	t.Parallel()
	input := validInternalLedgerInput(t)
	input.TransactionIDs = input.TransactionIDs[:1]
	if _, err := NewInternalLedgerReconciliation(input); err == nil {
		t.Fatal("membership count mismatch must fail")
	}
	input = validInternalLedgerInput(t)
	input.AccountID = uuid.New()
	if _, err := NewInternalLedgerReconciliation(input); err == nil {
		t.Fatal("foreign account must fail")
	}
	input = validInternalLedgerInput(t)
	input.HorizonStart = input.Checkpoint.AsOf
	if _, err := NewInternalLedgerReconciliation(input); err == nil {
		t.Fatal("horizon start at as-of must fail")
	}
	input = validInternalLedgerInput(t)
	input.CapturedAt = input.CapturedAt.Add(time.Nanosecond)
	if _, err := NewInternalLedgerReconciliation(input); err == nil {
		t.Fatal("sub-microsecond capture time must fail")
	}
}

func TestExternalProvidersStillRequireReviewedPolicyRules(t *testing.T) {
	t.Parallel()
	if _, ok := providerRuleFor(nil, "coinbase"); ok {
		t.Fatal("unknown provider must not resolve")
	}
	rule, ok := providerRuleFor(nil, InternalLedgerProvider)
	if !ok || rule.AuthoritativeFillNamespace != InternalLedgerNamespace || rule.SupportsRevisions {
		t.Fatalf("internal rule = %+v", rule)
	}
	policy, err := NewPolicy(ReviewedPolicyV1Input())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := policy.ProviderRule(InternalLedgerProvider); ok {
		t.Fatal("the pinned reviewed policy must not gain an internal provider")
	}
}

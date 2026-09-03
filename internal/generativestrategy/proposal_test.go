package generativestrategy

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type proposalStore struct {
	family  *strategycatalog.Family
	spec    *Spec
	version *strategycatalog.Version
	receipt *Receipt
}

func (store *proposalStore) RegisterStrategyFamily(_ context.Context, family *strategycatalog.Family) (*strategycatalog.Family, error) {
	store.family = family
	return family, nil
}

func (store *proposalStore) RegisterCompilation(_ context.Context, spec *Spec, version *strategycatalog.Version, receipt *Receipt) (*Spec, *strategycatalog.Version, *Receipt, error) {
	store.spec, store.version, store.receipt = spec, version, receipt
	return spec, version, receipt, nil
}

func (store *proposalStore) GetCompilation(context.Context, uuid.UUID) (*Spec, *strategycatalog.Version, *Receipt, error) {
	return store.spec, store.version, store.receipt, nil
}

func TestProposalServiceRecordsOnlyTypedInactiveCompilation(t *testing.T) {
	_, input := specFixture(t)
	store := &proposalStore{}
	service, err := NewProposalService(store)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := service.Propose(context.Background(), ProposalRequest{Input: input, SourceCommit: strings.Repeat("b", 40), SourceTreeSHA256: strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if store.family != input.Family || proposal.Spec != store.spec || proposal.Version != store.version || proposal.Receipt != store.receipt || proposal.Version.FamilyID() != input.Family.ID() ||
		strings.Contains(string(proposal.Version.Config()), "deployment") || strings.Contains(string(proposal.Version.Config()), "active") {
		t.Fatalf("proposal=%+v config=%s", proposal, proposal.Version.Config())
	}
}

func TestProposalServiceRejectsInvalidTypedOutputBeforeWrite(t *testing.T) {
	_, input := specFixture(t)
	input.ProhibitedBehaviors = nil
	store := &proposalStore{}
	service, _ := NewProposalService(store)
	if proposal, err := service.Propose(context.Background(), ProposalRequest{Input: input, SourceCommit: strings.Repeat("b", 40), SourceTreeSHA256: strings.Repeat("c", 64)}); err == nil || proposal != nil || store.spec != nil {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}
}

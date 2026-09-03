package generativestrategy

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type ProposalRequest struct {
	Input            SpecInput
	SourceCommit     string
	SourceTreeSHA256 string
}

type Proposal struct {
	Spec    *Spec
	Version *strategycatalog.Version
	Receipt *Receipt
}

type ProposalStore interface {
	RegisterStrategyFamily(context.Context, *strategycatalog.Family) (*strategycatalog.Family, error)
	RegisterCompilation(context.Context, *Spec, *strategycatalog.Version, *Receipt) (*Spec, *strategycatalog.Version, *Receipt, error)
}

type ProposalService struct{ store ProposalStore }

func NewProposalService(store ProposalStore) (*ProposalService, error) {
	if store == nil {
		return nil, fmt.Errorf("generated proposal store is required")
	}
	return &ProposalService{store: store}, nil
}

// Propose validates an entire typed model proposal, compiles its immutable
// inactive version, and records the exact graph. It cannot create an
// experiment, deployment, schedule, allocation, or order.
func (service *ProposalService) Propose(ctx context.Context, request ProposalRequest) (*Proposal, error) {
	if service == nil || service.store == nil || request.Input.Family == nil {
		return nil, fmt.Errorf("generated proposal requires family, source identity, and store")
	}
	spec, err := NewSpec(request.Input)
	if err != nil {
		return nil, fmt.Errorf("validate generated proposal: %w", err)
	}
	family, err := service.store.RegisterStrategyFamily(ctx, request.Input.Family)
	if err != nil {
		return nil, fmt.Errorf("record generated proposal family: %w", err)
	}
	if family == nil || family.ID() != request.Input.Family.ID() || family.Digest() != request.Input.Family.Digest() {
		return nil, fmt.Errorf("persisted generated proposal family diverged")
	}
	version, receipt, err := Compile(spec, request.SourceCommit, request.SourceTreeSHA256)
	if err != nil {
		return nil, fmt.Errorf("compile generated proposal: %w", err)
	}
	persistedSpec, persistedVersion, persistedReceipt, err := service.store.RegisterCompilation(ctx, spec, version, receipt)
	if err != nil {
		return nil, fmt.Errorf("record generated proposal: %w", err)
	}
	if persistedSpec == nil || persistedVersion == nil || persistedReceipt == nil || persistedSpec.ID() != spec.ID() || persistedSpec.Digest() != spec.Digest() ||
		persistedVersion.ID() != version.ID() || persistedVersion.Digest() != version.Digest() || persistedReceipt.ID() != receipt.ID() || persistedReceipt.Digest() != receipt.Digest() {
		return nil, fmt.Errorf("persisted generated proposal diverged")
	}
	return &Proposal{Spec: persistedSpec, Version: persistedVersion, Receipt: persistedReceipt}, nil
}

type EligibleProposal struct {
	Key     string
	Request ProposalRequest
}

type EligibleProposalSource interface {
	ListEligibleGeneratedProposals(context.Context, uuid.UUID, uuid.UUID, int) ([]EligibleProposal, error)
}

type ProposalBatchSummary struct {
	Eligible  int
	Completed int
	Failed    int
}

type ProposalBatchService struct {
	source   EligibleProposalSource
	proposal *ProposalService
}

func NewProposalBatchService(source EligibleProposalSource, proposal *ProposalService) (*ProposalBatchService, error) {
	if source == nil || proposal == nil {
		return nil, fmt.Errorf("generated proposal batch source and service are required")
	}
	return &ProposalBatchService{source: source, proposal: proposal}, nil
}

func (service *ProposalBatchService) RunEligible(ctx context.Context, accountID, scopeID uuid.UUID, limit int) (ProposalBatchSummary, error) {
	summary := ProposalBatchSummary{}
	if service == nil || service.source == nil || service.proposal == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > MaximumResearchBatchSize {
		return summary, fmt.Errorf("generated proposal batch requires exact account, scope, and bounded limit")
	}
	items, err := service.source.ListEligibleGeneratedProposals(ctx, accountID, scopeID, limit)
	if err != nil {
		return summary, fmt.Errorf("list eligible generated proposals: %w", err)
	}
	if len(items) > limit {
		return summary, fmt.Errorf("generated proposal source exceeded requested batch limit")
	}
	summary.Eligible = len(items)
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		if item.Key == "" || item.Key != item.Request.Input.SpecKey || item.Request.Input.Family == nil {
			summary.Failed++
			return summary, fmt.Errorf("generated proposal work item %d is incomplete", index)
		}
		if _, duplicate := seen[item.Key]; duplicate {
			summary.Failed++
			return summary, fmt.Errorf("generated proposal key %q is duplicated", item.Key)
		}
		seen[item.Key] = struct{}{}
	}
	for index, item := range items {
		if _, err := service.proposal.Propose(ctx, item.Request); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated proposal work item %d: %w", index, err)
		}
		summary.Completed++
	}
	return summary, nil
}

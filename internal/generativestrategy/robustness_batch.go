package generativestrategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/robustness"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type EligibleRobustnessAssessment struct {
	Key        string
	Family     *robustness.Family
	Policy     *robustness.Policy
	ScopeID    uuid.UUID
	Mode       strategycatalog.ExperimentMode
	Candidates []robustness.CandidateInput
}

type EligibleRobustnessSource interface {
	ListEligibleGeneratedRobustness(context.Context, uuid.UUID, uuid.UUID, int) ([]EligibleRobustnessAssessment, error)
}

type RobustnessStore interface {
	RegisterPolicy(context.Context, *robustness.Policy) (*robustness.Policy, error)
	RegisterFamily(context.Context, *robustness.Family) (*robustness.Family, error)
	RecordAssessment(context.Context, *robustness.Assessment) (*robustness.Assessment, error)
}

type RobustnessBatchService struct {
	source EligibleRobustnessSource
	store  RobustnessStore
}

func NewRobustnessBatchService(source EligibleRobustnessSource, store RobustnessStore) (*RobustnessBatchService, error) {
	if source == nil || store == nil {
		return nil, fmt.Errorf("generated robustness source and store are required")
	}
	return &RobustnessBatchService{source: source, store: store}, nil
}

// RunEligible records only complete deterministic reviewed assessments. It
// does not create a deployment, promotion decision, schedule, or order.
func (service *RobustnessBatchService) RunEligible(ctx context.Context, accountID, scopeID uuid.UUID, limit int) (BatchSummary, error) {
	summary := BatchSummary{}
	if service == nil || service.source == nil || service.store == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > MaximumResearchBatchSize {
		return summary, fmt.Errorf("generated robustness batch requires exact account, scope, and bounded limit")
	}
	items, err := service.source.ListEligibleGeneratedRobustness(ctx, accountID, scopeID, limit)
	if err != nil {
		return summary, fmt.Errorf("list eligible generated robustness: %w", err)
	}
	if len(items) > limit {
		return summary, fmt.Errorf("generated robustness source exceeded requested batch limit")
	}
	summary.Eligible = len(items)
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		if item.Key == "" || item.Key != strings.TrimSpace(item.Key) || item.Family == nil || item.Policy == nil || item.ScopeID != scopeID ||
			item.Mode != strategycatalog.ExperimentPaperScored || len(item.Candidates) == 0 {
			summary.Failed++
			return summary, fmt.Errorf("generated robustness work item %d is invalid", index)
		}
		if _, duplicate := seen[item.Key]; duplicate {
			summary.Failed++
			return summary, fmt.Errorf("generated robustness key %q is duplicated", item.Key)
		}
		seen[item.Key] = struct{}{}
		assessment, buildErr := robustness.NewAssessment(robustness.AssessmentInput{
			Family: item.Family, Policy: item.Policy, ScopeID: item.ScopeID, Mode: item.Mode, Candidates: item.Candidates,
		})
		if buildErr != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated robustness work item %d: %w", index, buildErr)
		}
		registeredPolicy, registerErr := service.store.RegisterPolicy(ctx, item.Policy)
		if registerErr != nil || registeredPolicy == nil || registeredPolicy.ID() != item.Policy.ID() || registeredPolicy.Digest() != item.Policy.Digest() {
			summary.Failed++
			return summary, fmt.Errorf("generated robustness work item %d policy registration diverged: %v", index, registerErr)
		}
		registeredFamily, registerErr := service.store.RegisterFamily(ctx, item.Family)
		if registerErr != nil || registeredFamily == nil || registeredFamily.ID() != item.Family.ID() || registeredFamily.Digest() != item.Family.Digest() {
			summary.Failed++
			return summary, fmt.Errorf("generated robustness work item %d family registration diverged: %v", index, registerErr)
		}
		recorded, recordErr := service.store.RecordAssessment(ctx, assessment)
		if recordErr != nil || recorded == nil || recorded.ID() != assessment.ID() || recorded.Digest() != assessment.Digest() || recorded.ScopeID() != scopeID {
			summary.Failed++
			return summary, fmt.Errorf("generated robustness work item %d assessment registration diverged: %v", index, recordErr)
		}
		summary.Completed++
	}
	return summary, nil
}

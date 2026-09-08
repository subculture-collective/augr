package generativestrategy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type EligibleDeploymentProposal struct {
	Key             string
	ScopeID         uuid.UUID
	RiskPolicy      *portfolio.PortfolioRiskPolicy
	RiskEffectiveAt time.Time
	Deployment      *strategycatalog.Deployment
}

type EligibleDeploymentSource interface {
	ListEligibleGeneratedDeployments(context.Context, uuid.UUID, uuid.UUID, int) ([]EligibleDeploymentProposal, error)
}

type DeploymentProposalStore interface {
	RegisterPortfolioRiskPolicy(context.Context, *portfolio.PortfolioRiskPolicy) error
	BindPortfolioRiskPolicy(context.Context, uuid.UUID, *portfolio.PortfolioRiskPolicy, time.Time) (uuid.UUID, error)
	ProposeGeneratedDeployment(context.Context, *strategycatalog.Deployment) (*strategycatalog.Deployment, error)
}

type DeploymentBatchService struct {
	source EligibleDeploymentSource
	store  DeploymentProposalStore
}

func NewDeploymentBatchService(source EligibleDeploymentSource, store DeploymentProposalStore) (*DeploymentBatchService, error) {
	if source == nil || store == nil {
		return nil, fmt.Errorf("generated deployment source and store are required")
	}
	return &DeploymentBatchService{source: source, store: store}, nil
}

// RunEligible persists the exact reviewed risk binding and paper-scored
// deployment proposal. Promotion and activation remain separate authorities.
func (service *DeploymentBatchService) RunEligible(ctx context.Context, accountID, scopeID uuid.UUID, limit int) (BatchSummary, error) {
	summary := BatchSummary{}
	if service == nil || service.source == nil || service.store == nil || accountID == uuid.Nil || scopeID == uuid.Nil || limit <= 0 || limit > MaximumResearchBatchSize {
		return summary, fmt.Errorf("generated deployment batch requires exact account, scope, and bounded limit")
	}
	items, err := service.source.ListEligibleGeneratedDeployments(ctx, accountID, scopeID, limit)
	if err != nil {
		return summary, fmt.Errorf("list eligible generated deployments: %w", err)
	}
	if len(items) > limit {
		return summary, fmt.Errorf("generated deployment source exceeded requested batch limit")
	}
	summary.Eligible = len(items)
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		deployment := item.Deployment
		if item.Key == "" || item.Key != strings.TrimSpace(item.Key) || item.ScopeID != scopeID || item.RiskPolicy == nil ||
			item.RiskEffectiveAt.IsZero() || item.RiskEffectiveAt.Location() != time.UTC || !item.RiskEffectiveAt.Equal(item.RiskEffectiveAt.Truncate(time.Microsecond)) ||
			deployment == nil || deployment.AccountID() != accountID || deployment.Mode() != strategycatalog.ExperimentPaperScored ||
			deployment.RiskPolicyVersion() != item.RiskPolicy.Reference() {
			summary.Failed++
			return summary, fmt.Errorf("generated deployment work item %d is invalid", index)
		}
		if _, duplicate := seen[item.Key]; duplicate {
			summary.Failed++
			return summary, fmt.Errorf("generated deployment key %q is duplicated", item.Key)
		}
		seen[item.Key] = struct{}{}
		if err := service.store.RegisterPortfolioRiskPolicy(ctx, item.RiskPolicy); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated deployment work item %d register risk policy: %w", index, err)
		}
		if _, err := service.store.BindPortfolioRiskPolicy(ctx, deployment.CapitalBindingID(), item.RiskPolicy, item.RiskEffectiveAt); err != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated deployment work item %d bind risk policy: %w", index, err)
		}
		persisted, err := service.store.ProposeGeneratedDeployment(ctx, deployment)
		if err != nil {
			summary.Failed++
			return summary, fmt.Errorf("generated deployment work item %d persist proposal: %w", index, err)
		}
		if persisted == nil || persisted.ID() != deployment.ID() || persisted.Digest() != deployment.Digest() {
			summary.Failed++
			return summary, fmt.Errorf("generated deployment work item %d persisted proposal diverged", index)
		}
		summary.Completed++
	}
	return summary, nil
}

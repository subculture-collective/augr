package postgres

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
)

// ExecutionOrderPreparation contains the evidence supplied by the decision,
// allocation, risk, and routing owners. RiskApproval must be the result of an
// actual admission for AllocatedQuantity, not an approval synthesized by a
// caller merely to obtain a route. Reference facts must already be persisted.
type ExecutionOrderPreparation struct {
	Proposal          lifecycle.ProposeInput
	AllocatedQuantity decimal.Decimal
	Allocation        lifecycle.EventInput
	RiskApproval      lifecycle.EventInput
	Route             lifecycle.RouteInput
}

// PrepareExecutionOrder validates the entire route before writing and persists
// its stages using the existing immutable, idempotent lifecycle operations.
// A database failure can leave a valid prefix; retrying the identical evidence
// completes that prefix without replacing it. This is not risk evaluation.
func (repo *ExecutionLifecycleRepo) PrepareExecutionOrder(ctx context.Context, input ExecutionOrderPreparation) (*lifecycle.Aggregate, error) {
	proposed, err := lifecycle.Propose(input.Proposal)
	if err != nil {
		return nil, fmt.Errorf("prepare execution proposal: %w", err)
	}
	allocation, err := lifecycle.Allocate(proposed, input.AllocatedQuantity, input.Allocation, input.Allocation.ReceivedAt)
	if err != nil {
		return nil, fmt.Errorf("prepare execution allocation: %w", err)
	}
	allocated, err := lifecycle.ApplyTransition(proposed, allocation)
	if err != nil {
		return nil, err
	}
	approval, err := lifecycle.ApproveRisk(allocated, input.RiskApproval, input.RiskApproval.ReceivedAt)
	if err != nil {
		return nil, fmt.Errorf("prepare execution risk admission: %w", err)
	}
	approved, err := lifecycle.ApplyTransition(allocated, approval)
	if err != nil {
		return nil, err
	}
	route, err := lifecycle.Route(approved, input.Route)
	if err != nil {
		return nil, fmt.Errorf("prepare execution route: %w", err)
	}
	if _, err := lifecycle.ApplyTransition(approved, route); err != nil {
		return nil, err
	}
	current, err := repo.ProposeExecutionIntent(ctx, proposed)
	if err != nil {
		return nil, err
	}
	for _, transition := range []*lifecycle.Transition{allocation, approval, route} {
		current, err = repo.ApplyExecutionTransition(ctx, proposed.Intent.AccountID, transition)
		if err != nil {
			return nil, fmt.Errorf("persist execution preparation %s: %w", transition.Event.Kind, err)
		}
	}
	return current, nil
}

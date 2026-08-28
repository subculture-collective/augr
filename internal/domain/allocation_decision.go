package domain

import (
	"time"

	"github.com/google/uuid"
)

// AllocationDecisionMode identifies how the allocator was running.
type AllocationDecisionMode string

const (
	AllocationDecisionModeShadow AllocationDecisionMode = "shadow"
	AllocationDecisionModePaper  AllocationDecisionMode = "paper"
)

// String returns the string representation of an AllocationDecisionMode.
func (m AllocationDecisionMode) String() string { return string(m) }

// AllocationDecisionAction captures the allocator outcome.
type AllocationDecisionAction string

const (
	AllocationDecisionActionShadowSelected    AllocationDecisionAction = "shadow_selected"
	AllocationDecisionActionShadowRejected    AllocationDecisionAction = "shadow_rejected"
	AllocationDecisionActionPaperOrderIntent  AllocationDecisionAction = "paper_order_intent"
	AllocationDecisionActionExecutionRejected AllocationDecisionAction = "execution_rejected"
	AllocationDecisionActionExecuted          AllocationDecisionAction = "executed"
)

// String returns the string representation of an AllocationDecisionAction.
func (a AllocationDecisionAction) String() string { return string(a) }

// AllocationDecision records a single allocator outcome.
type AllocationDecision struct {
	ID               uuid.UUID                `json:"id"`
	AccountID        uuid.UUID                `json:"account_id,omitzero"`
	Environment      AccountEnvironment       `json:"environment,omitempty"`
	OriginType       string                   `json:"origin_type,omitempty"`
	OriginID         string                   `json:"origin_id,omitempty"`
	OpportunityID    *uuid.UUID               `json:"opportunity_id,omitempty"`
	StrategyID       *uuid.UUID               `json:"strategy_id,omitempty"`
	Mode             AllocationDecisionMode   `json:"mode"`
	Action           AllocationDecisionAction `json:"action"`
	Score            float64                  `json:"score"`
	NotionalUSD      float64                  `json:"notional_usd"`
	Quantity         float64                  `json:"quantity"`
	Reasons          []string                 `json:"reasons"`
	CreatedOrderID   *uuid.UUID               `json:"created_order_id,omitempty"`
	ExecutionClaimID uuid.UUID                `json:"-"`
	CreatedAt        time.Time                `json:"created_at"`
}

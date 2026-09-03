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
	ID                   uuid.UUID                `json:"id"`
	AccountID            uuid.UUID                `json:"account_id,omitzero"`
	Environment          AccountEnvironment       `json:"environment,omitempty"`
	OriginType           string                   `json:"origin_type,omitempty"`
	OriginID             string                   `json:"origin_id,omitempty"`
	PipelineRunID        *uuid.UUID               `json:"pipeline_run_id,omitempty"`
	PipelineRunTradeDate *time.Time               `json:"pipeline_run_trade_date,omitempty"`
	OpportunityID        *uuid.UUID               `json:"opportunity_id,omitempty"`
	StrategyID           *uuid.UUID               `json:"strategy_id,omitempty"`
	Mode                 AllocationDecisionMode   `json:"mode"`
	Action               AllocationDecisionAction `json:"action"`
	Score                float64                  `json:"score"`
	NotionalUSD          float64                  `json:"notional_usd"`
	Quantity             float64                  `json:"quantity"`
	RiskPolicyVersion    string                   `json:"risk_policy_version,omitempty"`
	AccountSnapshotID    uuid.UUID                `json:"account_snapshot_id,omitempty"`
	ProposedQuantity     float64                  `json:"proposed_quantity"`
	MaxLossPerUnit       float64                  `json:"max_loss_per_unit"`
	ReservedRiskUSD      float64                  `json:"reserved_risk_usd"`
	ReservedCapitalUSD   float64                  `json:"reserved_capital_usd"`
	ExposureBeforeUSD    float64                  `json:"exposure_before_usd"`
	ExposureAfterUSD     float64                  `json:"exposure_after_usd"`
	BindingConstraint    string                   `json:"binding_constraint,omitempty"`
	ExecutionRoute       string                   `json:"execution_route,omitempty"`
	RiskCaps             []AllocationRiskCap      `json:"risk_caps,omitempty"`
	Reasons              []string                 `json:"reasons"`
	CreatedOrderID       *uuid.UUID               `json:"created_order_id,omitempty"`
	ExecutionClaimID     uuid.UUID                `json:"-"`
	CreatedAt            time.Time                `json:"created_at"`
}

// AllocationRiskCap records one replayable sizing constraint. QuantityCap is
// expressed in shares/contracts for unit-based constraints and dollars for
// stock notional constraints.
type AllocationRiskCap struct {
	Sequence        int     `json:"sequence"`
	Name            string  `json:"name"`
	AvailableAmount float64 `json:"available_amount"`
	UnitAmount      float64 `json:"unit_amount"`
	QuantityCap     float64 `json:"quantity_cap"`
	Binding         bool    `json:"binding"`
}

package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// OpportunityStatus represents the lifecycle state of a portfolio opportunity.
type OpportunityStatus string

const (
	OpportunityStatusQueued   OpportunityStatus = "queued"
	OpportunityStatusSelected OpportunityStatus = "selected"
	OpportunityStatusRejected OpportunityStatus = "rejected"
	OpportunityStatusExpired  OpportunityStatus = "expired"
	OpportunityStatusExecuted OpportunityStatus = "executed"
)

// String returns the string representation of an OpportunityStatus.
func (s OpportunityStatus) String() string { return string(s) }

// Opportunity represents a persisted candidate for portfolio allocation.
type Opportunity struct {
	ID                   uuid.UUID              `json:"id"`
	AccountID            uuid.UUID              `json:"account_id,omitzero"`
	Environment          AccountEnvironment     `json:"environment,omitempty"`
	OriginType           string                 `json:"origin_type,omitempty"`
	OriginID             string                 `json:"origin_id,omitempty"`
	StrategyID           uuid.UUID              `json:"strategy_id"`
	PipelineRunID        *uuid.UUID             `json:"pipeline_run_id,omitempty"`
	PipelineRunTradeDate *time.Time             `json:"pipeline_run_trade_date,omitempty"`
	ExecutionVersionID   uuid.UUID              `json:"execution_version_id,omitempty"`
	EvaluationScopeID    uuid.UUID              `json:"evaluation_scope_id,omitempty"`
	ManifestID           uuid.UUID              `json:"manifest_id,omitempty"`
	QualityResultID      uuid.UUID              `json:"quality_result_id,omitempty"`
	DeploymentID         uuid.UUID              `json:"deployment_id,omitempty"`
	PromotionDecisionID  uuid.UUID              `json:"promotion_decision_id,omitempty"`
	CapitalBindingID     uuid.UUID              `json:"capital_binding_id,omitempty"`
	RiskPolicyVersion    string                 `json:"risk_policy_version,omitempty"`
	DeploymentBudgetUSD  float64                `json:"deployment_budget_usd"`
	MarketType           MarketType             `json:"market_type"`
	Ticker               string                 `json:"ticker"`
	Side                 OrderSide              `json:"side"`
	PredictionSide       string                 `json:"prediction_side,omitempty"`
	Signal               PipelineSignal         `json:"signal"`
	Status               OpportunityStatus      `json:"status"`
	Score                *float64               `json:"score,omitempty"`
	Confidence           float64                `json:"confidence"`
	EdgePct              float64                `json:"edge_pct"`
	ExpectedReturnPct    float64                `json:"expected_return_pct"`
	MaxLossPct           float64                `json:"max_loss_pct"`
	EntryPrice           float64                `json:"entry_price"`
	LiquidityUSD         float64                `json:"liquidity_usd"`
	MarketCapUSD         float64                `json:"market_cap_usd"`
	SpreadPct            float64                `json:"spread_pct"`
	ProposedNotional     float64                `json:"proposed_notional"`
	SelectedNotional     float64                `json:"selected_notional"`
	ExpectedLossUSD      float64                `json:"expected_loss_usd"`
	MaxLossPerUnit       float64                `json:"max_loss_per_unit"`
	RequiredCapitalUnit  float64                `json:"required_capital_per_unit"`
	QuoteObservedAt      *time.Time             `json:"quote_observed_at,omitempty"`
	Delta                float64                `json:"delta"`
	Gamma                float64                `json:"gamma"`
	Theta                float64                `json:"theta"`
	Vega                 float64                `json:"vega"`
	OptionLegs           []OpportunityOptionLeg `json:"option_legs,omitempty"`
	Reason               string                 `json:"reason"`
	RejectReason         string                 `json:"reject_reason"`
	Evidence             json.RawMessage        `json:"evidence,omitempty"`
	ExpiresAt            time.Time              `json:"expires_at"`
	CreatedAt            time.Time              `json:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at"`
	DedupeKey            string                 `json:"dedupe_key"`
}

// OpportunityOptionLeg is one normalized leg in an executable two-leg
// defined-risk package. Sequence is economically significant.
type OpportunityOptionLeg struct {
	Sequence          int       `json:"sequence"`
	ContractID        uuid.UUID `json:"contract_id"`
	ContractPayloadID uuid.UUID `json:"contract_payload_id"`
	ContractSHA256    string    `json:"contract_sha256"`
	QuotePayloadID    uuid.UUID `json:"quote_payload_id"`
	QuoteSHA256       string    `json:"quote_sha256"`
	SnapshotPayloadID uuid.UUID `json:"snapshot_payload_id"`
	SnapshotSHA256    string    `json:"snapshot_sha256"`
	OCCSymbol         string    `json:"occ_symbol"`
	Underlying        string    `json:"underlying"`
	Expiry            time.Time `json:"expiry"`
	OptionType        string    `json:"option_type"`
	Strike            float64   `json:"strike"`
	Ratio             int       `json:"ratio"`
	Side              OrderSide `json:"side"`
	PositionIntent    string    `json:"position_intent"`
	Bid               float64   `json:"bid"`
	Ask               float64   `json:"ask"`
	Multiplier        int       `json:"multiplier"`
}

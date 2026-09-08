package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PipelineStatus represents the execution status of a pipeline run.
type PipelineStatus string

const (
	PipelineStatusRunning   PipelineStatus = "running"
	PipelineStatusCompleted PipelineStatus = "completed"
	PipelineStatusFailed    PipelineStatus = "failed"
	PipelineStatusCancelled PipelineStatus = "cancelled"
)

// String returns the string representation of a PipelineStatus.
func (s PipelineStatus) String() string {
	return string(s)
}

// PipelineSignal represents the trading signal output of a pipeline run.
type PipelineSignal string

const (
	PipelineSignalBuy  PipelineSignal = "buy"
	PipelineSignalSell PipelineSignal = "sell"
	PipelineSignalHold PipelineSignal = "hold"
)

// String returns the string representation of a PipelineSignal.
func (s PipelineSignal) String() string {
	return string(s)
}

// IsValid returns true if the status is a defined PipelineStatus constant.
func (s PipelineStatus) IsValid() bool {
	switch s {
	case PipelineStatusRunning, PipelineStatusCompleted, PipelineStatusFailed, PipelineStatusCancelled:
		return true
	}
	return false
}

// validPipelineTransitions defines the legal status transitions.
var validPipelineTransitions = map[PipelineStatus][]PipelineStatus{
	PipelineStatusRunning:   {PipelineStatusCompleted, PipelineStatusFailed, PipelineStatusCancelled},
	PipelineStatusCompleted: {},
	PipelineStatusFailed:    {},
	PipelineStatusCancelled: {},
}

// CanTransitionTo returns true if transitioning from s to next is valid.
func (s PipelineStatus) CanTransitionTo(next PipelineStatus) bool {
	allowed, ok := validPipelineTransitions[s]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == next {
			return true
		}
	}
	return false
}

// IsValid returns true if the signal is a defined PipelineSignal constant.
func (s PipelineSignal) IsValid() bool {
	switch s {
	case PipelineSignalBuy, PipelineSignalSell, PipelineSignalHold:
		return true
	}
	return false
}

// PipelineRun represents a single execution of a trading strategy pipeline.
type PipelineRun struct {
	ID                  uuid.UUID          `json:"id"`
	AccountID           uuid.UUID          `json:"account_id,omitzero"`
	Environment         AccountEnvironment `json:"environment,omitempty"`
	OriginType          string             `json:"origin_type,omitempty"`
	OriginID            string             `json:"origin_id,omitempty"`
	ExecutionVersionID  uuid.UUID          `json:"execution_version_id,omitzero"`
	EvaluationScopeID   uuid.UUID          `json:"evaluation_scope_id,omitzero"`
	ManifestID          uuid.UUID          `json:"manifest_id,omitzero"`
	QualityResultID     uuid.UUID          `json:"quality_result_id,omitzero"`
	DeploymentID        uuid.UUID          `json:"deployment_id,omitzero"`
	PromotionDecisionID uuid.UUID          `json:"promotion_decision_id,omitzero"`
	CapitalBindingID    uuid.UUID          `json:"capital_binding_id,omitzero"`
	RiskPolicyVersion   string             `json:"risk_policy_version,omitempty"`
	StrategyID          uuid.UUID          `json:"strategy_id"`
	Ticker              string             `json:"ticker"`
	TradeDate           time.Time          `json:"trade_date"`
	Status              PipelineStatus     `json:"status"`
	Signal              PipelineSignal     `json:"signal,omitempty"`
	StartedAt           time.Time          `json:"started_at"`
	CompletedAt         *time.Time         `json:"completed_at,omitempty"`
	ErrorMessage        string             `json:"error_message,omitempty"`
	ConfigSnapshot      json.RawMessage    `json:"config_snapshot,omitempty"`
	PhaseTimings        json.RawMessage    `json:"phase_timings,omitempty"`
}

// PipelineRunSnapshot captures market data made available during a pipeline run.
type PipelineRunSnapshot struct {
	ID                   uuid.UUID          `json:"id"`
	AccountID            uuid.UUID          `json:"account_id,omitzero"`
	Environment          AccountEnvironment `json:"environment,omitempty"`
	OriginType           string             `json:"origin_type,omitempty"`
	OriginID             string             `json:"origin_id,omitempty"`
	PipelineRunID        uuid.UUID          `json:"pipeline_run_id"`
	PipelineRunTradeDate time.Time          `json:"pipeline_run_trade_date,omitzero"`
	DataType             string             `json:"data_type"`
	Payload              json.RawMessage    `json:"payload"`
	CreatedAt            time.Time          `json:"created_at"`
}

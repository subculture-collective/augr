package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ReplayEventType string

const (
	ReplayEventTypeDecisionCreated ReplayEventType = "decision_created"
	ReplayEventTypeRiskReviewed    ReplayEventType = "risk_reviewed"
	ReplayEventTypePaperOrdered    ReplayEventType = "paper_ordered"
	ReplayEventTypeLiveOrdered     ReplayEventType = "live_ordered"
	ReplayEventTypeFillObserved    ReplayEventType = "fill_observed"
	ReplayEventTypePositionUpdated ReplayEventType = "position_updated"
	ReplayEventTypeOutcomeResolved ReplayEventType = "outcome_resolved"
)

type ReplayEvent struct {
	ID              uuid.UUID       `json:"id"`
	TradeDecisionID uuid.UUID       `json:"trade_decision_id"`
	EventType       ReplayEventType `json:"event_type"`
	Source          string          `json:"source"`
	Payload         json.RawMessage `json:"payload"`
	OccurredAt      time.Time       `json:"occurred_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ReplayWorkbenchSummary struct {
	EventCount        int        `json:"event_count"`
	FirstEventAt      *time.Time `json:"first_event_at,omitempty"`
	LastEventAt       *time.Time `json:"last_event_at,omitempty"`
	HasPaperOrder     bool       `json:"has_paper_order"`
	HasLiveOrder      bool       `json:"has_live_order"`
	HasFill           bool       `json:"has_fill"`
	HasOutcome        bool       `json:"has_outcome"`
	LatestStatus      string     `json:"latest_status"`
	TotalApprovedSize float64    `json:"total_approved_size"`
	TotalNetEV        float64    `json:"total_net_ev"`
	RejectionCount    int        `json:"rejection_count"`
	RejectionReasons  []string   `json:"rejection_reasons,omitempty"`
}

type ReplayWorkbenchResponse struct {
	Source  TradeDecision          `json:"source"`
	Events  []ReplayEvent          `json:"events"`
	Summary ReplayWorkbenchSummary `json:"summary"`
}

package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	PolymarketDiscoveryStatusRunning   = "running"
	PolymarketDiscoveryStatusCompleted = "completed"
	PolymarketDiscoveryStatusFailed    = "failed"

	PolymarketDiscoveryPhaseScreen  = "screen"
	PolymarketDiscoveryPhasePropose = "propose"
	PolymarketDiscoveryPhaseDeploy  = "deploy"
	PolymarketDiscoveryPhaseDone    = "done"
)

type PolymarketDiscoveryCandidate struct {
	Slug             string          `json:"slug"`
	Question         string          `json:"question"`
	Description      string          `json:"description,omitempty"`
	Category         string          `json:"category,omitempty"`
	ConditionID      string          `json:"condition_id,omitempty"`
	EndDate          string          `json:"end_date,omitempty"`
	Volume24Hr       float64         `json:"volume_24hr,omitempty"`
	Liquidity        float64         `json:"liquidity,omitempty"`
	BestBid          float64         `json:"best_bid,omitempty"`
	BestAsk          float64         `json:"best_ask,omitempty"`
	Spread           float64         `json:"spread,omitempty"`
	LastTradePrice   float64         `json:"last_trade_price,omitempty"`
	ResolutionSource string          `json:"resolution_source,omitempty"`
	RawMarket        json.RawMessage `json:"raw_market,omitempty"`
}

type PolymarketDiscoveryAccepted struct {
	Candidate PolymarketDiscoveryCandidate `json:"candidate"`
	Proposal  json.RawMessage              `json:"proposal"`
}

type PolymarketDiscoveryDeployed struct {
	StrategyID string  `json:"strategy_id"`
	Slug       string  `json:"slug"`
	Template   string  `json:"template"`
	Name       string  `json:"name"`
	Direction  string  `json:"direction"`
	Conviction float64 `json:"conviction"`
	Reused     bool    `json:"reused"`
}

type PolymarketDiscoverySummary struct {
	FetchedAll int `json:"fetched_all,omitempty"`
	Screened   int `json:"screened,omitempty"`
	Proposed   int `json:"proposed,omitempty"`
	Skipped    int `json:"skipped,omitempty"`
	Accepted   int `json:"accepted,omitempty"`
	Deployed   int `json:"deployed,omitempty"`
}

type PolymarketDiscoveryRun struct {
	ID             uuid.UUID                      `json:"id"`
	Status         string                         `json:"status"`
	Phase          string                         `json:"phase"`
	CandidateIndex int                            `json:"candidate_index"`
	Candidates     []PolymarketDiscoveryCandidate `json:"candidates"`
	Accepted       []PolymarketDiscoveryAccepted  `json:"accepted"`
	Deployed       []PolymarketDiscoveryDeployed  `json:"deployed"`
	Errors         []string                       `json:"errors"`
	Summary        PolymarketDiscoverySummary     `json:"summary"`
	StartedAt      time.Time                      `json:"started_at"`
	UpdatedAt      time.Time                      `json:"updated_at"`
	CompletedAt    *time.Time                     `json:"completed_at,omitempty"`
}

func NewPolymarketDiscoveryRun() PolymarketDiscoveryRun {
	return PolymarketDiscoveryRun{
		ID:     uuid.New(),
		Status: PolymarketDiscoveryStatusRunning,
		Phase:  PolymarketDiscoveryPhaseScreen,
	}
}

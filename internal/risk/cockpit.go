package risk

import (
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// CockpitExposure summarizes risk activity for a single market type.
type CockpitExposure struct {
	MarketType        domain.MarketType `json:"market_type"`
	OpenPositions     int               `json:"open_positions"`
	MarkedPositions   int               `json:"marked_positions"`
	UnmarkedPositions int               `json:"unmarked_positions"`
	ApprovedDecisions int               `json:"approved_decisions"`
	RejectedDecisions int               `json:"rejected_decisions"`
	GrossExposure     float64           `json:"gross_exposure"`
	GrossMarkedValue  *float64          `json:"gross_marked_value"`
	NetExpectedValue  float64           `json:"net_expected_value"`
}

// DecisionCounts preserves journal history without treating it as active risk.
type DecisionCounts struct {
	Approved int `json:"approved"`
	Rejected int `json:"rejected"`
}

// CockpitSummary is the aggregated backend view for the risk cockpit.
type CockpitSummary struct {
	GeneratedAt              time.Time                            `json:"generated_at"`
	KillSwitchActive         bool                                 `json:"kill_switch_active"`
	CircuitBreaker           bool                                 `json:"circuit_breaker"`
	DecisionWindowStart      time.Time                            `json:"decision_window_start"`
	DecisionWindowEnd        time.Time                            `json:"decision_window_end"`
	Exposures                []CockpitExposure                    `json:"exposures"`
	HistoricalDecisionCounts map[domain.MarketType]DecisionCounts `json:"historical_decision_counts"`
	OpenPositions            int                                  `json:"open_positions"`
	MarkedPositions          int                                  `json:"marked_positions"`
	UnmarkedPositions        int                                  `json:"unmarked_positions"`
	GrossCostBasis           float64                              `json:"gross_cost_basis"`
	ValuationStatus          string                               `json:"valuation_status"`
	ReconciliationStatus     string                               `json:"reconciliation_status"`
	Warnings                 []string                             `json:"warnings"`
}

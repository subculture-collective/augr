package api

import (
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

type RiskCockpitSummary struct {
	Scope                    string                                    `json:"scope"`
	GeneratedAt              time.Time                                 `json:"generated_at"`
	KillSwitchActive         bool                                      `json:"kill_switch_active"`
	CircuitBreaker           bool                                      `json:"circuit_breaker"`
	DecisionWindowStart      time.Time                                 `json:"decision_window_start"`
	DecisionWindowEnd        time.Time                                 `json:"decision_window_end"`
	Exposures                []RiskDecisionActivity                    `json:"exposures"`
	HistoricalDecisionCounts map[domain.MarketType]risk.DecisionCounts `json:"historical_decision_counts"`
	Warnings                 []string                                  `json:"warnings"`
}

type RiskDecisionActivity struct {
	MarketType        domain.MarketType `json:"market_type"`
	ApprovedDecisions int               `json:"approved_decisions"`
	RejectedDecisions int               `json:"rejected_decisions"`
	NetExpectedValue  float64           `json:"net_expected_value"`
}

func (s *Server) handleRiskCockpit(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.risk == nil {
		respondError(w, http.StatusNotImplemented, "risk cockpit requires risk engine", ErrCodeNotImplemented)
		return
	}
	if s.tradeDecisions == nil {
		respondError(w, http.StatusNotImplemented, "risk cockpit requires trade decision journal repository", ErrCodeNotImplemented)
		return
	}
	generatedAt := time.Now().UTC()
	windowStart := time.Date(generatedAt.Year(), generatedAt.Month(), generatedAt.Day(), 0, 0, 0, 0, time.UTC)

	status, err := s.risk.GetStatus(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get risk status", ErrCodeInternal)
		return
	}

	historicalFilter := repository.TradeDecisionFilter{CreatedBefore: &generatedAt}
	historicalCount, err := s.tradeDecisions.Count(r.Context(), historicalFilter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to count trade decisions", ErrCodeInternal)
		return
	}

	historicalDecisions := make([]domain.TradeDecision, 0)
	if historicalCount > 0 {
		historicalDecisions, err = s.tradeDecisions.List(r.Context(), historicalFilter, historicalCount, 0)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to list trade decisions", ErrCodeInternal)
			return
		}
		if historicalDecisions == nil {
			historicalDecisions = []domain.TradeDecision{}
		}
	}

	currentFilter := repository.TradeDecisionFilter{CreatedAfter: &windowStart, CreatedBefore: &generatedAt}
	currentCount, err := s.tradeDecisions.Count(r.Context(), currentFilter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to count current-window trade decisions", ErrCodeInternal)
		return
	}
	currentDecisions := make([]domain.TradeDecision, 0)
	if currentCount > 0 {
		currentDecisions, err = s.tradeDecisions.List(r.Context(), currentFilter, currentCount, 0)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to list current-window trade decisions", ErrCodeInternal)
			return
		}
	}

	cockpit := risk.BuildCockpitSummaryWithHistory(currentDecisions, historicalDecisions, &status, windowStart, generatedAt)
	exposures := make([]RiskDecisionActivity, len(cockpit.Exposures))
	for index, exposure := range cockpit.Exposures {
		exposures[index] = RiskDecisionActivity{
			MarketType: exposure.MarketType, ApprovedDecisions: exposure.ApprovedDecisions,
			RejectedDecisions: exposure.RejectedDecisions, NetExpectedValue: exposure.NetExpectedValue,
		}
	}
	summary := RiskCockpitSummary{
		Scope: "legacy_unscoped", GeneratedAt: cockpit.GeneratedAt,
		KillSwitchActive: cockpit.KillSwitchActive, CircuitBreaker: cockpit.CircuitBreaker,
		DecisionWindowStart: cockpit.DecisionWindowStart, DecisionWindowEnd: cockpit.DecisionWindowEnd,
		Exposures: exposures, HistoricalDecisionCounts: cockpit.HistoricalDecisionCounts, Warnings: cockpit.Warnings,
	}
	respondJSON(w, http.StatusOK, summary)
}

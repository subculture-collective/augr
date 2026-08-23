package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

func (s *Server) handleRiskCockpit(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.risk == nil {
		respondError(w, http.StatusNotImplemented, "risk cockpit requires risk engine", ErrCodeNotImplemented)
		return
	}
	if s.tradeDecisions == nil {
		respondError(w, http.StatusNotImplemented, "risk cockpit requires trade decision journal repository", ErrCodeNotImplemented)
		return
	}
	if s.positions == nil {
		respondError(w, http.StatusNotImplemented, "risk cockpit requires position repository", ErrCodeNotImplemented)
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

	positions, openPositionCount, err := s.loadAllOpenPositions(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list open positions for risk cockpit", ErrCodeInternal)
		return
	}
	summary := risk.BuildCockpitSummaryWithPositionsAndHistory(currentDecisions, historicalDecisions, positions, &status, windowStart, generatedAt)
	if openPositionCount != len(positions) {
		summary.ReconciliationStatus = "incomplete"
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("risk aggregation loaded %d of %d open positions", len(positions), openPositionCount))
	}
	respondJSON(w, http.StatusOK, summary)
}

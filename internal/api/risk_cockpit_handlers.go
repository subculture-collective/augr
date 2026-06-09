package api

import (
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

	status, err := s.risk.GetStatus(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get risk status", ErrCodeInternal)
		return
	}

	count, err := s.tradeDecisions.Count(r.Context(), repository.TradeDecisionFilter{})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to count trade decisions", ErrCodeInternal)
		return
	}

	decisions := make([]domain.TradeDecision, 0)
	if count > 0 {
		decisions, err = s.tradeDecisions.List(r.Context(), repository.TradeDecisionFilter{}, count, 0)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to list trade decisions", ErrCodeInternal)
			return
		}
		if decisions == nil {
			decisions = []domain.TradeDecision{}
		}
	}

	summary := risk.BuildCockpitSummary(decisions, &status, time.Now().UTC())
	respondJSON(w, http.StatusOK, summary)
}

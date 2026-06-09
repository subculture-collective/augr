package api

import (
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func (s *Server) handleListTradeDecisions(w http.ResponseWriter, r *http.Request) {
	if s.tradeDecisions == nil {
		respondError(w, http.StatusNotImplemented, "trade decision journal repository is not configured", ErrCodeNotImplemented)
		return
	}

	limit, offset := parsePagination(r)
	q := r.URL.Query()

	filter := repository.TradeDecisionFilter{}

	if !ParseUUIDParam(w, q, "strategy_id", &filter.StrategyID) {
		return
	}
	if !ParseEnumParam(w, q, "market_type", &filter.MarketType) {
		return
	}
	if raw := q.Get("status"); raw != "" {
		filter.Status = domain.TradeDecisionStatus(raw)
		switch filter.Status {
		case domain.TradeDecisionStatusCandidate, domain.TradeDecisionStatusRejected,
			domain.TradeDecisionStatusPaper, domain.TradeDecisionStatusLive,
			domain.TradeDecisionStatusClosed:
		default:
			respondError(w, http.StatusBadRequest, "invalid status", ErrCodeBadRequest)
			return
		}
	}
	if !ParseTimeParam(w, q, "created_after", time.RFC3339, &filter.CreatedAfter) {
		return
	}
	if !ParseTimeParam(w, q, "created_before", time.RFC3339, &filter.CreatedBefore) {
		return
	}

	decisions, err := s.tradeDecisions.List(r.Context(), filter, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list trade decisions", ErrCodeInternal)
		return
	}
	if decisions == nil {
		decisions = []domain.TradeDecision{}
	}

	respondList(w, decisions, limit, offset)
}

func (s *Server) handleGetTradeDecision(w http.ResponseWriter, r *http.Request) {
	if s.tradeDecisions == nil {
		respondError(w, http.StatusNotImplemented, "trade decision journal repository is not configured", ErrCodeNotImplemented)
		return
	}

	id, err := parseUUID(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeBadRequest)
		return
	}

	decision, err := s.tradeDecisions.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			respondError(w, http.StatusNotFound, "trade decision not found", ErrCodeNotFound)
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to get trade decision", ErrCodeInternal)
		return
	}

	respondJSON(w, http.StatusOK, decision)
}

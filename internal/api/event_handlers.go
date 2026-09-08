package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	if s.events == nil {
		respondError(w, http.StatusNotImplemented, "events not configured", ErrCodeNotImplemented)
		return
	}
	limit, offset := parsePagination(r)
	q := r.URL.Query()

	filter := repository.AgentEventFilter{
		EventKind: q.Get("event_kind"),
	}
	rawRunID, hasRunID := q["pipeline_run_id"]
	rawTradeDate, hasTradeDate := q["pipeline_run_trade_date"]
	if hasRunID != hasTradeDate || hasRunID && (len(rawRunID) != 1 || len(rawTradeDate) != 1) {
		respondError(w, http.StatusBadRequest, "pipeline_run_id and pipeline_run_trade_date must be provided together", ErrCodeBadRequest)
		return
	}
	if hasRunID {
		runID, err := uuid.Parse(rawRunID[0])
		if err != nil || runID == uuid.Nil {
			respondError(w, http.StatusBadRequest, "invalid pipeline_run_id", ErrCodeBadRequest)
			return
		}
		tradeDate, err := time.Parse("2006-01-02", rawTradeDate[0])
		if err != nil || tradeDate.IsZero() {
			respondError(w, http.StatusBadRequest, "invalid pipeline_run_trade_date", ErrCodeBadRequest)
			return
		}
		filter.PipelineRunRef = &domain.PipelineRunRef{ID: runID, TradeDate: tradeDate}
	}
	if !ParseUUIDParam(w, q, "strategy_id", &filter.StrategyID) {
		return
	}
	if !ParseEnumParam(w, q, "agent_role", &filter.AgentRole) {
		return
	}
	if !ParseTimeParam(w, q, "after", time.RFC3339Nano, &filter.CreatedAfter) {
		return
	}
	if !ParseTimeParam(w, q, "before", time.RFC3339Nano, &filter.CreatedBefore) {
		return
	}

	events, err := s.events.List(r.Context(), filter, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list events", ErrCodeInternal)
		return
	}
	total, err := s.events.Count(r.Context(), filter)
	if err != nil {
		s.logger.Warn("count events", "error", err.Error())
	}
	respondListWithTotal(w, events, total, limit, offset)
}

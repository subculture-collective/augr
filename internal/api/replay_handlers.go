package api

import (
	"net/http"

	"github.com/PatrickFanella/get-rich-quick/internal/replay"
)

func (s *Server) handleGetReplayDecision(w http.ResponseWriter, r *http.Request) {
	if s.tradeDecisions == nil || s.replayEvents == nil {
		respondError(w, http.StatusNotImplemented, "replay workbench dependencies are not configured", ErrCodeNotImplemented)
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

	events, err := s.replayEvents.ListReplayEvents(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list replay events", ErrCodeInternal)
		return
	}

	respondJSON(w, http.StatusOK, replay.BuildWorkbench(*decision, events))
}

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

// SchedulerReloader is the subset of the strategy scheduler the API drives.
type SchedulerReloader interface {
	Reload(ctx context.Context) error
	RegisteredStrategyCount() int
}

// SchedulerReloadResponse reports how many strategy schedules are registered
// after a reload.
type SchedulerReloadResponse struct {
	Registered int `json:"registered"`
}

// handleSchedulerReload re-reads strategy schedules on demand.
// POST /api/v1/scheduler/reload (admin key required)
func (s *Server) handleSchedulerReload(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		respondError(w, http.StatusServiceUnavailable, "scheduler not configured", ErrCodeNotImplemented)
		return
	}
	if err := s.scheduler.Reload(r.Context()); err != nil {
		if errors.Is(err, scheduler.ErrNotStarted) {
			respondError(w, http.StatusConflict, "scheduler not started", ErrCodeConflict)
			return
		}
		s.logger.Error("scheduler reload failed", slog.String("error", err.Error()))
		respondError(w, http.StatusInternalServerError, "scheduler reload failed", ErrCodeInternal)
		return
	}
	s.writeAuditLog(r.Context(), actorOf(r), "scheduler.reload", "scheduler", nil, nil)
	respondJSON(w, http.StatusOK, SchedulerReloadResponse{Registered: s.scheduler.RegisteredStrategyCount()})
}

// reloadSchedulerAfterWrite applies a strategy create/update/delete to the
// running scheduler without waiting for the periodic reload. Failures are
// logged; the write already succeeded and the periodic reload will catch up.
func (s *Server) reloadSchedulerAfterWrite(ctx context.Context, action string) {
	if s.scheduler == nil {
		return
	}
	if err := s.scheduler.Reload(ctx); err != nil && !errors.Is(err, scheduler.ErrNotStarted) {
		s.logger.Warn("scheduler reload after strategy write failed",
			slog.String("action", action),
			slog.String("error", err.Error()))
	}
}

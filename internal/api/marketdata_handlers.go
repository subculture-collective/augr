package api

import (
	"context"
	"net/http"
	"time"
)

type MarketDataStatusSource interface {
	PolymarketStatus(ctx context.Context) (PolymarketStatus, error)
}

type PolymarketStatus struct {
	Enabled       bool      `json:"enabled"`
	WSConnections int       `json:"ws_connections"`
	AvgJitterMS   float64   `json:"avg_jitter_ms"`
	Dropped       uint64    `json:"dropped"`
	ReadySlugs    []string  `json:"ready_slugs"`
	RecorderLagS  float64   `json:"recorder_lag_seconds"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (s *Server) handlePolymarketStatus(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.mdStatusSrc == nil {
		respondError(w, http.StatusServiceUnavailable, "market data status unavailable", ErrCodeNotImplemented)
		return
	}
	status, err := s.mdStatusSrc.PolymarketStatus(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get market data status", ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusOK, status)
}

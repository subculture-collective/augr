package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleListNews returns recent news articles with LLM-derived triage data.
// GET /api/v1/news?limit=50&ticker=AAPL
func (s *Server) handleListNews(w http.ResponseWriter, r *http.Request) {
	if s.newsFeedRepo == nil {
		respondError(w, http.StatusServiceUnavailable, "news feed not configured", ErrCodeInternal)
		return
	}

	limit, _ := parsePagination(r)
	ticker := r.URL.Query().Get("ticker")

	if ticker != "" {
		items, err := s.newsFeedRepo.ListByTicker(r.Context(), ticker, limit)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to list news", ErrCodeInternal)
			return
		}
		respondJSON(w, http.StatusOK, items)
		return
	}

	items, err := s.newsFeedRepo.ListRecent(r.Context(), limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list news", ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusOK, items)
}

// handleGetSocialSentiment returns recent social sentiment snapshots for a ticker.
// GET /api/v1/social/sentiment/{ticker}?limit=20
func (s *Server) handleGetSocialSentiment(w http.ResponseWriter, r *http.Request) {
	if s.newsFeedRepo == nil {
		respondError(w, http.StatusServiceUnavailable, "news feed not configured", ErrCodeInternal)
		return
	}

	ticker := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "ticker")))
	if ticker == "" {
		respondError(w, http.StatusBadRequest, "ticker is required", ErrCodeValidation)
		return
	}

	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	items, err := s.newsFeedRepo.ListSocialSentimentByTicker(r.Context(), ticker, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list social sentiment", ErrCodeInternal)
		return
	}

	respondJSON(w, http.StatusOK, items)
}

package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// handleGetHistoricalOHLCV returns persisted OHLCV history for a ticker.
// GET /api/v1/market/ohlcv/{ticker}?timeframe=1d&from=YYYY-MM-DD&to=YYYY-MM-DD&provider=yahoo
func (s *Server) handleGetHistoricalOHLCV(w http.ResponseWriter, r *http.Request) {
	if s.marketDataHistory == nil {
		respondError(w, http.StatusServiceUnavailable, "market data history not configured", ErrCodeInternal)
		return
	}

	ticker := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "ticker")))
	if ticker == "" {
		respondError(w, http.StatusBadRequest, "ticker is required", ErrCodeValidation)
		return
	}

	timeframe := strings.TrimSpace(r.URL.Query().Get("timeframe"))
	if timeframe == "" {
		timeframe = "1d"
	}

	from, to, ok := parseHistoricalDateRange(w, r)
	if !ok {
		return
	}

	provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	filter := repository.HistoricalOHLCVFilter{
		Ticker:    ticker,
		Provider:  provider,
		Timeframe: timeframe,
		From:      from,
		To:        to,
	}

	bars, err := s.marketDataHistory.ListHistoricalOHLCV(r.Context(), filter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list historical ohlcv", ErrCodeInternal)
		return
	}

	respondJSON(w, http.StatusOK, bars)
}

func parseHistoricalDateRange(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	now := time.Now().UTC()
	from = now.AddDate(-1, 0, 0)
	to = now

	if v := strings.TrimSpace(r.URL.Query().Get("from")); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid 'from' format, expected YYYY-MM-DD", ErrCodeValidation)
			return time.Time{}, time.Time{}, false
		}
		from = parsed.UTC()
	}
	if v := strings.TrimSpace(r.URL.Query().Get("to")); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid 'to' format, expected YYYY-MM-DD", ErrCodeValidation)
			return time.Time{}, time.Time{}, false
		}
		to = endOfDayUTC(parsed)
	}

	from = startOfDayUTC(from)
	if to.Before(from) {
		respondError(w, http.StatusBadRequest, "'from' must be before or equal to 'to'", ErrCodeValidation)
		return time.Time{}, time.Time{}, false
	}

	return from, to, true
}

func startOfDayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func endOfDayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999_999_999, time.UTC)
}

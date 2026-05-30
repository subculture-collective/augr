package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

// handleListUniverse returns paginated universe tickers with optional filtering.
func (s *Server) handleListUniverse(w http.ResponseWriter, r *http.Request) {
	if s.universeRepo == nil {
		respondError(w, http.StatusServiceUnavailable, "universe not configured", ErrCodeInternal)
		return
	}

	limit, offset := parsePagination(r)
	q := r.URL.Query()

	filter := universe.ListFilter{
		IndexGroup: q.Get("index_group"),
		Search:     q.Get("search"),
	}

	tickers, err := s.universeRepo.List(r.Context(), filter, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list universe", ErrCodeInternal)
		return
	}
	respondList(w, tickers, limit, offset)
}

// handleGetWatchlist returns the top N scored tickers.
func (s *Server) handleGetWatchlist(w http.ResponseWriter, r *http.Request) {
	if s.universe == nil {
		respondError(w, http.StatusServiceUnavailable, "universe not configured", ErrCodeInternal)
		return
	}

	topN := 30
	if v := r.URL.Query().Get("top"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topN = n
		}
	}

	tickers, err := s.universe.GetWatchlist(r.Context(), topN)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get watchlist", ErrCodeInternal)
		return
	}

	if s.positions != nil && s.universeRepo != nil {
		positions, err := s.positions.GetOpen(r.Context(), repository.PositionFilter{}, maxLimit, 0)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to get open positions", ErrCodeInternal)
			return
		}
		tickers = s.mergeOpenPositionsIntoWatchlist(r.Context(), tickers, positions)
	}

	respondJSON(w, http.StatusOK, tickers)
}

func (s *Server) mergeOpenPositionsIntoWatchlist(ctx context.Context, tickers []universe.TrackedTicker, positions []domain.Position) []universe.TrackedTicker {
	seen := make(map[string]struct{}, len(tickers))
	for _, ticker := range tickers {
		seen[strings.ToUpper(strings.TrimSpace(ticker.Ticker))] = struct{}{}
	}

	for _, position := range positions {
		ticker := strings.ToUpper(strings.TrimSpace(position.Ticker))
		if ticker == "" {
			continue
		}
		if _, exists := seen[ticker]; exists {
			continue
		}

		tracked := universe.TrackedTicker{
			Ticker: ticker,
			Name:   "Current holding",
			Active: true,
		}
		if s.universeRepo != nil {
			matches, err := s.universeRepo.List(ctx, universe.ListFilter{Search: ticker}, 10, 0)
			if err == nil {
				for _, match := range matches {
					if strings.EqualFold(match.Ticker, ticker) {
						tracked = match
						break
					}
				}
			}
		}

		tickers = append(tickers, tracked)
		seen[ticker] = struct{}{}
	}

	return tickers
}

// handleRefreshUniverse triggers a full universe constituent refresh from Polygon.
func (s *Server) handleRefreshUniverse(w http.ResponseWriter, r *http.Request) {
	if s.universe == nil {
		respondError(w, http.StatusServiceUnavailable, "universe not configured", ErrCodeInternal)
		return
	}

	count, err := s.universe.RefreshConstituents(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "refresh failed: "+err.Error(), ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusOK, map[string]int{"count": count})
}

// handleRunPreMarketScan triggers a pre-market scan and returns scored tickers.
func (s *Server) handleRunPreMarketScan(w http.ResponseWriter, r *http.Request) {
	if s.universe == nil {
		respondError(w, http.StatusServiceUnavailable, "universe not configured", ErrCodeInternal)
		return
	}

	scored, err := s.universe.RunPreMarketScreen(r.Context(), 100000, 30)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "scan failed: "+err.Error(), ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusOK, scored)
}

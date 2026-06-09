package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/service"
)

func (s *Server) handleGetResearchOptionsOpportunities(w http.ResponseWriter, r *http.Request) {
	if s.researchSvc == nil {
		respondError(w, http.StatusNotImplemented, "research scanner service not configured", ErrCodeNotImplemented)
		return
	}

	underlying := strings.TrimSpace(chi.URLParam(r, "underlying"))
	if underlying == "" {
		respondError(w, http.StatusBadRequest, "underlying ticker is required", ErrCodeBadRequest)
		return
	}

	limit, err := parsePositiveIntQuery(r, "limit", 5)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	strategyID, err := parseOptionalUUIDQuery(r, "strategy_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	expiry, err := parseOptionalDateQuery(r, "expiry")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	optionType, err := parseOptionalOptionTypeQuery(r, "type")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}

	items, err := s.researchSvc.ScanOptions(r.Context(), service.OptionsOpportunityRequest{
		Underlying: underlying,
		StrategyID: strategyID,
		Limit:      limit,
		Expiry:     expiry,
		OptionType: optionType,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error(), ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusOK, ListResponse{Data: items, Total: len(items), Limit: limitOrDefault(limit), Offset: 0})
}

func (s *Server) handleGetResearchPolymarketOpportunities(w http.ResponseWriter, r *http.Request) {
	if s.researchSvc == nil {
		respondError(w, http.StatusNotImplemented, "research scanner service not configured", ErrCodeNotImplemented)
		return
	}

	limit, err := parsePositiveIntQuery(r, "limit", 5)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	strategyID, err := parseOptionalUUIDQuery(r, "strategy_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}

	bestBid, err := parseOptionalFloatQuery(r, "best_bid", true)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	bestAsk, err := parseOptionalFloatQuery(r, "best_ask", true)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	probability, err := parseOptionalFloatQuery(r, "probability", false)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	askDepthUSD, err := parseOptionalFloatQuery(r, "ask_depth_usd", true)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	askSize, err := parseOptionalFloatQuery(r, "ask_size", true)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}

	items, err := s.researchSvc.ScanPolymarket(r.Context(), service.PolymarketOpportunityRequest{
		Slug:        strings.TrimSpace(r.URL.Query().Get("slug")),
		TokenID:     strings.TrimSpace(r.URL.Query().Get("token_id")),
		Outcome:     strings.TrimSpace(r.URL.Query().Get("outcome")),
		StrategyID:  strategyID,
		Limit:       limit,
		Probability: probability,
		BestBid:     bestBid,
		BestAsk:     bestAsk,
		AskDepthUSD: askDepthUSD,
		AskSize:     askSize,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error(), ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusOK, ListResponse{Data: items, Total: len(items), Limit: limitOrDefault(limit), Offset: 0})
}

func parsePositiveIntQuery(r *http.Request, key string, def int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("invalid " + key)
	}
	return n, nil
}

func parseOptionalUUIDQuery(r *http.Request, key string) (*uuid.UUID, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid " + key)
	}
	return &id, nil
}

func parseOptionalDateQuery(r *http.Request, key string) (*time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, errors.New("invalid " + key)
	}
	return &t, nil
}

func parseOptionalOptionTypeQuery(r *http.Request, key string) (domain.OptionType, error) {
	raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get(key)))
	if raw == "" {
		return "", nil
	}
	switch raw {
	case "call":
		return domain.OptionTypeCall, nil
	case "put":
		return domain.OptionTypePut, nil
	default:
		return "", errors.New("invalid " + key)
	}
}

func parseOptionalFloatQuery(r *http.Request, key string, positiveOnly bool) (*float64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || isBadFloat(v, positiveOnly) {
		return nil, errors.New("invalid " + key)
	}
	return &v, nil
}

func limitOrDefault(limit int) int {
	if limit > 0 {
		return limit
	}
	return 5
}

func isBadFloat(v float64, positiveOnly bool) bool {
	if positiveOnly {
		return math.IsNaN(v) || math.IsInf(v, 0) || v <= 0
	}
	return math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v >= 1
}

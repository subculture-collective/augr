package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/google/uuid"
)

// ReportMetrics captures report staleness observations.
type ReportMetrics interface {
	ObserveReportStaleness(strategyID string, seconds float64)
}

// ReportArtifactStore captures report artifact reads used by report handlers.
type ReportArtifactStore interface {
	List(ctx context.Context, filter pgrepo.ReportArtifactFilter, limit, offset int) ([]pgrepo.ReportArtifact, error)
}

type PaperEvaluationScopeStore interface {
	RegisterScope(context.Context, *pgrepo.PaperEvaluationScope) error
	ValidateBacktestConfigScope(context.Context, *domain.BacktestConfig) error
	ScopedExecutionBinding(context.Context, uuid.UUID) (bool, string, error)
}

func (s *Server) handleCreatePaperEvaluationScope(w http.ResponseWriter, r *http.Request) {
	if s.paperEvaluationScopes == nil {
		respondError(w, http.StatusNotImplemented, "paper evaluation scopes not configured", ErrCodeNotImplemented)
		return
	}
	var input pgrepo.PaperEvaluationScope
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body", ErrCodeBadRequest)
		return
	}
	scope, err := pgrepo.NewPaperEvaluationScope(input)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeValidation)
		return
	}
	if err := s.paperEvaluationScopes.RegisterScope(r.Context(), scope); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to register paper evaluation scope", ErrCodeInternal)
		return
	}
	respondJSON(w, http.StatusCreated, scope)
}

// reportLatestResponse wraps the latest report artifact with a stale_seconds
// field showing how old the report is.
type reportLatestResponse struct {
	pgrepo.ReportArtifact
	StaleSeconds float64 `json:"stale_seconds"`
}

func reportScopeFilter(r *http.Request) (*uuid.UUID, *uuid.UUID, bool, error) {
	if r.URL.Query().Get("legacy") == "legacy_unscoped" {
		return nil, nil, true, nil
	}
	accountID, err := uuid.Parse(r.URL.Query().Get("account_id"))
	if err != nil {
		return nil, nil, false, err
	}
	scopeID, err := uuid.Parse(r.URL.Query().Get("scope_id"))
	if err != nil {
		return nil, nil, false, err
	}
	return &accountID, &scopeID, false, nil
}

// handleGetLatestReport returns the newest report artifact for a given
// strategy, including pending/error states that supersede an older completed
// decision.
//
//	GET /api/v1/strategies/{id}/reports/latest
func (s *Server) handleGetLatestReport(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeBadRequest)
		return
	}
	if s.reportArtifacts == nil {
		respondError(w, http.StatusNotImplemented, "report artifacts not configured", ErrCodeNotImplemented)
		return
	}

	reportType := r.URL.Query().Get("report_type")
	if reportType == "" {
		reportType = "paper_validation"
	}
	accountID, scopeID, legacy, err := reportScopeFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "account_id and scope_id are required, or request legacy=legacy_unscoped", ErrCodeBadRequest)
		return
	}

	artifacts, err := s.reportArtifacts.List(r.Context(), pgrepo.ReportArtifactFilter{
		StrategyID:    &id,
		ReportType:    reportType,
		AccountID:     accountID,
		ScopeID:       scopeID,
		IncludeLegacy: legacy,
	}, 1, 0)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get latest report", ErrCodeInternal)
		return
	}
	if len(artifacts) == 0 {
		respondError(w, http.StatusNotFound, "no report found", ErrCodeNotFound)
		return
	}
	artifact := &artifacts[0]

	ageReference := artifact.CreatedAt
	if artifact.CompletedAt != nil {
		ageReference = *artifact.CompletedAt
	}
	stale := math.Max(0, math.Round(time.Since(ageReference).Seconds()))

	if s.reportMetrics != nil {
		s.reportMetrics.ObserveReportStaleness(id.String(), stale)
	}

	respondJSON(w, http.StatusOK, reportLatestResponse{
		ReportArtifact: *artifact,
		StaleSeconds:   stale,
	})
}

// handleListReports returns a paginated list of report artifacts for a strategy.
//
//	GET /api/v1/strategies/{id}/reports
func (s *Server) handleListReports(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error(), ErrCodeBadRequest)
		return
	}
	if s.reportArtifacts == nil {
		respondError(w, http.StatusNotImplemented, "report artifacts not configured", ErrCodeNotImplemented)
		return
	}

	limit, offset := parsePagination(r)
	accountID, scopeID, legacy, err := reportScopeFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "account_id and scope_id are required, or request legacy=legacy_unscoped", ErrCodeBadRequest)
		return
	}

	filter := pgrepo.ReportArtifactFilter{
		StrategyID:    &id,
		AccountID:     accountID,
		ScopeID:       scopeID,
		IncludeLegacy: legacy,
	}
	if rt := r.URL.Query().Get("report_type"); rt != "" {
		filter.ReportType = rt
	}
	if st := r.URL.Query().Get("status"); st != "" {
		filter.Status = st
	}

	artifacts, err := s.reportArtifacts.List(r.Context(), filter, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list reports", ErrCodeInternal)
		return
	}

	respondList(w, artifacts, limit, offset)
}

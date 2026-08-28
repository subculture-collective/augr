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
	List(ctx context.Context, accountID, scopeID, strategyID uuid.UUID, reportType, status string, limit, offset int) ([]pgrepo.ReportArtifact, error)
}

type PaperEvaluationScopeStore interface {
	RegisterScope(context.Context, *pgrepo.PaperEvaluationScope) error
	ListScopes(context.Context, uuid.UUID, int, int) ([]pgrepo.PaperEvaluationScope, error)
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
	accountID, err := canonicalAccountIDFromPath(r)
	if err != nil || input.AccountID != accountID {
		respondError(w, http.StatusNotFound, "account not found", ErrCodeNotFound)
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

type paperEvaluationScopeResponse struct {
	ID                     uuid.UUID `json:"id"`
	AccountID              uuid.UUID `json:"account_id"`
	Label                  string    `json:"label"`
	ManifestSHA256         string    `json:"manifest_sha256"`
	QualitySHA256          string    `json:"quality_sha256"`
	SimulationPolicySHA256 string    `json:"simulation_policy_sha256"`
	CapitalPolicySHA256    string    `json:"capital_policy_sha256"`
	CanonicalSHA256        string    `json:"canonical_sha256"`
}

func (s *Server) handleListPaperEvaluationScopes(w http.ResponseWriter, r *http.Request) {
	if s.paperEvaluationScopes == nil {
		respondError(w, http.StatusNotImplemented, "paper evaluation scopes not configured", ErrCodeNotImplemented)
		return
	}
	accountID, err := canonicalAccountIDFromPath(r)
	if err != nil {
		respondError(w, http.StatusNotFound, "account not found", ErrCodeNotFound)
		return
	}
	limit, offset := parsePagination(r)
	scopes, err := s.paperEvaluationScopes.ListScopes(r.Context(), accountID, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list paper evaluation scopes", ErrCodeInternal)
		return
	}
	response := make([]paperEvaluationScopeResponse, len(scopes))
	for i, scope := range scopes {
		if scope.AccountID != accountID {
			respondError(w, http.StatusNotFound, "paper evaluation scope not found", ErrCodeNotFound)
			return
		}
		response[i] = paperEvaluationScopeResponse{
			ID: scope.ID, AccountID: scope.AccountID,
			Label:          scope.EvaluationStart.Format("2006-01-02") + " to " + scope.EvaluationEnd.Format("2006-01-02"),
			ManifestSHA256: scope.ManifestSHA256, QualitySHA256: scope.QualitySHA256,
			SimulationPolicySHA256: scope.SimulationPolicySHA256, CapitalPolicySHA256: scope.CapitalPolicySHA256,
			CanonicalSHA256: scope.CanonicalSHA256,
		}
	}
	respondList(w, response, limit, offset)
}

// reportLatestResponse wraps the latest report artifact with a stale_seconds
// field showing how old the report is.
type reportLatestResponse struct {
	pgrepo.ReportArtifact
	StaleSeconds float64 `json:"stale_seconds"`
}

func reportScopeFilter(r *http.Request) (*uuid.UUID, *uuid.UUID, error) {
	accountID, err := canonicalAccountIDFromPath(r)
	if err != nil {
		return nil, nil, err
	}
	scopeID, err := uuid.Parse(r.URL.Query().Get("evidence_scope_id"))
	if err != nil {
		return nil, nil, err
	}
	return &accountID, &scopeID, nil
}

// handleGetLatestReport returns the newest report artifact for a given
// strategy, including pending/error states that supersede an older completed
// decision.
//
//	GET /api/v1/accounts/{accountID}/strategies/{id}/reports/latest
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
	accountID, scopeID, err := reportScopeFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "evidence_scope_id is required", ErrCodeBadRequest)
		return
	}

	artifacts, err := s.reportArtifacts.List(r.Context(), *accountID, *scopeID, id, reportType, "", 1, 0)
	if err != nil {
		if isNotFound(err) {
			respondError(w, http.StatusNotFound, "report scope not found", ErrCodeNotFound)
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to get latest report", ErrCodeInternal)
		return
	}
	if len(artifacts) == 0 {
		respondError(w, http.StatusNotFound, "no report found", ErrCodeNotFound)
		return
	}
	artifact := &artifacts[0]
	if artifact.StrategyID != id || artifact.ScopeID == nil || *artifact.ScopeID != *scopeID || artifact.AccountID == nil || *artifact.AccountID != *accountID {
		respondError(w, http.StatusNotFound, "no report found", ErrCodeNotFound)
		return
	}

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
//	GET /api/v1/accounts/{accountID}/strategies/{id}/reports
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
	accountID, scopeID, err := reportScopeFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "evidence_scope_id is required", ErrCodeBadRequest)
		return
	}

	reportType := r.URL.Query().Get("report_type")
	if reportType == "" {
		reportType = "paper_validation"
	}
	status := r.URL.Query().Get("status")
	artifacts, err := s.reportArtifacts.List(r.Context(), *accountID, *scopeID, id, reportType, status, limit, offset)
	if err != nil {
		if isNotFound(err) {
			respondError(w, http.StatusNotFound, "report scope not found", ErrCodeNotFound)
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to list reports", ErrCodeInternal)
		return
	}
	if len(artifacts) == 0 {
		respondError(w, http.StatusNotFound, "no report found", ErrCodeNotFound)
		return
	}
	for i := range artifacts {
		artifact := artifacts[i]
		if artifact.StrategyID != id || artifact.ScopeID == nil || *artifact.ScopeID != *scopeID || artifact.AccountID == nil || *artifact.AccountID != *accountID {
			respondError(w, http.StatusNotFound, "no report found", ErrCodeNotFound)
			return
		}
	}

	respondList(w, artifacts, limit, offset)
}

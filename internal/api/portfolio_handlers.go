package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PortfolioValuation is derived from one account-scoped canonical checkpoint.
// P&L is absent unless mark coverage and checkpoint reconciliation both pass.
type PortfolioValuation struct {
	AccountID            *uuid.UUID       `json:"account_id"`
	GeneratedAt          time.Time        `json:"generated_at"`
	AsOf                 *time.Time       `json:"as_of"`
	MarkCoverageComplete *bool            `json:"mark_coverage_complete"`
	ReconciliationPassed *bool            `json:"reconciliation_passed"`
	OpenPositions        *int             `json:"open_positions"`
	MarkedPositions      *int             `json:"marked_positions"`
	UnmarkedPositions    *int             `json:"unmarked_positions"`
	MarketValue          *decimal.Decimal `json:"market_value"`
	TotalPnL             *decimal.Decimal `json:"total_pnl"`
	UnrealizedPnL        *decimal.Decimal `json:"unrealized_pnl"`
	RealizedPnL          *decimal.Decimal `json:"realized_pnl"`
	UnavailableReasons   []string         `json:"unavailable_reasons"`
}

type PortfolioSummary struct {
	PortfolioValuation
}

func (s *Server) handleListPositions(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePagination(r)
	q := r.URL.Query()
	filter := repository.PositionFilter{Ticker: q.Get("ticker")}
	if !ParseEnumParam(w, q, "side", &filter.Side) {
		return
	}
	positions, err := s.positions.List(r.Context(), filter, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list positions", ErrCodeInternal)
		return
	}
	total, err := s.positions.Count(r.Context(), filter)
	if err != nil {
		s.logger.Warn("count positions", "error", err.Error())
	}
	respondListWithTotal(w, positions, total, limit, offset)
}

func (s *Server) handleGetOpenPositions(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePagination(r)
	q := r.URL.Query()
	filter := repository.PositionFilter{Ticker: q.Get("ticker")}
	if !ParseEnumParam(w, q, "side", &filter.Side) {
		return
	}
	positions, err := s.positions.GetOpen(r.Context(), filter, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list open positions", ErrCodeInternal)
		return
	}
	total, err := s.positions.CountOpen(r.Context(), filter)
	if err != nil {
		s.logger.Warn("count open positions", "error", err.Error())
	}
	respondListWithTotal(w, positions, total, limit, offset)
}

func (s *Server) handlePortfolioSummary(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.authorizedProjectionAccount(w, r)
	if !ok {
		return
	}
	generatedAt := time.Now().UTC()
	summary := PortfolioSummary{PortfolioValuation: s.loadPortfolioValuation(r.Context(), accountID, generatedAt)}
	respondJSON(w, http.StatusOK, summary)
}

func (s *Server) authorizedProjectionAccount(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	if _, supplied := r.URL.Query()["account_id"]; supplied {
		respondError(w, http.StatusBadRequest, "account_id is server configured", ErrCodeValidation)
		return nil, false
	}
	return s.projectionAccountID, true
}

func (s *Server) loadPortfolioValuation(ctx context.Context, accountID *uuid.UUID, generatedAt time.Time) PortfolioValuation {
	result := PortfolioValuation{AccountID: accountID, GeneratedAt: generatedAt, UnavailableReasons: []string{}}
	if accountID == nil {
		result.UnavailableReasons = append(result.UnavailableReasons, "server_account_binding_unavailable")
		return result
	}
	if s.projections == nil {
		result.UnavailableReasons = append(result.UnavailableReasons, "projection_reader_unavailable")
		return result
	}
	snapshot, err := s.projections.GetLatestPortfolioProjection(ctx, *accountID, generatedAt)
	if errors.Is(err, repository.ErrNotFound) {
		result.UnavailableReasons = append(result.UnavailableReasons, "projection_unavailable")
		return result
	}
	if err != nil {
		s.logger.Error("read portfolio projection", "account_id", accountID.String(), "error", err.Error())
		result.UnavailableReasons = append(result.UnavailableReasons, "projection_read_failed")
		return result
	}
	if snapshot == nil || snapshot.Checkpoint == nil || snapshot.Valuation == nil {
		result.UnavailableReasons = append(result.UnavailableReasons, "projection_invalid")
		return result
	}
	if snapshot.Checkpoint.AccountID != *accountID || snapshot.Checkpoint.AsOf.After(generatedAt) {
		result.UnavailableReasons = append(result.UnavailableReasons, "projection_boundary_invalid")
		return result
	}
	asOf := snapshot.Checkpoint.AsOf
	result.AsOf = &asOf
	openPositions, markedPositions, unmarkedPositions := 0, 0, 0
	for _, position := range snapshot.Valuation.Positions {
		if !position.Open {
			continue
		}
		openPositions++
		if position.MarkObservationID == uuid.Nil {
			unmarkedPositions++
		} else {
			markedPositions++
		}
	}
	markCoverageComplete := unmarkedPositions == 0
	reconciliationPassed := snapshot.ReconciliationAvailable && snapshot.ReconciliationPassed
	result.OpenPositions, result.MarkedPositions, result.UnmarkedPositions = &openPositions, &markedPositions, &unmarkedPositions
	result.MarkCoverageComplete, result.ReconciliationPassed = &markCoverageComplete, &reconciliationPassed
	if !markCoverageComplete {
		result.UnavailableReasons = append(result.UnavailableReasons, "mark_coverage_incomplete")
	}
	if !snapshot.ReconciliationAvailable {
		result.UnavailableReasons = append(result.UnavailableReasons, "reconciliation_unavailable")
	} else if !snapshot.ReconciliationPassed {
		result.UnavailableReasons = append(result.UnavailableReasons, "reconciliation_failed")
	}
	if len(result.UnavailableReasons) == 0 {
		result.MarketValue = decimalPointer(snapshot.Valuation.Totals.MarketValue)
		result.RealizedPnL = decimalPointer(snapshot.Valuation.Totals.RealizedPnL)
		result.UnrealizedPnL = decimalPointer(snapshot.Valuation.Totals.UnrealizedPnL)
		result.TotalPnL = decimalPointer(snapshot.Valuation.Totals.TotalPnL)
	}
	return result
}

func decimalPointer(value decimal.Decimal) *decimal.Decimal { cloned := value; return &cloned }

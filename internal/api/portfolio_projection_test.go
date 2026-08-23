package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type stubProjectionReader struct {
	snapshot *repository.ProjectionSnapshot
	err      error
	accounts []uuid.UUID
	cutoffs  []time.Time
}

func (reader *stubProjectionReader) GetLatestPortfolioProjection(_ context.Context, accountID uuid.UUID, generatedAt time.Time) (*repository.ProjectionSnapshot, error) {
	reader.accounts = append(reader.accounts, accountID)
	reader.cutoffs = append(reader.cutoffs, generatedAt)
	return reader.snapshot, reader.err
}

func TestPortfolioValuationUsesOneAccountScopedSnapshot(t *testing.T) {
	accountID := uuid.New()
	asOf := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	reader := &stubProjectionReader{snapshot: &repository.ProjectionSnapshot{
		Checkpoint: &ledger.ProjectionCheckpoint{AccountID: accountID, AsOf: asOf},
		Valuation: &ledger.ProjectionValuation{
			Positions: []ledger.ProjectionPosition{{Open: true, MarkObservationID: uuid.New()}},
			Totals:    ledger.ProjectionTotals{MarketValue: decimal.RequireFromString("125.50"), RealizedPnL: decimal.RequireFromString("4.25"), UnrealizedPnL: decimal.RequireFromString("21.25"), TotalPnL: decimal.RequireFromString("25.50")},
		},
		ReconciliationAvailable: true, ReconciliationPassed: true,
	}}
	server := &Server{projections: reader, projectionAccountID: &accountID, logger: slog.Default()}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/summary", nil)
	recorder := httptest.NewRecorder()
	server.handlePortfolioSummary(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	result := decodeJSON[PortfolioSummary](t, recorder)
	if len(reader.accounts) != 1 || reader.accounts[0] != accountID {
		t.Fatalf("projection reads = %+v", reader.accounts)
	}
	if len(reader.cutoffs) != 1 || reader.cutoffs[0].Before(asOf) {
		t.Fatalf("projection cutoffs = %+v", reader.cutoffs)
	}
	if result.AsOf == nil || !result.AsOf.Equal(asOf) || result.TotalPnL == nil || result.TotalPnL.String() != "25.5" {
		t.Fatalf("valuation = %+v", result)
	}
}

func TestPortfolioValuationUnavailableWithoutProjection(t *testing.T) {
	accountID := uuid.New()
	reader := &stubProjectionReader{err: repository.ErrNotFound}
	result := (&Server{projections: reader, logger: slog.Default()}).loadPortfolioValuation(context.Background(), &accountID, time.Now().UTC())
	if result.TotalPnL != nil || result.RealizedPnL != nil || result.UnrealizedPnL != nil || result.MarketValue != nil {
		t.Fatalf("missing projection returned valuation: %+v", result)
	}
	if len(result.UnavailableReasons) != 1 || result.UnavailableReasons[0] != "projection_unavailable" {
		t.Fatalf("reasons = %+v", result.UnavailableReasons)
	}
}

func TestPortfolioValuationFailsClosedOnCoverageAndReconciliation(t *testing.T) {
	accountID := uuid.New()
	reader := &stubProjectionReader{snapshot: &repository.ProjectionSnapshot{
		Checkpoint:              &ledger.ProjectionCheckpoint{AccountID: accountID, AsOf: time.Now().UTC()},
		Valuation:               &ledger.ProjectionValuation{Positions: []ledger.ProjectionPosition{{Open: true}}, Totals: ledger.ProjectionTotals{TotalPnL: decimal.NewFromInt(99)}},
		ReconciliationAvailable: true, ReconciliationPassed: false,
	}}
	result := (&Server{projections: reader, logger: slog.Default()}).loadPortfolioValuation(context.Background(), &accountID, time.Now().UTC())
	if result.TotalPnL != nil {
		t.Fatalf("gated P&L = %v", result.TotalPnL)
	}
	if len(result.UnavailableReasons) != 2 || result.UnavailableReasons[0] != "mark_coverage_incomplete" || result.UnavailableReasons[1] != "reconciliation_failed" {
		t.Fatalf("reasons = %+v", result.UnavailableReasons)
	}
}

func TestPortfolioValuationUnavailableWithoutCorrectProviderReconciliation(t *testing.T) {
	accountID := uuid.New()
	reader := &stubProjectionReader{snapshot: &repository.ProjectionSnapshot{
		Checkpoint: &ledger.ProjectionCheckpoint{AccountID: accountID, AsOf: time.Now().UTC()},
		Valuation: &ledger.ProjectionValuation{
			Positions: []ledger.ProjectionPosition{{Open: true, MarkObservationID: uuid.New()}},
			Totals:    ledger.ProjectionTotals{TotalPnL: decimal.NewFromInt(99)},
		},
	}}
	result := (&Server{projections: reader, logger: slog.Default()}).loadPortfolioValuation(context.Background(), &accountID, time.Now().UTC())
	if result.TotalPnL != nil || len(result.UnavailableReasons) != 1 || result.UnavailableReasons[0] != "reconciliation_unavailable" {
		t.Fatalf("result = %+v", result)
	}
}

func TestPortfolioSummaryFailsClosedWithoutServerAccountBinding(t *testing.T) {
	reader := &stubProjectionReader{}
	server := &Server{projections: reader, logger: slog.Default()}
	recorder := httptest.NewRecorder()
	server.handlePortfolioSummary(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/summary", nil))
	result := decodeJSON[PortfolioSummary](t, recorder)
	if result.AccountID != nil || result.TotalPnL != nil || result.OpenPositions != nil || result.MarkCoverageComplete != nil || len(reader.accounts) != 0 || len(result.UnavailableReasons) != 1 || result.UnavailableReasons[0] != "server_account_binding_unavailable" {
		t.Fatalf("result = %+v reads=%+v", result, reader.accounts)
	}
}

func TestPortfolioSummaryRejectsCallerControlledAccount(t *testing.T) {
	accountID := uuid.New()
	server := &Server{projectionAccountID: &accountID, logger: slog.Default()}
	recorder := httptest.NewRecorder()
	server.handlePortfolioSummary(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/summary?account_id="+uuid.NewString(), nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPortfolioValuationRejectsFutureCheckpoint(t *testing.T) {
	accountID := uuid.New()
	generatedAt := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	reader := &stubProjectionReader{snapshot: &repository.ProjectionSnapshot{
		Checkpoint: &ledger.ProjectionCheckpoint{AccountID: accountID, AsOf: generatedAt.Add(time.Second)},
		Valuation:  &ledger.ProjectionValuation{}, ReconciliationAvailable: true, ReconciliationPassed: true,
	}}
	result := (&Server{projections: reader, logger: slog.Default()}).loadPortfolioValuation(context.Background(), &accountID, generatedAt)
	if result.TotalPnL != nil || len(result.UnavailableReasons) != 1 || result.UnavailableReasons[0] != "projection_boundary_invalid" {
		t.Fatalf("result = %+v", result)
	}
}

var _ repository.ProjectionReader = (*stubProjectionReader)(nil)

package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/service"
)

type researchListResponse struct {
	Data   []service.ResearchOpportunity `json:"data"`
	Total  int                           `json:"total"`
	Limit  int                           `json:"limit"`
	Offset int                           `json:"offset"`
}

type fakeResearchScannerService struct {
	optionsReq     *service.OptionsOpportunityRequest
	polymarketReq  *service.PolymarketOpportunityRequest
	optionsResp    []service.ResearchOpportunity
	polymarketResp []service.ResearchOpportunity
	optionsErr     error
	polymarketErr  error
}

func (f *fakeResearchScannerService) ScanOptions(_ context.Context, req service.OptionsOpportunityRequest) ([]service.ResearchOpportunity, error) {
	f.optionsReq = &req
	return append([]service.ResearchOpportunity(nil), f.optionsResp...), f.optionsErr
}

func (f *fakeResearchScannerService) ScanPolymarket(_ context.Context, req service.PolymarketOpportunityRequest) ([]service.ResearchOpportunity, error) {
	f.polymarketReq = &req
	return append([]service.ResearchOpportunity(nil), f.polymarketResp...), f.polymarketErr
}

func TestResearchScannerRoutesReturn501WhenServiceMissing(t *testing.T) {
	srv := newTestServer(t)
	srv.researchSvc = nil

	rr := doRequest(t, srv, http.MethodGet, "/api/v1/research/options/opportunities/AAPL", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotImplemented)
	}
	body := decodeJSON[ErrorResponse](t, rr)
	if body.Code != ErrCodeNotImplemented {
		t.Fatalf("code = %q, want %q", body.Code, ErrCodeNotImplemented)
	}
}

func TestResearchScannerRoutesRejectInvalidQuery(t *testing.T) {
	srv := newTestServer(t)
	srv.researchSvc = service.NewResearchScannerService(nil, nil, nil)

	rr := doRequest(t, srv, http.MethodGet, "/api/v1/research/polymarket/opportunities?best_bid=abc", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	body := decodeJSON[ErrorResponse](t, rr)
	if body.Code != ErrCodeValidation {
		t.Fatalf("code = %q, want %q", body.Code, ErrCodeValidation)
	}
}

func TestResearchOptionsRoutePassesFilters(t *testing.T) {
	srv := newTestServer(t)
	fake := &fakeResearchScannerService{optionsResp: []service.ResearchOpportunity{{Decision: domain.TradeDecision{InstrumentKey: "AAPL-TEST"}}}}
	srv.researchSvc = fake

	strategyID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	expiry := time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/research/options/opportunities/AAPL?limit=2&strategy_id="+strategyID.String()+"&expiry="+expiry.Format("2006-01-02")+"&type=call", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if fake.optionsReq == nil {
		t.Fatal("options request not captured")
	}
	if fake.optionsReq.Underlying != "AAPL" || fake.optionsReq.Limit != 2 || fake.optionsReq.OptionType != domain.OptionTypeCall {
		t.Fatalf("options request = %+v", fake.optionsReq)
	}
	if fake.optionsReq.StrategyID == nil || *fake.optionsReq.StrategyID != strategyID {
		t.Fatalf("StrategyID = %v, want %v", fake.optionsReq.StrategyID, strategyID)
	}
	if fake.optionsReq.Expiry == nil || !fake.optionsReq.Expiry.Equal(expiry) {
		t.Fatalf("Expiry = %v, want %v", fake.optionsReq.Expiry, expiry)
	}
	body := decodeJSON[researchListResponse](t, rr)
	if len(body.Data) != 1 || body.Data[0].Decision.InstrumentKey != "AAPL-TEST" {
		t.Fatalf("response data = %+v", body.Data)
	}
}

func TestResearchOptionsRouteReturns500OnServiceError(t *testing.T) {
	srv := newTestServer(t)
	srv.researchSvc = &fakeResearchScannerService{optionsErr: errors.New("provider failed")}

	rr := doRequest(t, srv, http.MethodGet, "/api/v1/research/options/opportunities/AAPL", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	body := decodeJSON[ErrorResponse](t, rr)
	if body.Code != ErrCodeInternal {
		t.Fatalf("code = %q, want %q", body.Code, ErrCodeInternal)
	}
}

func TestResearchPolymarketRouteReturnsListWithBookAndProbability(t *testing.T) {
	srv := newTestServer(t)
	srv.researchSvc = service.NewResearchScannerService(nil, nil, nil)

	rr := doRequest(t, srv, http.MethodGet, "/api/v1/research/polymarket/opportunities?slug=will-it-happen&token_id=token-123&outcome=YES&probability=0.65&best_bid=0.50&best_ask=0.55&ask_depth_usd=500", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := decodeJSON[researchListResponse](t, rr)
	if len(body.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(body.Data))
	}
	if body.Data[0].Decision.MarketType != domain.MarketTypePolymarket {
		t.Fatalf("MarketType = %q, want %q", body.Data[0].Decision.MarketType, domain.MarketTypePolymarket)
	}
	if body.Data[0].Decision.InstrumentKey != "token-123" {
		t.Fatalf("InstrumentKey = %q, want token-123", body.Data[0].Decision.InstrumentKey)
	}
}

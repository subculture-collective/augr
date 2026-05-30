package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
)

type fakeDivergenceSource struct {
	div backtest.Divergence
	err error
}

func (f fakeDivergenceSource) DivergenceFor(context.Context, string) (backtest.Divergence, error) {
	return f.div, f.err
}

func TestDivergenceHandler_MissingStrategyID_400(t *testing.T) {
	srv := newTestServerWithDeps(t, testDeps())
	srv.divergenceSrc = fakeDivergenceSource{}
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/backtests/divergence", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestDivergenceHandler_SingularPath_Success_200(t *testing.T) {
	srv := newTestServerWithDeps(t, testDeps())
	srv.divergenceSrc = fakeDivergenceSource{div: backtest.Divergence{StrategyID: "123", Backtest: backtest.SidedMetrics{Samples: 1}, Live: backtest.SidedMetrics{Samples: 1}}}
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/backtest/divergence?strategy_id=123", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestDivergenceHandler_NotFound_404(t *testing.T) {
	srv := newTestServerWithDeps(t, testDeps())
	srv.divergenceSrc = fakeDivergenceSource{err: backtest.ErrDivergenceNotFound}
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/backtests/divergence?strategy_id=123", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestDivergenceHandler_Success_200(t *testing.T) {
	srv := newTestServerWithDeps(t, testDeps())
	srv.divergenceSrc = fakeDivergenceSource{div: backtest.Divergence{StrategyID: "123", Backtest: backtest.SidedMetrics{FillRate: 0.5, WinRate: 0.55, Samples: 120}, Live: backtest.SidedMetrics{FillRate: 0.52, WinRate: 0.54, Samples: 80}}}
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/backtests/divergence?strategy_id=123", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var got struct {
		StrategyID  string                `json:"strategy_id"`
		Backtest    backtest.SidedMetrics `json:"backtest"`
		Live        backtest.SidedMetrics `json:"live"`
		Tolerance   float64               `json:"tolerance"`
		MaxAbsDelta float64               `json:"max_abs_delta"`
		Status      string                `json:"status"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != string(backtest.DivergenceWithinTolerance) || got.StrategyID != "123" || got.Tolerance != backtest.DefaultDivergenceTolerance {
		t.Fatalf("unexpected response: %+v", got)
	}
}

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeMarketDataStatusSource struct{ status PolymarketStatus }

func (f fakeMarketDataStatusSource) PolymarketStatus(context.Context) (PolymarketStatus, error) {
	return f.status, nil
}

func TestHandlePolymarketStatus(t *testing.T) {
	t.Run("nil source", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/marketdata/polymarket/status", nil)
		(&Server{}).handlePolymarketStatus(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d", rr.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		srv := &Server{mdStatusSrc: fakeMarketDataStatusSource{status: PolymarketStatus{Enabled: true, WSConnections: 2, AvgJitterMS: 1.5, Dropped: 3, ReadySlugs: []string{"a"}, RecorderLagS: 4.2, UpdatedAt: time.Unix(1, 0)}}}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/marketdata/polymarket/status", nil)
		srv.handlePolymarketStatus(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
	})
}

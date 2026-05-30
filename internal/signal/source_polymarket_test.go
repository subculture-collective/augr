package signal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPolymarketFetchMarketAcceptsNumericVolume(t *testing.T) {
	t.Parallel()
	_, summary := fetchMarketSummary(t, `{"data":[{"market_slug":"slug-1","question":"Q","condition_id":"cond-1","volume_24hr":123.45}]}`)
	if summary.volume24h != 123.45 {
		t.Fatalf("volume24h = %v, want 123.45", summary.volume24h)
	}
}

func TestPolymarketFetchMarketAcceptsStringVolume(t *testing.T) {
	t.Parallel()
	_, summary := fetchMarketSummary(t, `{"data":[{"market_slug":"slug-1","question":"Q","condition_id":"cond-1","volume_24hr":"123.45"}]}`)
	if summary.volume24h != 123.45 {
		t.Fatalf("volume24h = %v, want 123.45", summary.volume24h)
	}
}

func fetchMarketSummary(t *testing.T, body string) (*PolymarketSource, clobMarketSummary) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)

	p := NewPolymarketSource(PolymarketSourceConfig{CLOBURL: server.URL, Interval: time.Second}, nil)
	summary, err := p.fetchMarket(context.Background(), "slug-1")
	if err != nil {
		t.Fatalf("fetchMarket() error = %v", err)
	}
	return p, summary
}

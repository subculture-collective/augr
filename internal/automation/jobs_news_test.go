package automation

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJobOrchestratorSocialScan_SkipsStockTwitsForPolymarketStrategies(t *testing.T) {
	origTransport := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = origTransport
	})

	var (
		mu    sync.Mutex
		calls = map[string]int{}
	)
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		calls[req.URL.Path]++
		mu.Unlock()

		switch req.URL.Path {
		case "/api/2/trending/symbols.json":
			return jsonResponse(`{"symbols":[]}`), nil
		case "/api/2/streams/symbol/AAPL.json":
			return jsonResponse(`{"messages":[]}`), nil
		case "/api/2/streams/symbol/SLUG-1.json":
			return jsonResponse(`{"messages":[]}`), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL.String())
			return nil, nil
		}
	})

	orch := NewJobOrchestrator(OrchestratorDeps{
		NewsFeedRepo: &pgrepo.NewsFeedRepo{},
		StrategyRepo: &stubStrategyRepoForReports{
			strategies: []domain.Strategy{
				{ID: uuid.New(), Name: "stock", Status: domain.StrategyStatusActive, Ticker: "AAPL", MarketType: domain.MarketTypeStock},
				{ID: uuid.New(), Name: "polymarket", Status: domain.StrategyStatusActive, Ticker: "slug-1", MarketType: domain.MarketTypePolymarket},
			},
		},
	})

	if err := orch.socialScan(context.Background()); err != nil {
		t.Fatalf("socialScan() error = %v", err)
	}

	mu.Lock()
	gotStock := calls["/api/2/streams/symbol/AAPL.json"]
	gotPolymarket := calls["/api/2/streams/symbol/SLUG-1.json"]
	mu.Unlock()

	if gotStock != 1 {
		t.Fatalf("stock sentiment requests = %d, want 1", gotStock)
	}
	if gotPolymarket != 0 {
		t.Fatalf("polymarket sentiment requests = %d, want 0", gotPolymarket)
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

package universe

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
)

type refreshRepo struct {
	replaced []TrackedTicker
	err      error
}

func (r *refreshRepo) Upsert(context.Context, *TrackedTicker) error       { return nil }
func (r *refreshRepo) UpsertBatch(context.Context, []TrackedTicker) error { return nil }
func (r *refreshRepo) ReplaceConstituents(_ context.Context, tickers []TrackedTicker) error {
	r.replaced = append([]TrackedTicker(nil), tickers...)
	return r.err
}

func (r *refreshRepo) List(context.Context, ListFilter, int, int) ([]TrackedTicker, error) {
	return nil, nil
}
func (r *refreshRepo) Watchlist(context.Context, int) ([]TrackedTicker, error) { return nil, nil }
func (r *refreshRepo) UpdateScore(context.Context, string, float64) error      { return nil }
func (r *refreshRepo) Count(context.Context) (int, error)                      { return 0, nil }

func TestRefreshConstituentsReplacesAuthoritativeSet(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"ticker":" aapl ","name":"Apple","primary_exchange":"XNAS"},{"ticker":"IBM","name":"IBM","primary_exchange":"XNYS"}]}`))
	}))
	defer server.Close()

	client := polygon.NewClient("test-key", slog.New(slog.NewTextHandler(io.Discard, nil)))
	polygon.SetBaseURLForTest(client, server.URL)
	repo := &refreshRepo{}
	manager := NewUniverse(repo, client, nil)

	count, err := manager.RefreshConstituents(t.Context())
	if err != nil {
		t.Fatalf("RefreshConstituents() error = %v", err)
	}
	if count != 2 || len(repo.replaced) != 2 {
		t.Fatalf("count = %d, replaced = %d; want 2", count, len(repo.replaced))
	}
	if got := repo.replaced[0]; got.Ticker != "AAPL" || got.IndexGroup != "nasdaq" || !got.Active {
		t.Fatalf("first replacement = %#v", got)
	}
}

func TestRefreshConstituentsRejectsEmptyResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	client := polygon.NewClient("test-key", slog.New(slog.NewTextHandler(io.Discard, nil)))
	polygon.SetBaseURLForTest(client, server.URL)
	repo := &refreshRepo{err: errors.New("must not be called")}

	count, err := NewUniverse(repo, client, nil).RefreshConstituents(t.Context())
	if err == nil || count != 0 {
		t.Fatalf("RefreshConstituents() = (%d, %v), want empty-response error", count, err)
	}
	if repo.replaced != nil {
		t.Fatalf("repository called with %#v", repo.replaced)
	}
}

func TestTrackedTickersFromPolygonDeduplicatesNormalizedTickers(t *testing.T) {
	tracked, duplicates := trackedTickersFromPolygon([]polygon.TickerInfo{
		{Ticker: " aapl ", Name: "Apple Inc.", PrimaryExchange: "XNAS"},
		{Ticker: "AAPL", Name: "Apple Inc duplicate", PrimaryExchange: "XNAS"},
		{Ticker: "msft", Name: "Microsoft Corp.", PrimaryExchange: "XNAS"},
		{Ticker: "", Name: "blank", PrimaryExchange: "XNYS"},
	})

	if duplicates != 1 {
		t.Fatalf("duplicates = %d, want 1", duplicates)
	}
	if len(tracked) != 2 {
		t.Fatalf("len(tracked) = %d, want 2", len(tracked))
	}
	if tracked[0].Ticker != "AAPL" || tracked[1].Ticker != "MSFT" {
		t.Fatalf("tickers = %#v, want AAPL/MSFT", []string{tracked[0].Ticker, tracked[1].Ticker})
	}
	if !tracked[0].Active || !tracked[1].Active {
		t.Fatal("tracked tickers should be active")
	}
}

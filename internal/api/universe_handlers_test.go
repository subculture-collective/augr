package api

import (
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

func TestMergeOpenPositionsIntoWatchlistAppendsMissingHoldings(t *testing.T) {
	s := &Server{}

	got := s.mergeOpenPositionsIntoWatchlist(t.Context(), []universe.TrackedTicker{
		{Ticker: "AMD", Name: "Advanced Micro Devices", WatchScore: 20, Active: true},
	}, []domain.Position{
		{Ticker: "CCL", Side: domain.PositionSideLong, Quantity: 10},
		{Ticker: "amd", Side: domain.PositionSideLong, Quantity: 5},
		{Ticker: " TSCO ", Side: domain.PositionSideLong, Quantity: 2},
	})

	if len(got) != 3 {
		t.Fatalf("merged watchlist length = %d, want 3", len(got))
	}
	if got[0].Ticker != "AMD" {
		t.Fatalf("first ticker = %q, want existing watchlist ticker AMD", got[0].Ticker)
	}
	if got[1].Ticker != "CCL" || got[1].Name != "Current holding" {
		t.Fatalf("second ticker = %#v, want CCL current holding", got[1])
	}
	if got[2].Ticker != "TSCO" || got[2].Name != "Current holding" {
		t.Fatalf("third ticker = %#v, want TSCO current holding", got[2])
	}
}

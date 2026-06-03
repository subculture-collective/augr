package universe

import (
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/data/polygon"
)

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

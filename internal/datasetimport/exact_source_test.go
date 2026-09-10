package datasetimport

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/google/uuid"
)

func TestExactStockImportEvidence(t *testing.T) {
	barAt := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	observed := barAt.Add(25 * time.Hour)
	request := dataset.MarketImportRequest{Provider: "polygon", Feed: "sip", Timeframe: "1d", AdjustmentPolicy: "raw", From: barAt, To: barAt, Universe: []string{"AAPL"}}
	for _, tc := range []struct {
		name   string
		mutate func(*exactStockProviderStub)
		fail   bool
	}{
		{"exact", func(*exactStockProviderStub) {}, false},
		{"missing_count", func(s *exactStockProviderStub) { s.result.Bars[0].TradeCount = "" }, true},
		{"missing_vwap", func(s *exactStockProviderStub) { s.result.Bars[0].VWAP = "" }, true},
		{"altered_value", func(s *exactStockProviderStub) { s.result.Bars[0].Close = "11" }, true},
		{"unbound_page", func(s *exactStockProviderStub) { s.result.Bars[0].PageIndex = 2 }, true},
		{"page_count", func(s *exactStockProviderStub) { s.result.Receipt.Pages = 2 }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := exactStockFixture(barAt)
			tc.mutate(&stub)
			source := ProviderSource{Mode: ModeStockBars, Stock: stub, Instruments: resolverStub{stockID: uuid.New()}, Clock: func() time.Time { return observed }}
			result, err := source.FetchMarketPayloads(context.Background(), request)
			if tc.fail {
				if err == nil {
					t.Fatal("invalid exact source accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			payload := result.Payloads[0]
			if payload.Bar().Close != "11.000000000000000001" || payload.Bar().TradeCount != "5" || !bytes.Contains(payload.CanonicalBytes(), []byte("source_evidence")) {
				t.Fatal("source evidence or exact fields lost")
			}
			if _, err := dataset.MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

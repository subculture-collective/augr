package datasetimport

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
)

const exactImportSnapshotRow = `{"latestQuote":{"bp":1.000000000000000001,"bs":9007199254740993,"ap":3,"as":4,"t":"2026-01-02T14:30:00Z"},"latestTrade":{"p":2,"s":1,"i":9007199254740993,"x":"A","t":"2026-01-02T14:29:00Z"},"impliedVolatility":0,"greeks":{"delta":0,"gamma":0.000000000000000001,"theta":-0.123456789012345678,"vega":0,"rho":0}}`

func TestProviderSourceCapturesExactSnapshotEvidence(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1beta1/options/snapshots/AAPL" {
			t.Errorf("unexpected source path: %s", r.URL.Path)
		}
		if calls == 1 {
			_, _ = w.Write([]byte(`{"snapshots":{},"next_page_token":"cursor"}`))
			return
		}
		if r.URL.Query().Get("page_token") != "cursor" {
			t.Error("source pagination cursor missing")
		}
		_, _ = w.Write([]byte(`{"snapshots":{"AAPL260116C00150000":` + exactImportSnapshotRow + `},"next_page_token":null}`))
	}))
	defer server.Close()
	provider := alpaca.NewOptionsDataProvider("synthetic-key", "synthetic-secret", nil)
	provider.SetBaseURL(server.URL)
	at := time.Date(2026, 1, 2, 14, 31, 0, 0, time.UTC)
	source := &ProviderSource{Mode: ModeOptionChainSnapshot, Options: provider, Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, Clock: func() time.Time { return at }}
	result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "snapshot", AdjustmentPolicy: "raw", Universe: []string{"AAPL"}, From: at.Add(-2 * time.Minute), To: at, DecisionCutoff: at})
	if err != nil || len(result.Payloads) != 2 || calls != 2 {
		t.Fatalf("exact snapshot import failed: %+v err=%v", result, err)
	}
	if result.Payloads[0].Kind() != dataset.MarketPayloadOptionQuote || result.Payloads[1].Kind() != dataset.MarketPayloadOptionSnapshot {
		t.Fatal("snapshot endpoint fabricated contract or historical trade payload")
	}
	for _, payload := range result.Payloads {
		restored, err := dataset.MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
		if err != nil || !bytes.Equal(restored.CanonicalBytes(), payload.CanonicalBytes()) {
			t.Fatalf("canonical source replay failed: %v", err)
		}
	}
	if result.Payloads[1].Snapshot().Quote.BidPrice != "1.000000000000000001" || result.Payloads[1].Snapshot().Quote.BidSize != "9007199254740993" {
		t.Fatal("import lost exact decimal precision")
	}
}

type exactSnapshotResultStub struct {
	optionsProviderStub
	result data.ExactOptionsSnapshotResult
}

func (stub exactSnapshotResultStub) GetExactOptionsSnapshotsWithReceipt(context.Context, string, string) (data.ExactOptionsSnapshotResult, error) {
	return stub.result, nil
}

func TestProviderSourceRejectsMismatchedExactSnapshots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"snapshots":{"AAPL260116C00150000":` + exactImportSnapshotRow + `},"next_page_token":null}`))
	}))
	defer server.Close()
	provider := alpaca.NewOptionsDataProvider("synthetic-key", "synthetic-secret", nil)
	provider.SetBaseURL(server.URL)
	at := time.Date(2026, 1, 2, 14, 31, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*data.ExactOptionsSnapshotResult, *dataset.MarketImportRequest){
		"price": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots[0].BidPrice = "1"
		},
		"Greek": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) { r.Snapshots[0].Theta = "0" },
		"trade": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots[0].LatestTrade.Price = "3"
		},
		"missing trade": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots[0].LatestTrade = nil
		},
		"duplicate": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots = append(r.Snapshots, r.Snapshots[0])
		},
		"invalid OCC": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots[0].Symbol = "AAPL260230C00150000"
		},
		"wrong underlying": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots[0].Symbol = "MSFT260116C00150000"
		},
		"page index": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) { r.Snapshots[0].PageIndex = 1 },
		"page count": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) { r.Receipt.Pages = 2 },
		"incomplete": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Receipt.PaginationComplete = false
		},
		"unentitled": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) { r.Receipt.Entitled = false },
		"empty":      func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) { r.Snapshots = nil },
		"future quote": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots[0].QuoteTimestamp = at.Add(time.Minute)
		},
		"submicro quote": func(r *data.ExactOptionsSnapshotResult, _ *dataset.MarketImportRequest) {
			r.Snapshots[0].QuoteTimestamp = r.Snapshots[0].QuoteTimestamp.Add(time.Nanosecond)
		},
		"old acquisition window":    func(_ *data.ExactOptionsSnapshotResult, q *dataset.MarketImportRequest) { q.To = at.Add(-time.Minute) },
		"future acquisition window": func(_ *data.ExactOptionsSnapshotResult, q *dataset.MarketImportRequest) { q.From = at.Add(time.Minute) },
		"cutoff": func(_ *data.ExactOptionsSnapshotResult, q *dataset.MarketImportRequest) {
			q.DecisionCutoff = at.Add(-time.Minute)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fetched, err := provider.GetExactOptionsSnapshotsWithReceipt(t.Context(), "AAPL", "opra")
			if err != nil {
				t.Fatal(err)
			}
			request := dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "snapshot", AdjustmentPolicy: "raw", Universe: []string{"AAPL"}, From: at.Add(-2 * time.Minute), To: at, DecisionCutoff: at}
			mutate(&fetched, &request)
			source := &ProviderSource{Mode: ModeOptionChainSnapshot, Options: exactSnapshotResultStub{result: fetched}, Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, Clock: func() time.Time { return at }}
			result, err := source.FetchMarketPayloads(t.Context(), request)
			if err == nil || len(result.Payloads) != 0 || result.Entitled || result.PaginationComplete {
				t.Fatalf("invalid snapshot import did not fail closed: %+v err=%v", result, err)
			}
		})
	}
}

package datasetimport

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/google/uuid"
)

type exactTradeProviderStub struct {
	optionsProviderStub
	result data.ExactOptionsTradeResult
}

func (s exactTradeProviderStub) GetExactOptionsTradesWithReceipt(context.Context, string, time.Time, time.Time, string) (data.ExactOptionsTradeResult, error) {
	return s.result, nil
}

func exactTradeFixture(at time.Time) exactTradeProviderStub {
	const symbol = "AAPL260116C00150000"
	row := []byte(fmt.Sprintf(`{"i":42,"p":5.100000000000000001,"s":2,"x":"C","t":%q}`, at.Format(time.RFC3339Nano)))
	return exactTradeProviderStub{result: data.ExactOptionsTradeResult{
		Receipt: data.HistoricalFetchReceipt{Provider: "alpaca", Feed: "opra", AdjustmentPolicy: "raw", Pages: 1, Entitled: true, PaginationComplete: true},
		Trades:  []data.ExactOptionTrade{{Timestamp: at, ProviderID: "42", Price: "5.100000000000000001", Size: "2", Exchange: "C", Raw: row}},
		Pages:   []data.HistoricalSourcePage{{RequestPath: "/v1beta1/options/trades", Query: url.Values{"symbols": {symbol}, "feed": {"opra"}, "start": {at.Format(time.RFC3339Nano)}, "end": {at.Format(time.RFC3339Nano)}}.Encode(), Body: []byte(fmt.Sprintf(`{"trades":{"%s":[%s]},"next_page_token":null}`, symbol, row))}},
	}}
}

func TestExactOptionTradeImportProviderPagination(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	end := at.Add(time.Second)
	observed := end.Add(time.Minute)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		stamp, token := at, `"next-page"`
		if calls == 2 {
			stamp, token = end, "null"
			if r.URL.Query().Get("page_token") != "next-page" {
				t.Error("cursor lost")
			}
		}
		if calls > 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprintf(w, `{"trades":{"%s":[{"i":%d,"p":5.100000000000000001,"s":2,"x":"C","t":%q}]},"next_page_token":%s}`, symbol, 42+calls, stamp.Format(time.RFC3339Nano), token)
	}))
	defer server.Close()
	provider := alpaca.NewOptionsDataProvider("synthetic-key", "synthetic-secret", nil)
	provider.SetBaseURL(server.URL)
	source := &ProviderSource{Mode: ModeOptionTrades, Options: provider, Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, OptionSymbols: []string{symbol}, Clock: func() time.Time { return observed }}
	result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "trade", AdjustmentPolicy: "raw", From: at, To: end, DecisionCutoff: observed, Universe: []string{"AAPL"}})
	if err != nil || len(result.Payloads) != 2 || calls != 2 {
		t.Fatalf("pagination import: count=%d calls=%d err=%v", len(result.Payloads), calls, err)
	}
	for _, p := range result.Payloads {
		restored, err := dataset.MarketPayloadFromCanonical(p.ID(), p.Digest(), p.CanonicalBytes())
		if err != nil || !bytes.Equal(restored.CanonicalBytes(), p.CanonicalBytes()) || !bytes.Contains(p.CanonicalBytes(), []byte("5.100000000000000001")) || !bytes.Contains(p.CanonicalBytes(), []byte("source_evidence")) {
			t.Fatalf("exact replay: %v", err)
		}
	}
}

func TestOptionTradeImportRejectsLegacyFloatSource(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	source := &ProviderSource{Mode: ModeOptionTrades, Options: optionsProviderStub{}, Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, OptionSymbols: []string{"AAPL260116C00150000"}, Clock: func() time.Time { return at.Add(time.Minute) }}
	result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "trade", AdjustmentPolicy: "raw", From: at, To: at, DecisionCutoff: at.Add(time.Minute), Universe: []string{"AAPL"}})
	if err == nil || len(result.Payloads) != 0 {
		t.Fatal("accepted float-only trade source")
	}
}

func TestExactOptionTradeImportRejectsMismatchedEvidence(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*data.ExactOptionsTradeResult){
		"price":        func(r *data.ExactOptionsTradeResult) { r.Trades[0].Price = "5.1" },
		"size":         func(r *data.ExactOptionsTradeResult) { r.Trades[0].Size = "3" },
		"id":           func(r *data.ExactOptionsTradeResult) { r.Trades[0].ProviderID = "43" },
		"exchange":     func(r *data.ExactOptionsTradeResult) { r.Trades[0].Exchange = "A" },
		"timestamp":    func(r *data.ExactOptionsTradeResult) { r.Trades[0].Timestamp = at.Add(time.Nanosecond) },
		"duplicate":    func(r *data.ExactOptionsTradeResult) { r.Trades = append(r.Trades, r.Trades[0]) },
		"page index":   func(r *data.ExactOptionsTradeResult) { r.Trades[0].PageIndex = 1 },
		"row index":    func(r *data.ExactOptionsTradeResult) { r.Trades[0].RowIndex = 1 },
		"page count":   func(r *data.ExactOptionsTradeResult) { r.Receipt.Pages = 2 },
		"not entitled": func(r *data.ExactOptionsTradeResult) { r.Receipt.Entitled = false },
		"incomplete":   func(r *data.ExactOptionsTradeResult) { r.Receipt.PaginationComplete = false },
		"path":         func(r *data.ExactOptionsTradeResult) { r.Pages[0].RequestPath = "/v1beta1/options/bars" },
		"row bytes":    func(r *data.ExactOptionsTradeResult) { r.Trades[0].Raw = []byte(`{}`) },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := exactTradeFixture(at)
			mutate(&fixture.result)
			source := &ProviderSource{Mode: ModeOptionTrades, Options: fixture, Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, OptionSymbols: []string{"AAPL260116C00150000"}, Clock: func() time.Time { return at.Add(time.Minute) }}
			result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "trade", AdjustmentPolicy: "raw", From: at, To: at.Add(time.Second), DecisionCutoff: at.Add(time.Minute), Universe: []string{"AAPL"}})
			if err == nil || len(result.Payloads) != 0 {
				t.Fatalf("accepted mismatched source: %v", err)
			}
		})
	}
}

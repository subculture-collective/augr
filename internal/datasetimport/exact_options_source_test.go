package datasetimport

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type exactOptionsProviderStub struct {
	optionsProviderStub
	result data.ExactHistoricalResult
}

func (s exactOptionsProviderStub) GetExactOptionsOHLCVWithReceipt(context.Context, string, data.Timeframe, time.Time, time.Time, string, string) (data.ExactHistoricalResult, error) {
	return s.result, nil
}

func exactOptionsFixture(at time.Time) exactOptionsProviderStub {
	const symbol = "AAPL260116C00150000"
	row := []byte(fmt.Sprintf(`{"t":%q,"o":10,"h":12,"l":9,"c":11.000000000000000001,"v":100,"n":5,"vw":10.5}`, at.Format(time.RFC3339Nano)))
	page := []byte(fmt.Sprintf(`{"bars":{"%s":[%s]},"next_page_token":null}`, symbol, row))
	query := url.Values{"symbols": {symbol}, "feed": {"opra"}, "timeframe": {"1Day"}, "start": {at.Format(time.RFC3339Nano)}, "end": {at.Format(time.RFC3339Nano)}}
	return exactOptionsProviderStub{result: data.ExactHistoricalResult{
		Receipt: data.HistoricalFetchReceipt{Provider: "alpaca", Feed: "opra", AdjustmentPolicy: "raw", Pages: 1, Entitled: true, PaginationComplete: true},
		Bars:    []data.ExactHistoricalBar{{Timestamp: at, Open: "10", High: "12", Low: "9", Close: "11.000000000000000001", Volume: "100", TradeCount: "5", VWAP: "10.5", Raw: row}},
		Pages:   []data.HistoricalSourcePage{{RequestPath: "/v1beta1/options/bars", Query: query.Encode(), Body: page}},
	}}
}

func TestExactOptionBarImportRoundtrip(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	observed := at.Add(25 * time.Hour)
	source := &ProviderSource{Mode: ModeOptionBars, Options: exactOptionsFixture(at), Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, OptionSymbols: []string{"AAPL260116C00150000"}, Clock: func() time.Time { return observed }}
	result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "1d", AdjustmentPolicy: "raw", From: at, To: at, DecisionCutoff: observed, Universe: []string{"AAPL"}})
	if err != nil || len(result.Payloads) != 1 {
		t.Fatalf("exact import failed: %v", err)
	}
	payload := result.Payloads[0]
	if payload.Bar().Close != "11.000000000000000001" || payload.Bar().TradeCount != "5" || !bytes.Contains(payload.CanonicalBytes(), []byte("symbol_key")) {
		t.Fatal("exact option evidence lost")
	}
	restored, err := dataset.MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
	if err != nil || !bytes.Equal(restored.CanonicalBytes(), payload.CanonicalBytes()) {
		t.Fatalf("exact option replay failed: %v", err)
	}
}

func TestOptionBarImportRejectsLegacyFloatSource(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	observed := at.Add(25 * time.Hour)
	source := &ProviderSource{
		Mode: ModeOptionBars,
		Options: optionsProviderStub{bars: []domain.OHLCV{{
			Timestamp: at, Open: 10, High: 12, Low: 9, Close: 11, Volume: 100,
		}}},
		Instruments:   resolverStub{stockID: uuid.New(), optionID: uuid.New()},
		OptionSymbols: []string{"AAPL260116C00150000"},
		Clock:         func() time.Time { return observed },
	}
	result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{
		Provider: "alpaca", Feed: "opra", Timeframe: "1d", AdjustmentPolicy: "raw",
		From: at, To: at, DecisionCutoff: observed, Universe: []string{"AAPL"},
	})
	if err == nil || !strings.Contains(err.Error(), "exact source evidence") || len(result.Payloads) != 0 {
		t.Fatalf("legacy float provider must not produce immutable exact option bars: payloads=%d err=%v", len(result.Payloads), err)
	}
}

func TestExactOptionBarImportProviderPagination(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	end := at.Add(24 * time.Hour)
	observed := end.Add(25 * time.Hour)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		stamp, token := at, `"next-page"`
		if calls == 2 {
			if r.URL.Query().Get("page_token") != "next-page" {
				t.Error("second request lost pagination cursor")
			}
			stamp, token = end, "null"
		}
		if calls > 2 {
			t.Error("unexpected extra provider request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprintf(w, `{"bars":{"%s":[{"t":%q,"o":10,"h":12,"l":9,"c":11.000000000000000001,"v":100,"n":5,"vw":10.5}]},"next_page_token":%s}`, symbol, stamp.Format(time.RFC3339Nano), token)
	}))
	defer server.Close()
	provider := alpaca.NewOptionsDataProvider("synthetic-key", "synthetic-secret", nil)
	provider.SetBaseURL(server.URL)
	source := &ProviderSource{Mode: ModeOptionBars, Options: provider, Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, OptionSymbols: []string{symbol}, Clock: func() time.Time { return observed }}
	result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "1d", AdjustmentPolicy: "raw", From: at, To: end, DecisionCutoff: observed, Universe: []string{"AAPL"}})
	if err != nil || len(result.Payloads) != 2 || calls != 2 {
		t.Fatalf("two-page import failed: payloads=%d calls=%d err=%v", len(result.Payloads), calls, err)
	}
	for _, payload := range result.Payloads {
		restored, err := dataset.MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
		if err != nil || !bytes.Equal(restored.CanonicalBytes(), payload.CanonicalBytes()) || payload.Bar().Close != "11.000000000000000001" {
			t.Fatalf("provider evidence replay failed: %v", err)
		}
	}
}

func TestExactOptionBarImportRejectsMismatchedEvidence(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	observed := at.Add(25 * time.Hour)
	for _, tc := range []struct {
		name   string
		mutate func(*data.ExactHistoricalResult)
	}{
		{"wrong symbol", func(r *data.ExactHistoricalResult) {
			r.Pages[0].Body = bytes.ReplaceAll(r.Pages[0].Body, []byte("AAPL260116C00150000"), []byte("MSFT260116C00150000"))
		}},
		{"wrong feed", func(r *data.ExactHistoricalResult) {
			r.Pages[0].Query = strings.ReplaceAll(r.Pages[0].Query, "opra", "indicative")
		}},
		{"wrong timeframe", func(r *data.ExactHistoricalResult) {
			r.Pages[0].Query = strings.ReplaceAll(r.Pages[0].Query, "1Day", "1Min")
		}},
		{"wrong path", func(r *data.ExactHistoricalResult) { r.Pages[0].RequestPath = "/v2/stocks/bars" }},
		{"wrong row index", func(r *data.ExactHistoricalResult) { r.Bars[0].RowIndex = 1 }},
		{"wrong page index", func(r *data.ExactHistoricalResult) { r.Bars[0].PageIndex = 1 }},
		{"rounded close", func(r *data.ExactHistoricalResult) { r.Bars[0].Close = "11" }},
		{"guessed count", func(r *data.ExactHistoricalResult) { r.Bars[0].TradeCount = "0" }},
		{"guessed VWAP", func(r *data.ExactHistoricalResult) { r.Bars[0].VWAP = "0" }},
		{"missing count", func(r *data.ExactHistoricalResult) {
			r.Bars[0].Raw = bytes.ReplaceAll(r.Bars[0].Raw, []byte(`,"n":5`), nil)
			r.Pages[0].Body = bytes.ReplaceAll(r.Pages[0].Body, []byte(`,"n":5`), nil)
		}},
		{"missing VWAP", func(r *data.ExactHistoricalResult) {
			r.Bars[0].Raw = bytes.ReplaceAll(r.Bars[0].Raw, []byte(`,"vw":10.5`), nil)
			r.Pages[0].Body = bytes.ReplaceAll(r.Pages[0].Body, []byte(`,"vw":10.5`), nil)
		}},
		{"duplicate symbol map", func(r *data.ExactHistoricalResult) {
			r.Pages[0].Body = []byte(fmt.Sprintf(`{"bars":{"AAPL260116C00150000":[%s],"AAPL260116C00150000":[%s]}}`, r.Bars[0].Raw, r.Bars[0].Raw))
		}},
		{"incomplete pagination", func(r *data.ExactHistoricalResult) { r.Receipt.PaginationComplete = false }},
		{"credential query", func(r *data.ExactHistoricalResult) {
			q, _ := url.ParseQuery(r.Pages[0].Query)
			q.Set("access_token", "synthetic")
			r.Pages[0].Query = q.Encode()
		}},
		{"duplicate cursor", func(r *data.ExactHistoricalResult) {
			q, _ := url.ParseQuery(r.Pages[0].Query)
			q["page_token"] = []string{"one", "two"}
			r.Pages[0].Query = q.Encode()
		}},
		{"oversized cursor", func(r *data.ExactHistoricalResult) {
			q, _ := url.ParseQuery(r.Pages[0].Query)
			q.Set("page_token", strings.Repeat("x", 4097))
			r.Pages[0].Query = q.Encode()
		}},
		{"unknown query", func(r *data.ExactHistoricalResult) {
			q, _ := url.ParseQuery(r.Pages[0].Query)
			q.Set("adjusted", "true")
			r.Pages[0].Query = q.Encode()
		}},
		{"submicrosecond timestamp", func(r *data.ExactHistoricalResult) { *r = exactOptionsFixture(at.Add(time.Nanosecond)).result }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := exactOptionsFixture(at)
			tc.mutate(&fixture.result)
			source := &ProviderSource{Mode: ModeOptionBars, Options: fixture, Instruments: resolverStub{stockID: uuid.New(), optionID: uuid.New()}, OptionSymbols: []string{"AAPL260116C00150000"}, Clock: func() time.Time { return observed }}
			result, err := source.FetchMarketPayloads(t.Context(), dataset.MarketImportRequest{Provider: "alpaca", Feed: "opra", Timeframe: "1d", AdjustmentPolicy: "raw", From: at, To: at.Add(time.Second), DecisionCutoff: observed, Universe: []string{"AAPL"}})
			if err == nil || len(result.Payloads) != 0 {
				t.Fatalf("invalid evidence accepted: payloads=%d err=%v", len(result.Payloads), err)
			}
		})
	}
}

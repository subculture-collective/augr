package dataset

import (
	"strings"
	"testing"
	"time"
)

func TestExactSnapshotQuoteSourceBinding(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const row = `{"latestQuote":{"bp":1.000000000000000001,"bs":9007199254740993,"ap":2,"as":0,"t":"2026-01-02T14:30:00Z"}}`
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	makeInput := func() MarketPayloadInput {
		return MarketPayloadInput{Kind: MarketPayloadOptionQuote, Provider: "alpaca", Symbol: symbol, UnderlyingSymbol: "AAPL", Feed: "opra", Timeframe: "snapshot", AdjustmentPolicy: "raw", EffectiveAt: at, Quote: &QuotePayload{BidPrice: "1.000000000000000001", BidSize: "9007199254740993", AskPrice: "2", AskSize: "0"}, SourceEvidence: &SourcePageEvidence{RequestPath: "/v1beta1/options/snapshots/AAPL", Query: "feed=opra&limit=100", SymbolKey: symbol, Row: []byte(row), Page: []byte(`{"snapshots":{"` + symbol + `":` + row + `},"next_page_token":null}`)}}
	}
	i := makeInput()
	if err := validateBarSource(&i); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*MarketPayloadInput){
		"price":           func(i *MarketPayloadInput) { i.Quote.BidPrice = "1" },
		"size":            func(i *MarketPayloadInput) { i.Quote.BidSize = "9007199254740992" },
		"time":            func(i *MarketPayloadInput) { i.EffectiveAt = at.Add(time.Microsecond) },
		"feed":            func(i *MarketPayloadInput) { i.Feed = "indicative" },
		"underlying":      func(i *MarketPayloadInput) { i.UnderlyingSymbol = "MSFT" },
		"symbol":          func(i *MarketPayloadInput) { i.Symbol = "AAPL260116P00150000" },
		"path":            func(i *MarketPayloadInput) { i.SourceEvidence.RequestPath += "/extra" },
		"missing limit":   func(i *MarketPayloadInput) { i.SourceEvidence.Query = "feed=opra" },
		"duplicate query": func(i *MarketPayloadInput) { i.SourceEvidence.Query = "feed=opra&feed=opra&limit=100" },
		"unknown query":   func(i *MarketPayloadInput) { i.SourceEvidence.Query += "&unknown=value" },
		"missing zero": func(i *MarketPayloadInput) {
			i.SourceEvidence.Row = []byte(strings.Replace(row, `"as":0,`, "", 1))
			i.SourceEvidence.Page = []byte(strings.Replace(string(i.SourceEvidence.Page), `"as":0,`, "", 1))
		},
		"fractional size": func(i *MarketPayloadInput) {
			i.Quote.AskSize = "0.5"
			i.SourceEvidence.Row = []byte(strings.Replace(row, `"as":0`, `"as":0.5`, 1))
			i.SourceEvidence.Page = []byte(strings.Replace(string(i.SourceEvidence.Page), `"as":0`, `"as":0.5`, 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			i := makeInput()
			mutate(&i)
			if err := validateBarSource(&i); err == nil {
				t.Fatal("accepted unrelated or malformed source")
			}
		})
	}
}

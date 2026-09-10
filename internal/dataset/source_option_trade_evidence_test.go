package dataset

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExactOptionTradeSourceBinding(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const row = `{"i":9007199254740993,"p":1.234567890123456789,"s":2,"x":"A","t":"2026-01-02T14:30:00Z"}`
	at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	makeInput := func() MarketPayloadInput {
		return MarketPayloadInput{Kind: MarketPayloadOptionTrade, Provider: "alpaca", Symbol: symbol, Feed: "opra", Timeframe: "trade", AdjustmentPolicy: "raw", EffectiveAt: at, Revision: "trade_9007199254740993", Trade: &TradePayload{Price: "1.234567890123456789", Size: "2", Exchange: "A"}, SourceEvidence: &SourcePageEvidence{RequestPath: "/v1beta1/options/trades", Query: url.Values{"symbols": {symbol}, "feed": {"opra"}, "start": {at.Format(time.RFC3339Nano)}, "end": {at.Format(time.RFC3339Nano)}, "page_token": {"cursor"}}.Encode(), SymbolKey: symbol, RowIndex: 0, Row: json.RawMessage(row), Page: json.RawMessage(`{"trades":{"` + symbol + `":[` + row + `]},"next_page_token":null}`)}}
	}
	input := makeInput()
	if err := validateBarSource(&input); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*MarketPayloadInput){
		"fractional source size": func(i *MarketPayloadInput) {
			i.Trade.Size = "2.5"
			i.SourceEvidence.Row = []byte(strings.ReplaceAll(string(i.SourceEvidence.Row), `"s":2`, `"s":2.5`))
			i.SourceEvidence.Page = []byte(strings.ReplaceAll(string(i.SourceEvidence.Page), `"s":2`, `"s":2.5`))
		},
		"price":     func(i *MarketPayloadInput) { i.Trade.Price = "1.2345678901234567" },
		"size":      func(i *MarketPayloadInput) { i.Trade.Size = "3" },
		"id":        func(i *MarketPayloadInput) { i.Revision = "trade_9007199254740992" },
		"exchange":  func(i *MarketPayloadInput) { i.Trade.Exchange = "B" },
		"time":      func(i *MarketPayloadInput) { i.EffectiveAt = at.Add(time.Nanosecond) },
		"symbol":    func(i *MarketPayloadInput) { i.Symbol = "MSFT260116C00150000" },
		"feed":      func(i *MarketPayloadInput) { i.Feed = "indicative" },
		"row index": func(i *MarketPayloadInput) { i.SourceEvidence.RowIndex = 1 },
		"row bytes": func(i *MarketPayloadInput) {
			i.SourceEvidence.Row = json.RawMessage(strings.Replace(row, `"s":2`, `"s":3`, 1))
		},
		"path": func(i *MarketPayloadInput) { i.SourceEvidence.RequestPath = "/v1beta1/options/bars" },
		"auth": func(i *MarketPayloadInput) { i.SourceEvidence.Query += "&token=secret" },
		"duplicate query": func(i *MarketPayloadInput) {
			q, _ := url.ParseQuery(i.SourceEvidence.Query)
			q.Add("feed", "opra")
			i.SourceEvidence.Query = q.Encode()
		},
		"unknown query": func(i *MarketPayloadInput) {
			q, _ := url.ParseQuery(i.SourceEvidence.Query)
			q.Set("unreviewed", "true")
			i.SourceEvidence.Query = q.Encode()
		},
	} {
		t.Run(name, func(t *testing.T) {
			i := makeInput()
			mutate(&i)
			if err := validateBarSource(&i); err == nil {
				t.Fatal("accepted mismatched source")
			}
		})
	}
}

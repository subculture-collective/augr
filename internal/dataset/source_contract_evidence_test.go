package dataset

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestExactContractSourceBinding(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const row = `{"id":"11111111-1111-4111-8111-111111111111","symbol":"AAPL260116C00150000","underlying_asset_id":"22222222-2222-4222-8222-222222222222","underlying_symbol":"AAPL","type":"call","style":"american","expiration_date":"2026-01-16","strike_price":"150.000","size":"100","status":"active","tradable":true}`
	at := time.Date(2026, 1, 2, 14, 31, 0, 0, time.UTC)
	makeInput := func() MarketPayloadInput {
		return MarketPayloadInput{Kind: MarketPayloadOptionContract, InstrumentID: uuid.New(), UnderlyingInstrumentID: uuid.New(), Provider: "alpaca", Symbol: symbol, UnderlyingSymbol: "AAPL", Feed: "reference", Timeframe: "snapshot", AdjustmentPolicy: "raw", EffectiveAt: at, ObservedAt: at, AvailableAt: at, Contract: &OptionContractPayload{OptionType: "call", Strike: "150", Expiry: "2026-01-16", Multiplier: "100", Style: "american"}, SourceEvidence: &SourcePageEvidence{RequestPath: "/v2/options/contracts/" + symbol, SymbolKey: symbol, Row: []byte(row), Page: []byte(row)}}
	}
	input := makeInput()
	payload, err := NewMarketPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
	if err != nil || !bytes.Equal(restored.CanonicalBytes(), payload.CanonicalBytes()) || !bytes.Equal(restored.canonical.SourceEvidence.Row, []byte(row)) {
		t.Fatalf("contract canonical reconstruction failed: %v", err)
	}
	for name, change := range map[string][2]string{
		"strike mismatch":     {`"150.000"`, `"151"`},
		"numeric strike":      {`"150.000"`, `150`},
		"size mismatch":       {`"size":"100"`, `"size":"50"`},
		"fractional size":     {`"size":"100"`, `"size":"100.5"`},
		"missing size":        {`"size":"100",`, ``},
		"style mismatch":      {`"american"`, `"european"`},
		"underlying mismatch": {`"underlying_symbol":"AAPL"`, `"underlying_symbol":"MSFT"`},
		"expiry mismatch":     {`"2026-01-16"`, `"2026-01-17"`},
		"null tradable":       {`"tradable":true`, `"tradable":null`},
		"bad status":          {`"active"`, `"unknown"`},
		"bad UUID":            {`11111111-1111-4111-8111-111111111111`, `invalid`},
	} {
		t.Run(name, func(t *testing.T) {
			i := makeInput()
			i.SourceEvidence.Row = []byte(strings.Replace(row, change[0], change[1], 1))
			i.SourceEvidence.Page = bytes.Clone(i.SourceEvidence.Row)
			if _, err := NewMarketPayload(i); err == nil {
				t.Fatal("accepted mismatched source")
			}
		})
	}
	for _, mode := range []string{"historical", "published", "feed", "timeframe", "adjustment"} {
		t.Run(mode, func(t *testing.T) {
			i := makeInput()
			switch mode {
			case "historical":
				i.EffectiveAt = at.Add(-time.Hour)
			case "published":
				i.PublishedAt = &at
			case "feed":
				i.Feed = "opra"
			case "timeframe":
				i.Timeframe = "1d"
			case "adjustment":
				i.AdjustmentPolicy = "adjusted"
			}
			if _, err := NewMarketPayload(i); err == nil {
				t.Fatal("accepted false acquisition metadata")
			}
		})
	}
}

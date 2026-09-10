package dataset

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestExactSnapshotAggregateSourceBinding(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const row = `{"latestQuote":{"bp":1,"bs":2,"ap":3,"as":4,"t":"2026-01-02T14:30:00Z"},"latestTrade":{"p":2,"s":1,"i":9007199254740993,"x":"A","t":"2026-01-02T14:29:00Z"},"impliedVolatility":0,"greeks":{"delta":0,"gamma":0.000000000000000001,"theta":-0.123456789012345678,"vega":0,"rho":0}}`
	at := time.Date(2026, 1, 2, 14, 31, 0, 0, time.UTC)
	makeInput := func() MarketPayloadInput {
		return MarketPayloadInput{Kind: MarketPayloadOptionSnapshot, Provider: "alpaca", Symbol: symbol, UnderlyingSymbol: "AAPL", Feed: "opra", Timeframe: "snapshot", AdjustmentPolicy: "raw", EffectiveAt: at, ObservedAt: at, Snapshot: &OptionSnapshotPayload{Quote: QuotePayload{BidPrice: "1", BidSize: "2", AskPrice: "3", AskSize: "4"}, LastTradePrice: "2", LastTradeSize: "1", ImpliedVolatility: "0", Delta: "0", Gamma: "0.000000000000000001", Theta: "-0.123456789012345678", Vega: "0", Rho: "0"}, SourceEvidence: &SourcePageEvidence{RequestPath: "/v1beta1/options/snapshots/AAPL", Query: "feed=opra&limit=100", SymbolKey: symbol, Row: []byte(row), Page: []byte(`{"snapshots":{"` + symbol + `":` + row + `},"next_page_token":null}`)}}
	}
	input := makeInput()
	if err := validateBarSource(&input); err != nil {
		t.Fatal(err)
	}
	input.InstrumentID, input.UnderlyingInstrumentID = uuid.New(), uuid.New()
	input.AvailableAt, input.Revision = at, "original"
	payload, err := NewMarketPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
	if err != nil || !bytes.Equal(restored.CanonicalBytes(), payload.CanonicalBytes()) || !bytes.Equal(restored.canonical.SourceEvidence.Row, []byte(row)) {
		t.Fatalf("snapshot canonical evidence failed reconstruction: %v", err)
	}
	for name, change := range map[string][2]string{
		"missing IV":      {`"impliedVolatility":0,`, ""},
		"null IV":         {`"impliedVolatility":0`, `"impliedVolatility":null`},
		"missing Greek":   {`"delta":0,`, ""},
		"changed Greek":   {`"rho":0`, `"rho":1`},
		"duplicate Greek": {`"rho":0`, `"rho":0,"rho":0`},
		"absent trade":    {`"latestTrade"`, `"unrelated"`},
		"bad trade id":    {`"i":9007199254740993`, `"i":0`},
		"empty exchange":  {`"x":"A"`, `"x":""`},
		"future trade":    {"14:29:00Z", "14:32:00Z"},
		"future quote":    {"14:30:00Z", "14:32:00Z"},
		"fractional size": {`"s":1`, `"s":1.5`},
	} {
		t.Run(name, func(t *testing.T) {
			i := makeInput()
			i.SourceEvidence.Row = []byte(strings.Replace(row, change[0], change[1], 1))
			i.SourceEvidence.Page = []byte(strings.Replace(string(i.SourceEvidence.Page), change[0], change[1], 1))
			if err := validateBarSource(&i); err == nil {
				t.Fatal("accepted incomplete or mismatched aggregate")
			}
		})
	}
	input.EffectiveAt = at.Add(-time.Minute)
	if err := validateBarSource(&input); err == nil {
		t.Fatal("aggregate acquisition time became old quote time")
	}
}

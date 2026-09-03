package dataset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMarketPayloadCanonicalIdentityAndRestore(t *testing.T) {
	payload, err := NewMarketPayload(testStockBarPayloadInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewMarketPayload(testStockBarPayloadInput())
	if err != nil {
		t.Fatal(err)
	}
	if payload.ID() != second.ID() || payload.Digest() != second.Digest() || string(payload.CanonicalBytes()) != string(second.CanonicalBytes()) {
		t.Fatal("identical market payloads did not converge")
	}
	restored, err := MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID() != payload.ID() || restored.Symbol() != "SPY" || restored.Timeframe() != "1Day" || restored.Bar().Close != "501.25" {
		t.Fatalf("restored payload = %+v", restored)
	}
	metadata := restored.Metadata()
	if metadata.PublishedAt == nil {
		t.Fatal("restored payload lost publication time")
	}
	wantReplay := time.Date(2026, 9, 3, 20, 0, 2, 123456000, time.UTC)
	if !restored.ReplayAvailableAt().Equal(wantReplay) || restored.ReplayAvailableAt().Equal(restored.AvailableAt()) {
		t.Fatalf("replay availability = %s, want publication time", restored.ReplayAvailableAt())
	}
	bar := restored.Bar()
	bar.Close = "1"
	if restored.Bar().Close != "501.25" {
		t.Fatal("market payload exposed mutable bar state")
	}
}

func TestMarketPayloadReplayAvailabilityFallsBackToLocalAvailability(t *testing.T) {
	input := testStockBarPayloadInput()
	input.PublishedAt = nil
	payload, err := NewMarketPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ReplayAvailableAt() != payload.AvailableAt() {
		t.Fatalf("replay availability = %s, want %s", payload.ReplayAvailableAt(), payload.AvailableAt())
	}
}

func TestMarketPayloadRejectsInvalidTypedBodiesAndEconomics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MarketPayloadInput)
	}{
		{"missing body", func(input *MarketPayloadInput) { input.Bar = nil }},
		{"multiple bodies", func(input *MarketPayloadInput) {
			input.Quote = &QuotePayload{BidPrice: "1", BidSize: "1", AskPrice: "2", AskSize: "1"}
		}},
		{"crossed bar", func(input *MarketPayloadInput) { input.Bar.High = "499" }},
		{"negative volume", func(input *MarketPayloadInput) { input.Bar.Volume = "-1" }},
		{"noncanonical decimal", func(input *MarketPayloadInput) { input.Bar.Close = "501.250" }},
		{"future publication", func(input *MarketPayloadInput) {
			value := input.ObservedAt.Add(time.Microsecond)
			input.PublishedAt = &value
		}},
		{"publication before event", func(input *MarketPayloadInput) {
			value := input.EffectiveAt.Add(-time.Microsecond)
			input.PublishedAt = &value
		}},
		{"event after observation", func(input *MarketPayloadInput) { input.EffectiveAt = input.ObservedAt.Add(time.Microsecond) }},
		{"option identity on stock", func(input *MarketPayloadInput) {
			input.UnderlyingInstrumentID = uuid.New()
			input.UnderlyingSymbol = "SPY"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testStockBarPayloadInput()
			test.mutate(&input)
			if _, err := NewMarketPayload(input); err == nil {
				t.Fatal("NewMarketPayload() error = nil")
			}
		})
	}
}

func TestMarketPayloadSupportsReviewedOptionPayloads(t *testing.T) {
	base := testOptionPayloadInput()
	tests := []struct {
		kind   MarketPayloadKind
		mutate func(*MarketPayloadInput)
	}{
		{MarketPayloadOptionBar, func(input *MarketPayloadInput) {
			input.Bar = &BarPayload{Open: "4", High: "5", Low: "3", Close: "4.5", Volume: "100", TradeCount: "20", VWAP: "4.25"}
		}},
		{MarketPayloadOptionQuote, func(input *MarketPayloadInput) {
			input.Quote = &QuotePayload{BidPrice: "4.4", BidSize: "10", AskPrice: "4.6", AskSize: "12", Exchange: "OPRA"}
		}},
		{MarketPayloadOptionTrade, func(input *MarketPayloadInput) {
			input.Trade = &TradePayload{Price: "4.5", Size: "2", Exchange: "CBOE"}
		}},
		{MarketPayloadOptionContract, func(input *MarketPayloadInput) {
			input.Contract = &OptionContractPayload{OptionType: "call", Strike: "500", Expiry: "2026-12-18", Multiplier: "100", Style: "american"}
		}},
		{MarketPayloadOptionSnapshot, func(input *MarketPayloadInput) {
			input.Snapshot = &OptionSnapshotPayload{
				Quote:          QuotePayload{BidPrice: "4.4", BidSize: "10", AskPrice: "4.6", AskSize: "12", Exchange: "OPRA"},
				LastTradePrice: "4.5", LastTradeSize: "2", ImpliedVolatility: "0.22",
				Delta: "0.51", Gamma: "0.04", Theta: "-0.03", Vega: "0.11", Rho: "0.02",
			}
		}},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			input := base
			input.Kind = test.kind
			test.mutate(&input)
			payload, err := NewMarketPayload(input)
			if err != nil {
				t.Fatal(err)
			}
			if payload.Kind() != test.kind {
				t.Fatalf("kind = %q", payload.Kind())
			}
		})
	}
}

func TestMarketPayloadFieldReconstructsTypedValues(t *testing.T) {
	t.Parallel()
	stock := testStockBarPayloadInput()
	option := func(kind MarketPayloadKind) MarketPayloadInput {
		input := testOptionPayloadInput()
		input.Kind = kind
		return input
	}
	quote := option(MarketPayloadOptionQuote)
	quote.Quote = &QuotePayload{BidPrice: "4.4", BidSize: "10", AskPrice: "4.6", AskSize: "12", Exchange: "OPRA"}
	snapshot := option(MarketPayloadOptionSnapshot)
	snapshot.Snapshot = &OptionSnapshotPayload{Quote: *quote.Quote, LastTradePrice: "4.5", LastTradeSize: "2", ImpliedVolatility: "0.22", Delta: "0.51", Gamma: "0.04", Theta: "-0.03", Vega: "0.11", Rho: "0.02"}
	contract := option(MarketPayloadOptionContract)
	contract.Contract = &OptionContractPayload{OptionType: "call", Strike: "500", Expiry: "2026-12-18", Multiplier: "100", Style: "american"}
	trade := option(MarketPayloadOptionTrade)
	trade.Trade = &TradePayload{Price: "4.5", Size: "2", Exchange: "CBOE"}
	tests := []struct {
		name  string
		input MarketPayloadInput
		kind  Kind
		field string
		want  string
	}{
		{name: "stock close", input: stock, kind: KindBars, field: "close", want: "501.25"},
		{name: "option midpoint", input: quote, kind: KindQuotes, field: "midpoint", want: "4.5"},
		{name: "snapshot delta", input: snapshot, kind: KindOptionChains, field: "delta", want: "0.51"},
		{name: "contract strike", input: contract, kind: KindOptionContracts, field: "strike", want: "500"},
		{name: "trade price", input: trade, kind: KindExternalObject, field: "price", want: "4.5"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload, err := NewMarketPayload(test.input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := payload.Field(test.kind, test.field)
			if err != nil || got != test.want {
				t.Fatalf("Field() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestMarketPayloadFieldRejectsKindAndFieldEscape(t *testing.T) {
	t.Parallel()
	payload, err := NewMarketPayload(testStockBarPayloadInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		kind  Kind
		field string
	}{{KindQuotes, "close"}, {KindBars, "future_close"}, {KindFundamentals, "close"}} {
		if got, fieldErr := payload.Field(request.kind, request.field); fieldErr == nil || got != "" {
			t.Fatalf("Field(%q,%q) = %q, %v; want fail closed", request.kind, request.field, got, fieldErr)
		}
	}
}

func TestMarketPayloadRestoreRejectsUnknownOrChangedContent(t *testing.T) {
	payload, err := NewMarketPayload(testStockBarPayloadInput())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload.CanonicalBytes(), &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	changed, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MarketPayloadFromCanonical(payload.ID(), payload.Digest(), changed); err == nil {
		t.Fatal("changed payload restored")
	}
	if _, err := MarketPayloadFromCanonical(payload.ID(), strings.Repeat("0", 64), payload.CanonicalBytes()); err == nil {
		t.Fatal("wrong digest restored")
	}
}

func testStockBarPayloadInput() MarketPayloadInput {
	observed := time.Date(2026, 9, 3, 20, 1, 2, 123456000, time.UTC)
	published := observed.Add(-time.Minute)
	return MarketPayloadInput{
		Kind: MarketPayloadStockBar, InstrumentID: uuid.MustParse("11000000-0000-4000-8000-000000000001"),
		Provider: "alpaca", Feed: "sip", Symbol: "SPY", Timeframe: "1Day", AdjustmentPolicy: "all",
		EffectiveAt: time.Date(2026, 9, 2, 20, 0, 0, 0, time.UTC), PublishedAt: &published,
		ObservedAt: observed, AvailableAt: observed, Revision: "original",
		Bar: &BarPayload{Open: "500", High: "503", Low: "498", Close: "501.25", Volume: "1234567", TradeCount: "9876", VWAP: "500.75"},
	}
}

func testOptionPayloadInput() MarketPayloadInput {
	observed := time.Date(2026, 9, 3, 20, 1, 2, 123456000, time.UTC)
	return MarketPayloadInput{
		InstrumentID:           uuid.MustParse("11000000-0000-4000-8000-000000000002"),
		UnderlyingInstrumentID: uuid.MustParse("11000000-0000-4000-8000-000000000001"),
		Provider:               "alpaca", Feed: "opra", Symbol: "SPY261218C00500000", UnderlyingSymbol: "SPY",
		Timeframe: "1Min", AdjustmentPolicy: "raw", EffectiveAt: observed.Add(-time.Minute),
		ObservedAt: observed, AvailableAt: observed, Revision: "original",
	}
}

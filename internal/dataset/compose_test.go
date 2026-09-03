package dataset

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestComposeBoundMarketDatasetsIsDeterministicAndComplete(t *testing.T) {
	start := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	cutoff := start.Add(time.Hour)
	stock := composedFixture(t, MarketPayloadStockBar, uuid.New(), uuid.Nil, "AAPL", "", start, cutoff, "polygon", "sip", "1d")
	underlyingID := uuid.New()
	option := composedFixture(t, MarketPayloadOptionTrade, uuid.New(), underlyingID, "AAPL260116C00150000", "AAPL", start.Add(time.Minute), cutoff, "alpaca", "opra", "trade")
	forward, err := ComposeBoundMarketDatasets([]*BoundMarketDataset{stock, option}, cutoff)
	if err != nil {
		t.Fatalf("ComposeBoundMarketDatasets() error = %v", err)
	}
	reverse, err := ComposeBoundMarketDatasets([]*BoundMarketDataset{option, stock}, cutoff)
	if err != nil {
		t.Fatalf("ComposeBoundMarketDatasets(reverse) error = %v", err)
	}
	if forward.Manifest().ID() != reverse.Manifest().ID() || forward.Manifest().Digest() != reverse.Manifest().Digest() || len(forward.Payloads()) != 2 || len(forward.Bindings()) != 2 {
		t.Fatalf("composition is not deterministic and complete: %s/%s payloads=%d bindings=%d", forward.Manifest().ID(), reverse.Manifest().ID(), len(forward.Payloads()), len(forward.Bindings()))
	}
	if _, err := ComposeBoundMarketDatasets([]*BoundMarketDataset{stock, stock}, cutoff); err == nil {
		t.Fatal("ComposeBoundMarketDatasets() accepted duplicate source manifest")
	}
	if _, err := ComposeBoundMarketDatasets([]*BoundMarketDataset{stock, option}, cutoff.Add(-2*time.Hour)); err == nil {
		t.Fatal("ComposeBoundMarketDatasets() accepted cutoff before source manifests")
	}
}

func composedFixture(t *testing.T, kind MarketPayloadKind, instrumentID, underlyingID uuid.UUID, symbol, underlying string, effectiveAt, cutoff time.Time, provider, feed, timeframe string) *BoundMarketDataset {
	t.Helper()
	input := MarketPayloadInput{
		Kind: kind, InstrumentID: instrumentID, UnderlyingInstrumentID: underlyingID, Provider: provider, Feed: feed,
		Symbol: symbol, UnderlyingSymbol: underlying, Timeframe: timeframe, AdjustmentPolicy: "raw",
		EffectiveAt: effectiveAt, ObservedAt: cutoff, AvailableAt: cutoff, Revision: "original",
	}
	partitionKind := KindBars
	switch kind {
	case MarketPayloadStockBar:
		input.Bar = &BarPayload{Open: "1", High: "1", Low: "1", Close: "1", Volume: "1", TradeCount: "1", VWAP: "1"}
	case MarketPayloadOptionTrade:
		input.Trade = &TradePayload{Price: "1", Size: "1", Exchange: "C"}
		partitionKind = KindExternalObject
	}
	payload, err := NewMarketPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ManifestInput{DecisionCutoff: cutoff, Partitions: []PartitionInput{{
		Kind: partitionKind, Provider: provider, Source: "test", Namespace: provider + "/" + symbol,
		RequestSHA256: strings.Repeat("a", 64), MediaType: "application/json", SymbologyVersion: provider + "-v1",
		AdjustmentPolicy: "raw", Timezone: "UTC", Calendar: "XNYS", Revision: "original", License: "test", RetentionPolicy: "indefinite",
		Observations: []ObservationInput{{SourceKey: symbol + "/" + timeframe, InstrumentID: instrumentID, EffectiveAt: effectiveAt, ObservedAt: cutoff, AvailableAt: cutoff, Revision: "original", ContentSHA256: payload.Digest()}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := NewBoundMarketDataset(manifest, []*MarketPayload{payload})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

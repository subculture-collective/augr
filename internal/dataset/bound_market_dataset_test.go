package dataset

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBoundMarketDatasetRequiresCompleteExactGraph(t *testing.T) {
	payload := mustBoundTestPayload(t, MarketPayloadStockBar, "AAPL", uuid.Nil)
	manifest := mustBoundTestManifest(t, payload, KindBars)

	bound, err := NewBoundMarketDataset(manifest, []*MarketPayload{payload})
	if err != nil {
		t.Fatalf("NewBoundMarketDataset() error = %v", err)
	}
	bindings := bound.Bindings()
	if len(bindings) != 1 || bindings[0].PayloadID != payload.ID() || bindings[0].ContentSHA256 != payload.Digest() {
		t.Fatalf("Bindings() = %#v", bindings)
	}
	if _, err := NewBoundMarketDataset(manifest, nil); err == nil {
		t.Fatal("NewBoundMarketDataset() accepted missing payload")
	}
	if _, err := NewBoundMarketDataset(manifest, []*MarketPayload{payload, payload}); err == nil {
		t.Fatal("NewBoundMarketDataset() accepted duplicate payload")
	}
}

func TestBoundMarketDatasetRejectsMetadataOrKindMismatch(t *testing.T) {
	payload := mustBoundTestPayload(t, MarketPayloadStockBar, "AAPL", uuid.Nil)
	for name, manifest := range map[string]*Manifest{
		"provider": mustBoundTestManifestWithProvider(t, payload, KindBars, "other"),
		"kind":     mustBoundTestManifest(t, payload, KindQuotes),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewBoundMarketDataset(manifest, []*MarketPayload{payload}); err == nil {
				t.Fatal("NewBoundMarketDataset() accepted mismatched graph")
			}
		})
	}
}

func mustBoundTestPayload(t *testing.T, kind MarketPayloadKind, symbol string, underlying uuid.UUID) *MarketPayload {
	t.Helper()
	effectiveAt := time.Date(2026, 1, 2, 15, 30, 0, 0, time.UTC)
	input := MarketPayloadInput{
		Kind: kind, InstrumentID: uuid.New(), UnderlyingInstrumentID: underlying,
		Provider: "alpaca", Feed: "sip", Symbol: symbol, Timeframe: "1d", AdjustmentPolicy: "raw",
		EffectiveAt: effectiveAt, ObservedAt: effectiveAt.Add(time.Minute), AvailableAt: effectiveAt.Add(time.Minute),
		Revision: "original", Bar: &BarPayload{Open: "10", High: "12", Low: "9", Close: "11", Volume: "100", TradeCount: "10", VWAP: "10.5"},
	}
	if underlying != uuid.Nil {
		input.UnderlyingSymbol = "AAPL"
	}
	payload, err := NewMarketPayload(input)
	if err != nil {
		t.Fatalf("NewMarketPayload() error = %v", err)
	}
	return payload
}

func mustBoundTestManifest(t *testing.T, payload *MarketPayload, kind Kind) *Manifest {
	t.Helper()
	return mustBoundTestManifestWithProvider(t, payload, kind, "alpaca")
}

func mustBoundTestManifestWithProvider(t *testing.T, payload *MarketPayload, kind Kind, provider string) *Manifest {
	t.Helper()
	metadata := payload.Metadata()
	manifest, err := NewManifest(ManifestInput{DecisionCutoff: metadata.AvailableAt, Partitions: []PartitionInput{{
		Kind: kind, Provider: provider, Source: "historical_api", Namespace: "test", RequestSHA256: hashBytes([]byte("request")),
		MediaType: "application/json", SymbologyVersion: "occ-v1", AdjustmentPolicy: metadata.AdjustmentPolicy,
		Timezone: "UTC", Calendar: "XNYS", Revision: "original", License: "licensed", RetentionPolicy: "indefinite",
		Observations: []ObservationInput{{
			SourceKey: metadata.Symbol + "@" + formatTime(metadata.EffectiveAt), InstrumentID: metadata.InstrumentID,
			EffectiveAt: metadata.EffectiveAt, PublishedAt: metadata.PublishedAt, ObservedAt: metadata.ObservedAt,
			AvailableAt: metadata.AvailableAt, Revision: metadata.Revision, CorrectionOf: metadata.CorrectionOfSHA256,
			ContentSHA256: payload.Digest(),
		}},
	}}})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	return manifest
}

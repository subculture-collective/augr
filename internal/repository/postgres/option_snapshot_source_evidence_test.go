package postgres

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
)

func TestOptionSnapshotPayloadPreservesExactAndLegacySourceEvidence(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatal(err)
	}
	option, underlying := createPhysicalOptionTermsFixture(t, fixture.ctx, NewInstrumentRepo(fixture.pool))
	at := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	observed := at.Add(time.Minute)
	const symbol = "SPY260815C00500000"
	for _, exact := range []bool{false, true} {
		for _, kind := range []dataset.MarketPayloadKind{dataset.MarketPayloadOptionQuote, dataset.MarketPayloadOptionSnapshot} {
			t.Run(fmt.Sprintf("%s/exact=%t", kind, exact), func(t *testing.T) {
				quote := dataset.QuotePayload{BidPrice: "11.000000000000000001", BidSize: "9007199254740993", AskPrice: "12", AskSize: "2"}
				input := dataset.MarketPayloadInput{Kind: kind, InstrumentID: option.ID, UnderlyingInstrumentID: underlying.ID, UnderlyingSymbol: "SPY", Symbol: symbol, Provider: "alpaca", Feed: "opra", Timeframe: "snapshot", AdjustmentPolicy: "raw", EffectiveAt: at, ObservedAt: observed, AvailableAt: observed, Revision: "original", Quote: &quote}
				if kind == dataset.MarketPayloadOptionSnapshot {
					input.Quote, input.EffectiveAt = nil, observed
					input.Snapshot = &dataset.OptionSnapshotPayload{Quote: quote, LastTradePrice: "11", LastTradeSize: "2", ImpliedVolatility: "0", Delta: "0", Gamma: "0", Theta: "-0.123456789012345678", Vega: "0", Rho: "0"}
				}
				if exact {
					row := []byte(fmt.Sprintf(`{ "latestQuote":{"bp":11.000000000000000001,"bs":9007199254740993,"ap":12,"as":2,"t":%q},"latestTrade":{"p":11,"s":2,"i":9007199254740993,"x":"A","t":%q},"impliedVolatility":0,"greeks":{"delta":0,"gamma":0,"theta":-0.123456789012345678,"vega":0,"rho":0} }`, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)))
					input.SourceEvidence = &dataset.SourcePageEvidence{RequestPath: "/v1beta1/options/snapshots/SPY", Query: "feed=opra&limit=100&page_token=cursor", SymbolKey: symbol, Row: row, Page: []byte(fmt.Sprintf(`{"snapshots":{"%s":%s},"next_page_token":null}`, symbol, row))}
				}
				payload, err := dataset.NewMarketPayload(input)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if _, err := fixture.repo.RecordMarketPayload(fixture.ctx, payload, fixture.createdAt); err != nil {
						t.Fatal(err)
					}
				}
				reloaded, err := NewDatasetRepo(fixture.pool).GetMarketPayload(fixture.ctx, payload.ID())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(reloaded.CanonicalBytes(), payload.CanonicalBytes()) || reloaded.Digest() != payload.Digest() || reloaded.Metadata().UnderlyingInstrumentID != underlying.ID || !bytes.Contains(reloaded.CanonicalBytes(), []byte("11.000000000000000001")) {
					t.Fatal("snapshot source or exact identity lost in SQL replay")
				}
				var raw []byte
				if err := fixture.pool.QueryRow(fixture.ctx, `SELECT canonical_bytes FROM dataset_market_payloads WHERE id=$1`, payload.ID()).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, payload.CanonicalBytes()) || bytes.Contains(raw, []byte("source_evidence")) != exact {
					t.Fatal("stored source envelope presence or bytes changed")
				}
			})
		}
	}
}

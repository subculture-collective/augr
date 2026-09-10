package postgres

import (
	"bytes"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
)

func TestOptionMarketPayloadPreservesExactAndLegacySourceEvidence(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatal(err)
	}
	option, underlying := createPhysicalOptionTermsFixture(t, fixture.ctx, NewInstrumentRepo(fixture.pool))
	at := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	observed := at.Add(25 * time.Hour)
	published := at.Add(24 * time.Hour)
	const symbol = "SPY260815C00500000"
	for _, exact := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact=%t", exact), func(t *testing.T) {
			input := dataset.MarketPayloadInput{
				Kind: dataset.MarketPayloadOptionBar, InstrumentID: option.ID, UnderlyingInstrumentID: underlying.ID,
				UnderlyingSymbol: "SPY", Symbol: symbol, Provider: "alpaca", Feed: "opra", Timeframe: "1Day", AdjustmentPolicy: "raw",
				EffectiveAt: at, PublishedAt: &published, ObservedAt: observed, AvailableAt: observed, Revision: "original",
				Bar: &dataset.BarPayload{Open: "10", High: "12", Low: "9", Close: "11.000000000000000001", Volume: "100", TradeCount: "5", VWAP: "10.5"},
			}
			if exact {
				row := []byte(fmt.Sprintf(`{ "t":%q,"o":10,"h":12,"l":9,"c":11.000000000000000001,"v":100,"n":5,"vw":10.5 }`, at.Format(time.RFC3339Nano)))
				query := url.Values{"symbols": {symbol}, "feed": {"opra"}, "timeframe": {"1Day"}, "start": {at.Format(time.RFC3339Nano)}, "end": {at.Format(time.RFC3339Nano)}, "page_token": {"synthetic-cursor"}}
				input.SourceEvidence = &dataset.SourcePageEvidence{RequestPath: "/v1beta1/options/bars", Query: query.Encode(), SymbolKey: symbol, Row: row, Page: []byte(fmt.Sprintf(`{"bars":{"%s":[%s]},"next_page_token":null}`, symbol, row))}
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
			if !bytes.Equal(reloaded.CanonicalBytes(), payload.CanonicalBytes()) || reloaded.Digest() != payload.Digest() || reloaded.Bar().Close != input.Bar.Close || reloaded.Metadata().UnderlyingInstrumentID != underlying.ID {
				t.Fatal("option evidence or canonical identity changed through SQL replay")
			}
			var raw []byte
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT canonical_bytes FROM dataset_market_payloads WHERE id=$1`, payload.ID()).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, payload.CanonicalBytes()) || bytes.Contains(raw, []byte("source_evidence")) != exact || bytes.Contains(raw, []byte("symbol_key")) != exact {
				t.Fatal("persisted option envelope presence or bytes changed")
			}
		})
	}
}

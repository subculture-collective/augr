package postgres

import (
	"bytes"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
)

func TestOptionTradePayloadPreservesExactAndLegacySourceEvidence(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatal(err)
	}
	option, underlying := createPhysicalOptionTermsFixture(t, fixture.ctx, NewInstrumentRepo(fixture.pool))
	at := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	observed := at.Add(25 * time.Hour)
	const symbol = "SPY260815C00500000"
	for _, exact := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact=%t", exact), func(t *testing.T) {
			input := dataset.MarketPayloadInput{
				Kind: dataset.MarketPayloadOptionTrade, InstrumentID: option.ID, UnderlyingInstrumentID: underlying.ID,
				UnderlyingSymbol: "SPY", Symbol: symbol, Provider: "alpaca", Feed: "opra", Timeframe: "trade", AdjustmentPolicy: "raw",
				EffectiveAt: at, ObservedAt: observed, AvailableAt: observed, Revision: "trade_9007199254740993",
				Trade: &dataset.TradePayload{Price: "11.000000000000000001", Size: "2", Exchange: "C"},
			}
			if exact {
				row := []byte(fmt.Sprintf(`{ "t":%q,"i":9007199254740993,"p":11.000000000000000001,"s":2,"x":"C" }`, at.Format(time.RFC3339Nano)))
				query := url.Values{"symbols": {symbol}, "feed": {"opra"}, "start": {at.Format(time.RFC3339Nano)}, "end": {at.Format(time.RFC3339Nano)}, "page_token": {"synthetic-cursor"}}
				input.SourceEvidence = &dataset.SourcePageEvidence{RequestPath: "/v1beta1/options/trades", Query: query.Encode(), SymbolKey: symbol, Row: row, Page: []byte(fmt.Sprintf(`{"trades":{"%s":[%s]},"next_page_token":null}`, symbol, row))}
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
			if !bytes.Equal(reloaded.CanonicalBytes(), payload.CanonicalBytes()) || reloaded.Digest() != payload.Digest() || !bytes.Contains(reloaded.CanonicalBytes(), []byte("11.000000000000000001")) || reloaded.Metadata().UnderlyingInstrumentID != underlying.ID {
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

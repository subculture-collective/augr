package postgres

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
)

func TestOptionContractPayloadPreservesExactAndLegacySourceEvidence(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatal(err)
	}
	option, underlying := createPhysicalOptionTermsFixture(t, fixture.ctx, NewInstrumentRepo(fixture.pool))
	at := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	const symbol = "SPY260815C00500000"
	const row = `{ "id":"11111111-1111-4111-8111-111111111111","symbol":"SPY260815C00500000","underlying_asset_id":"22222222-2222-4222-8222-222222222222","underlying_symbol":"SPY","type":"call","style":"american","expiration_date":"2026-08-15","strike_price":"500.000","size":"100","status":"active","tradable":true }`
	for _, exact := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact=%t", exact), func(t *testing.T) {
			input := dataset.MarketPayloadInput{Kind: dataset.MarketPayloadOptionContract, InstrumentID: option.ID, UnderlyingInstrumentID: underlying.ID, UnderlyingSymbol: "SPY", Symbol: symbol, Provider: "alpaca", Feed: "reference", Timeframe: "snapshot", AdjustmentPolicy: "raw", EffectiveAt: at, ObservedAt: at, AvailableAt: at, Revision: "original", Contract: &dataset.OptionContractPayload{OptionType: "call", Strike: "500", Expiry: "2026-08-15", Multiplier: "100", Style: "american"}}
			if exact {
				input.SourceEvidence = &dataset.SourcePageEvidence{RequestPath: "/v2/options/contracts/" + symbol, SymbolKey: symbol, Page: []byte(row), Row: []byte(row)}
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
			if !bytes.Equal(reloaded.CanonicalBytes(), payload.CanonicalBytes()) || reloaded.Digest() != payload.Digest() || reloaded.Metadata().UnderlyingInstrumentID != underlying.ID {
				t.Fatal("contract source identity changed in SQL replay")
			}
			var raw []byte
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT canonical_bytes FROM dataset_market_payloads WHERE id=$1`, payload.ID()).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, payload.CanonicalBytes()) || bytes.Contains(raw, []byte("source_evidence")) != exact {
				t.Fatal("stored source or legacy bytes changed")
			}
		})
	}
}

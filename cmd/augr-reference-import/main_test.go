package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
	postgresrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func testBundle(t *testing.T) referenceBundle {
	t.Helper()
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	ref, err := instrument.NewInstrument(instrument.InstrumentInput{IdentityKey: "test:etf", AssetClass: instrument.AssetClassETF, PrimaryVenue: "iex", Currency: "USD", TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.NewFromInt(1), Multiplier: decimal.NewFromInt(1), SettlementMethod: instrument.SettlementPhysical, CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	alias, err := instrument.NewAliasEvent(instrument.AliasEventInput{InstrumentID: ref.ID, Provider: "alpaca", AliasType: instrument.AliasTicker, AliasValue: "SPY", Action: instrument.AliasAssigned, EffectiveAt: at, Source: "fixture", CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := instrument.NewVenueContract(instrument.VenueContractInput{InstrumentID: ref.ID, Venue: "iex", ContractID: "SPY", Currency: "USD", TickSize: ref.TickSize, LotSize: ref.LotSize, Multiplier: ref.Multiplier, SettlementMethod: ref.SettlementMethod, ValidFrom: at, CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := simulation.NewPolicy(simulation.PolicyInput{Schema: simulation.PolicySchemaV1, Assets: []simulation.AssetPolicy{{AssetClass: instrument.AssetClassETF, OrderTypes: []lifecycle.OrderType{lifecycle.OrderMarket}, TimeInForce: []lifecycle.TimeInForce{lifecycle.TimeInForceIOC}, QuoteRequirements: marketdata.QuoteRequirements{RequireSource: true, RequireVenueContract: true, RequireBid: true, RequireAsk: true, RequireBidDepth: true, RequireAskDepth: true, RequireMarketStatus: true, RequireSessionStatus: true, AllowedMarketStatuses: []string{"open"}, AllowedSessionStatuses: []string{"regular"}, MaxAge: time.Second}, MaxDepthParticipation: decimal.RequireFromString("0.25"), FixedLatency: time.Millisecond, Calendar: simulation.CalendarPolicy{Kind: simulation.CalendarExplicitSessions, Sessions: []simulation.SessionWindow{{Label: "fixture", OpenAt: at, CloseAt: at.Add(time.Hour)}}}, Fees: simulation.FeePolicy{Scale: 4}}}})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := policy.NewArtifact(at)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("fixture source only, not production evidence")
	hash := sha256.Sum256(body)
	return referenceBundle{Schema: "augr-reference-bundle-v1", Instrument: *ref, Alias: *alias, Contract: *contract, Policy: *artifact, Sources: []sourceReceipt{{URL: "https://example.invalid/fixture", Body: body, SHA256: hex.EncodeToString(hash[:]), ObservedAt: at.Add(-time.Second)}}}
}

// TestReferenceImportPostgresReplay exercises the actual CLI write path only
// against an explicitly named disposable test database. Fixture rows remain
// available for inspection; this test never deletes immutable evidence.
func TestReferenceImportPostgresReplay(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for disposable database integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgresrepo.NewDB(ctx, dsn)
	if err != nil {
		t.Fatal("disposable database connection failed")
	}
	defer db.Close()
	var databaseName string
	if err := db.Pool.QueryRow(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatal("refusing integration writes outside a database ending in _test")
	}
	t.Setenv("DB_URL", dsn)
	b := testBundle(t)
	suffix := uuid.NewString()
	b.Instrument.IdentityKey = "test:reference-import:" + suffix
	b.Alias.Provider = "test-" + suffix
	b.Contract.ContractID = "TEST-" + strings.ToUpper(suffix)
	apply := func(value referenceBundle) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var output bytes.Buffer
		return run(ctx, []string{"--apply"}, bytes.NewReader(raw), &output)
	}
	if err := apply(b); err != nil {
		t.Fatal(err)
	}
	if err := apply(b); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	var instruments, aliases, contracts int
	if err := db.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM instruments WHERE id=$1),
	 (SELECT count(*) FROM instrument_alias_events WHERE instrument_id=$1),
	 (SELECT count(*) FROM venue_contracts WHERE instrument_id=$1)`, b.Instrument.ID).Scan(&instruments, &aliases, &contracts); err != nil {
		t.Fatal(err)
	}
	if instruments != 1 || aliases != 1 || contracts != 1 {
		t.Fatalf("replay duplicated graph: %d/%d/%d", instruments, aliases, contracts)
	}
	retained, err := postgresrepo.NewSimulationPolicyRepo(db.Pool).GetSimulationPolicyByVersion(ctx, b.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !simulation.SamePolicyArtifactPayload(retained, &b.Policy) {
		t.Fatal("policy readback differs")
	}
	changed := b
	changed.Contract.TickSize = decimal.RequireFromString("0.02")
	if err := apply(changed); err == nil {
		t.Fatal("changed contract replay accepted")
	}
	changed = b
	changed.Instrument.ID = uuid.New()
	changed.Alias.InstrumentID = changed.Instrument.ID
	changed.Contract.InstrumentID = changed.Instrument.ID
	if err := apply(changed); err == nil {
		t.Fatal("alternate canonical ID accepted")
	}
	if err := apply(b); err != nil {
		t.Fatalf("original graph no longer replayable: %v", err)
	}
}

func TestBundleValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*referenceBundle)
		wantError bool
	}{
		{"valid", func(*referenceBundle) {}, false},
		{"digest", func(b *referenceBundle) { b.Sources[0].SHA256 = "bad" }, true},
		{"missing_receipt", func(b *referenceBundle) { b.Sources = nil }, true},
		{"future_source", func(b *referenceBundle) { b.Sources[0].ObservedAt = time.Now().Add(time.Hour) }, true},
		{"backdated_graph", func(b *referenceBundle) { b.Sources[0].ObservedAt = b.Instrument.CreatedAt.Add(time.Second) }, true},
		{"wrong_alias", func(b *referenceBundle) { b.Alias.InstrumentID = uuid.New() }, true},
		{"wrong_contract", func(b *referenceBundle) { b.Contract.InstrumentID = uuid.New() }, true},
		{"wrong_currency", func(b *referenceBundle) { b.Contract.Currency = "EUR" }, true},
		{"retirement", func(b *referenceBundle) { b.Alias.Action = instrument.AliasRetired }, true},
		{"missing_asset", func(b *referenceBundle) { b.Instrument.AssetClass = instrument.AssetClassEquity }, true},
		{"future_creation", func(b *referenceBundle) { b.Instrument.CreatedAt = time.Now().Add(time.Hour) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testBundle(t)
			tc.mutate(&b)
			err := b.validate(time.Now())
			if (err != nil) != tc.wantError {
				t.Fatalf("validation error=%v wantError=%v", err, tc.wantError)
			}
		})
	}
}

func TestReadOnlyDefaultAndCLI(t *testing.T) {
	t.Setenv("DB_URL", "postgres://invalid:do-not-connect@127.0.0.1:1/missing")
	b := testBundle(t)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), nil, bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"applied":false`) {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"--help"}, {"--version"}} {
		if err := run(context.Background(), args, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"unknown":true}`, string(raw) + ` {}`} {
		if err := run(context.Background(), nil, strings.NewReader(raw), &out); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}

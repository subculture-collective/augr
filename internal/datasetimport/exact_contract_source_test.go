package datasetimport

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

type contractCallStub struct{ calls int }

func (s *contractCallStub) GetExactOptionContract(context.Context, string) (data.ExactOptionContractResult, error) {
	s.calls++
	return data.ExactOptionContractResult{}, fmt.Errorf("synthetic unavailable reference")
}

type exactContractResultStub struct {
	result data.ExactOptionContractResult
}

func (s exactContractResultStub) GetExactOptionContract(_ context.Context, symbol string) (data.ExactOptionContractResult, error) {
	if symbol != s.result.Contract.Symbol {
		return data.ExactOptionContractResult{}, fmt.Errorf("synthetic second contract unavailable")
	}
	return s.result, nil
}

type contractReferenceStub struct{ option, underlying *instrument.Instrument }

func (s contractReferenceStub) ResolveAlias(_ context.Context, _ string, kind instrument.AliasType, _ string, _ time.Time) (*instrument.Instrument, error) {
	if kind == instrument.AliasOCC {
		return s.option, nil
	}
	return s.underlying, nil
}

func TestExactContractImportReferenceAndSourceGuards(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 31, 0, 0, time.UTC)
	const symbol = "AAPL260116C00150000"
	const row = `{"id":"11111111-1111-4111-8111-111111111111","symbol":"AAPL260116C00150000","underlying_asset_id":"22222222-2222-4222-8222-222222222222","underlying_symbol":"AAPL","type":"call","style":"american","expiration_date":"2026-01-16","strike_price":"150.000","size":"100","status":"active","tradable":true}`
	for _, mode := range []string{"valid", "nanosecond", "precision cutoff", "second contract failure", "oversized source", "nil option", "nil underlying", "quarantined", "wrong class", "future reference", "multiplier", "style", "expiry", "old observation", "future observation", "changed raw", "changed strike", "symbol mismatch"} {
		t.Run(mode, func(t *testing.T) {
			underlying := &instrument.Instrument{ID: uuid.New(), IdentityKey: "synthetic-equity", AssetClass: instrument.AssetClassEquity, PrimaryVenue: "xnas", Currency: "USD", TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.NewFromInt(1), Multiplier: decimal.NewFromInt(1), SettlementMethod: "physical", Status: instrument.StatusActive, Metadata: []byte(`{}`), CreatedAt: at.Add(-time.Hour)}
			expiry := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
			option := *underlying
			option.ID, option.IdentityKey, option.AssetClass = uuid.New(), "synthetic-option", instrument.AssetClassOption
			option.UnderlyingID, option.Expiration, option.ExerciseStyle, option.Multiplier = &underlying.ID, &expiry, "american", decimal.NewFromInt(100)
			refs := contractReferenceStub{option: &option, underlying: underlying}
			fetched := data.ExactOptionContractResult{ObservedAt: at, Contract: data.ExactOptionContract{Symbol: symbol, UnderlyingSymbol: "AAPL", OptionType: "call", Style: "american", ExpirationDate: "2026-01-16", StrikePrice: "150", Size: "100", Raw: []byte(row)}, Page: data.HistoricalSourcePage{RequestPath: "/v2/options/contracts/" + symbol, Body: []byte(row)}}
			switch mode {
			case "nil option":
				refs.option = nil
			case "nil underlying":
				refs.underlying = nil
			case "quarantined":
				option.Status, option.Metadata = instrument.StatusQuarantined, []byte(`{"source":"synthetic"}`)
			case "wrong class":
				option.AssetClass = instrument.AssetClassEquity
			case "future reference":
				option.CreatedAt = at.Add(time.Hour)
			case "multiplier":
				option.Multiplier = decimal.NewFromInt(50)
			case "style":
				option.ExerciseStyle = "european"
			case "expiry":
				expiry = expiry.Add(24 * time.Hour)
			case "old observation":
				fetched.ObservedAt = at.Add(-time.Second)
			case "future observation":
				fetched.ObservedAt = at.Add(time.Second)
			case "changed raw":
				fetched.Contract.Raw = []byte(`{}`)
			case "oversized source":
				fetched.Page.Body = make([]byte, 65537)
			case "changed strike":
				fetched.Contract.StrikePrice = "151"
			case "symbol mismatch":
				fetched.Contract.Symbol = "AAPL260116P00150000"
			}
			now := at
			request := dataset.MarketImportRequest{Provider: "alpaca", Feed: "reference", Timeframe: "snapshot", AdjustmentPolicy: "raw", Universe: []string{"AAPL"}, From: at, To: at, DecisionCutoff: at}
			if mode == "nanosecond" || mode == "precision cutoff" {
				now = at.Add(time.Nanosecond)
				fetched.ObservedAt = now
				request.To, request.DecisionCutoff = at.Add(time.Microsecond), at.Add(time.Microsecond)
				if mode == "precision cutoff" {
					request.DecisionCutoff = now
				}
			}
			source := &ProviderSource{Mode: ModeOptionContracts, Contracts: exactContractResultStub{fetched}, Instruments: refs, OptionSymbols: []string{symbol}, Clock: func() time.Time { return now }}
			if mode == "second contract failure" {
				source.OptionSymbols = append(source.OptionSymbols, "AAPL260116P00150000")
			}
			result, err := source.FetchMarketPayloads(t.Context(), request)
			if mode != "valid" && mode != "nanosecond" {
				if err == nil || len(result.Payloads) != 0 {
					t.Fatalf("accepted invalid reference/source: %+v %v", result, err)
				}
				return
			}
			if err != nil || len(result.Payloads) != 1 {
				t.Fatalf("valid import failed: %+v %v", result, err)
			}
			payload := result.Payloads[0]
			if payload.AvailableAt().Before(fetched.ObservedAt) || (mode == "nanosecond" && !payload.AvailableAt().Equal(at.Add(time.Microsecond))) {
				t.Fatal("canonical timestamp backdated reference availability")
			}
			restored, err := dataset.MarketPayloadFromCanonical(payload.ID(), payload.Digest(), payload.CanonicalBytes())
			if err != nil || !bytes.Equal(restored.CanonicalBytes(), payload.CanonicalBytes()) {
				t.Fatalf("canonical replay failed: %v", err)
			}
			// Exercise the complete manifest-building path with synthetic source
			// data and no recorder; this is not production license qualification.
			request.MaxPayloads, request.DryRun = 1, true
			request.SourceName, request.Namespace = "synthetic-contract-reference", "synthetic-contract-test"
			request.SymbologyVersion, request.Timezone, request.Calendar = "occ", "America/New_York", "XNYS"
			request.License, request.RetentionPolicy = "synthetic-test-only", "synthetic-test-only"
			importer, err := dataset.NewMarketImporter(source, nil)
			if err != nil {
				t.Fatal(err)
			}
			summary, err := importer.Import(t.Context(), request, at)
			if err != nil || summary.PayloadCount != 1 || summary.PartitionCount != 1 || !summary.DryRun || len(summary.PayloadSHA256) != 1 {
				t.Fatalf("synthetic contract manifest failed: %+v %v", summary, err)
			}
		})
	}
}

func TestExactContractImportPreflightAndProviderFailure(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 31, 0, 0, time.UTC)
	for _, mode := range []string{"provider failure", "missing provider", "empty", "duplicate", "invalid", "outside universe", "wrong feed", "past window", "cutoff", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			provider := &contractCallStub{}
			source := &ProviderSource{Mode: ModeOptionContracts, Contracts: provider, Instruments: resolverStub{}, OptionSymbols: []string{"AAPL260116C00150000"}, Clock: func() time.Time { return at }}
			request := dataset.MarketImportRequest{Provider: "alpaca", Feed: "reference", Timeframe: "snapshot", AdjustmentPolicy: "raw", Universe: []string{"AAPL"}, From: at, To: at, DecisionCutoff: at}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "missing provider":
				source.Contracts = nil
			case "empty":
				source.OptionSymbols = nil
			case "duplicate":
				source.OptionSymbols = append(source.OptionSymbols, source.OptionSymbols[0])
			case "invalid":
				source.OptionSymbols = []string{"AAPL"}
			case "outside universe":
				request.Universe = []string{"SPY"}
			case "wrong feed":
				request.Feed = "opra"
			case "past window":
				request.From, request.To = at.Add(-time.Hour), at.Add(-time.Minute)
			case "cutoff":
				request.DecisionCutoff = at.Add(-time.Second)
			case "cancelled":
				cancel()
			}
			result, err := source.FetchMarketPayloads(ctx, request)
			wantCalls := 0
			if mode == "provider failure" {
				wantCalls = 1
			}
			if err == nil || len(result.Payloads) != 0 || provider.calls != wantCalls {
				t.Fatalf("mode=%s calls=%d result=%+v err=%v", mode, provider.calls, result, err)
			}
		})
	}
}

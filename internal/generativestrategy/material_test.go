package generativestrategy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

func TestBuildObservationMaterialUsesOnlyExactImmutablePayload(t *testing.T) {
	at := time.Date(2026, 9, 3, 14, 30, 0, 0, time.UTC)
	instrumentValue, err := instrument.NewInstrument(instrument.InstrumentInput{
		IdentityKey: "figi:generated-material", AssetClass: instrument.AssetClassEquity, PrimaryVenue: "alpaca", Currency: "USD",
		TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.NewFromInt(1), Multiplier: decimal.NewFromInt(1),
		SettlementMethod: instrument.SettlementPhysical, Metadata: json.RawMessage(`{}`), CreatedAt: at.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := instrument.NewVenueContract(instrument.VenueContractInput{
		InstrumentID: instrumentValue.ID, Venue: "alpaca", ContractID: "AAPL", Currency: "USD",
		TickSize: instrumentValue.TickSize, LotSize: instrumentValue.LotSize, Multiplier: instrumentValue.Multiplier,
		SettlementMethod: instrumentValue.SettlementMethod, ValidFrom: at.Add(-time.Hour), Metadata: json.RawMessage(`{}`), CreatedAt: at.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
		Kind: dataset.MarketPayloadStockBar, InstrumentID: instrumentValue.ID, Provider: "alpaca", Feed: "sip", Symbol: "AAPL",
		Timeframe: "1Min", AdjustmentPolicy: "all", EffectiveAt: at.Add(-time.Minute), ObservedAt: at, AvailableAt: at,
		Revision: "original", Bar: &dataset.BarPayload{Open: "100", High: "102", Low: "99", Close: "101", Volume: "1000", TradeCount: "10", VWAP: "100.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence := ScenarioExecutionEvidence{
		InstrumentID: instrumentValue.ID, VenueContractID: contract.ID, ExecutionPrice: "101", PayloadID: payload.ID(), PayloadSHA256: payload.Digest(),
		PartitionContentSHA256: strings.Repeat("a", 64), SourceKey: "aapl/2026-09-03T14:30:00Z", AvailableAt: at,
	}
	material, err := BuildObservationMaterial(evidence, payload, contract)
	if err != nil {
		t.Fatal(err)
	}
	if material.ObservationContentSHA256 != payload.Digest() || string(material.CanonicalContent) != string(payload.CanonicalBytes()) ||
		material.Snapshot.Bid == nil || material.Snapshot.Ask == nil || !material.Snapshot.Bid.Equal(decimal.NewFromInt(101)) ||
		material.Snapshot.ObservationID != evidence.SourceKey || material.Snapshot.VenueContractID == nil || *material.Snapshot.VenueContractID != contract.ID {
		t.Fatalf("material = %+v", material)
	}
	tampered := evidence
	tampered.PayloadSHA256 = strings.Repeat("b", 64)
	if _, err = BuildObservationMaterial(tampered, payload, contract); err == nil {
		t.Fatal("tampered payload binding accepted")
	}
	wrong := *contract
	wrong.ID = uuid.New()
	if _, err = BuildObservationMaterial(evidence, payload, &wrong); err == nil {
		t.Fatal("wrong venue contract accepted")
	}
}

package alpaca

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestStockQuoteCanonicalSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	reference, err := instrument.NewInstrument(instrument.InstrumentInput{IdentityKey: "fixture:stock-quote", AssetClass: instrument.AssetClassEquity, PrimaryVenue: "fixture", Currency: "USD", TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.NewFromInt(1), Multiplier: decimal.NewFromInt(1), SettlementMethod: instrument.SettlementPhysical, Status: instrument.StatusActive, Metadata: json.RawMessage(`{}`), CreatedAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := instrument.NewVenueContract(instrument.VenueContractInput{InstrumentID: reference.ID, Venue: "fixture", ContractID: "fixture-stock-quote", Currency: "USD", TickSize: reference.TickSize, LotSize: reference.LotSize, Multiplier: reference.Multiplier, SettlementMethod: reference.SettlementMethod, ValidFrom: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour), Metadata: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"symbol":"SPY","quote":{"bp":600.01,"ap":600.02,"bs":80,"as":90,"bx":"V","ax":"V","t":"2026-09-09T11:59:59.123456789Z"}}`)
	for _, name := range []string{"valid", "changed_price", "changed_hash", "changed_raw", "wrong_contract", "backdated"} {
		t.Run(name, func(t *testing.T) {
			evidence, err := decodeStockQuoteEvidence("SPY", "iex", "/v2/stocks/SPY/quotes/latest?currency=USD&feed=iex", raw, now)
			if err != nil {
				t.Fatal(err)
			}
			binding := *contract
			retained := now.Add(time.Second)
			switch name {
			case "changed_price":
				evidence.Bid = decimal.NewFromInt(1)
			case "changed_hash":
				evidence.ResponseSHA256 = "changed"
			case "changed_raw":
				evidence.RawResponse = []byte(`{}`)
			case "wrong_contract":
				binding.InstrumentID = uuid.New()
			case "backdated":
				retained = now.Add(-time.Second)
			}
			snapshot, err := evidence.QuoteSnapshot(*reference, binding, retained)
			if name != "valid" {
				if err == nil {
					t.Fatal("accepted altered source or binding")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.MarketStatus != "" || snapshot.SessionStatus != "" || snapshot.Source != "iex" || snapshot.BidSize == nil || !snapshot.BidSize.Equal(decimal.NewFromInt(80)) || snapshot.AvailableAt == nil || !snapshot.AvailableAt.Equal(retained) || !snapshot.CreatedAt.Equal(retained) {
				t.Fatal("normalization invented facts or changed quantity/availability")
			}
			var metadata struct {
				Receipt    StockQuoteEvidence `json:"receipt"`
				DepthScope string             `json:"depth_scope"`
			}
			if err := json.Unmarshal(snapshot.Metadata, &metadata); err != nil || string(metadata.Receipt.RawResponse) != string(raw) || metadata.Receipt.ExchangeAt.Nanosecond() != 123456789 || metadata.DepthScope != "top_of_book" {
				t.Fatal("lost original source precision or depth scope")
			}
			evidence.ObservedAt = evidence.ObservedAt.Add(time.Second)
			later, err := evidence.QuoteSnapshot(*reference, binding, retained.Add(time.Second))
			if err != nil || later.ObservationID == snapshot.ObservationID {
				t.Fatal("distinct receipts of unchanged provider quote share an observation identity")
			}
		})
	}
}

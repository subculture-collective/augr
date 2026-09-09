package alpaca

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestStockQuotePennyContractPriceDomain(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, asset := range []instrument.AssetClass{instrument.AssetClassEquity, instrument.AssetClassETF} {
		for _, tc := range []struct {
			name, bid, ask, tick string
			reject               bool
		}{
			{"below", "0.98", "0.99", "0.01", true},
			{"straddle", "0.99", "1.00", "0.01", true},
			{"boundary", "1.00", "1.01", "0.01", false},
			{"above", "762.78", "762.88", "0.01", false},
			{"explicit_finer_contract", "0.9998", "0.9999", "0.0001", false},
		} {
			t.Run(string(asset)+"/"+tc.name, func(t *testing.T) {
				ref, err := instrument.NewInstrument(instrument.InstrumentInput{IdentityKey: "fixture:price-domain", AssetClass: asset, PrimaryVenue: "iex", Currency: "USD", TickSize: decimal.RequireFromString(tc.tick), LotSize: decimal.NewFromInt(1), Multiplier: decimal.NewFromInt(1), SettlementMethod: instrument.SettlementPhysical, CreatedAt: now.Add(-time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
				contract, err := instrument.NewVenueContract(instrument.VenueContractInput{InstrumentID: ref.ID, Venue: "iex", ContractID: "SPY", Currency: "USD", TickSize: ref.TickSize, LotSize: ref.LotSize, Multiplier: ref.Multiplier, SettlementMethod: ref.SettlementMethod, ValidFrom: ref.CreatedAt, CreatedAt: ref.CreatedAt})
				if err != nil {
					t.Fatal(err)
				}
				raw := []byte(fmt.Sprintf(`{"symbol":"SPY","quote":{"bp":%s,"ap":%s,"bs":80,"as":90,"bx":"V","ax":"V","c":["R"],"z":"B","t":"2026-09-09T11:59:59Z"}}`, tc.bid, tc.ask))
				evidence, err := decodeStockQuoteEvidence("SPY", "iex", "/v2/stocks/SPY/quotes/latest?currency=USD&feed=iex", raw, now)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := evidence.QuoteSnapshot(*ref, *contract, now.Add(time.Millisecond))
				if (err != nil) != tc.reject {
					t.Fatalf("snapshot error=%v reject=%v", err, tc.reject)
				}
				if tc.reject && snapshot != nil {
					t.Fatal("rejected quote produced snapshot")
				}
			})
		}
	}
}

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
	raw := []byte(`{"symbol":"SPY","quote":{"bp":600.01,"ap":600.02,"bs":80,"as":90,"bx":"V","ax":"V","c":["R"],"z":"B","t":"2026-09-09T11:59:59.123456789Z"}}`)
	for _, name := range []string{"valid", "changed_price", "changed_hash", "changed_raw", "changed_conditions", "changed_tape", "wrong_contract", "backdated"} {
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
			case "changed_conditions":
				evidence.Conditions[0] = "H"
			case "changed_tape":
				evidence.Tape = "A"
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
			if metadata.Receipt.Tape != "B" || len(metadata.Receipt.Conditions) != 1 || metadata.Receipt.Conditions[0] != "R" {
				t.Fatal("lost source quote conditions or tape")
			}
			etfReference := *reference
			etfReference.AssetClass = instrument.AssetClassETF
			if _, err := evidence.QuoteSnapshot(etfReference, binding, retained); err != nil {
				t.Fatal("ETF quote was rejected", err)
			}
			for _, clockCase := range []string{"valid", "tampered_phase", "later_instant", "premature_retention", "wrong_feed", "wrong_exchange"} {
				t.Run("calendar_"+clockCase, func(t *testing.T) {
					clockRaw := []byte(`{"clocks":[{"market":{"acronym":"IEX","mic":"IEXG"},"timestamp":"2026-09-09T11:59:59.123456789Z","phase_until":"2026-09-09T13:30:00Z","phase":"pre","is_market_day":true}]}`)
					path := "/v3/clock?" + url.Values{"markets": {"IEX"}, "time": {evidence.ExchangeAt.Format(time.RFC3339Nano)}}.Encode()
					calendar, err := decodeMarketClockEvidence("IEX", evidence.ExchangeAt, path, clockRaw, now.Add(time.Millisecond))
					if err != nil {
						t.Fatal(err)
					}
					quote := *evidence
					retention := retained
					switch clockCase {
					case "tampered_phase":
						calendar.Phase = "core"
					case "later_instant":
						calendar.At = calendar.At.Add(time.Second)
					case "premature_retention":
						retention = now
					case "wrong_feed":
						quote.Feed = "sip"
					case "wrong_exchange":
						quote.AskExchange = "N"
					}
					joined, err := quote.QuoteSnapshotWithClock(*reference, binding, retention, calendar)
					if clockCase != "valid" {
						if err == nil {
							t.Fatal("accepted invalid clock/quote evidence join")
						}
						return
					}
					if err != nil || joined.SessionStatus != "pre" || joined.MarketStatus != "" || joined.ObservationID == snapshot.ObservationID {
						t.Fatal("lost phase or invented security status", err)
					}
					var joinedMetadata struct {
						Calendar MarketClockEvidence `json:"calendar_receipt"`
					}
					if err := json.Unmarshal(joined.Metadata, &joinedMetadata); err != nil || string(joinedMetadata.Calendar.RawResponse) != string(clockRaw) {
						t.Fatal("lost calendar source provenance")
					}
					testQuoteStatusJoin(t, quote, *reference, binding, retention, calendar)
				})
			}
			evidence.ObservedAt = evidence.ObservedAt.Add(time.Second)
			later, err := evidence.QuoteSnapshot(*reference, binding, retained.Add(time.Second))
			if err != nil || later.ObservationID == snapshot.ObservationID {
				t.Fatal("distinct receipts of unchanged provider quote share an observation identity")
			}
		})
	}
}

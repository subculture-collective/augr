package alpaca

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
)

// QuoteSnapshot converts a receipt into canonical top-of-book facts using the
// explicitly supplied retained reference/contract binding. It does not persist,
// resolve ticker aliases, infer trading status, or authorize execution.
func (e *StockQuoteEvidence) QuoteSnapshot(reference instrument.Instrument, contract instrument.VenueContract, retainedAt time.Time) (*marketdata.QuoteSnapshot, error) {
	if e == nil || e.ObservedAt.IsZero() || retainedAt.IsZero() || retainedAt.Before(e.ObservedAt) {
		return nil, fmt.Errorf("alpaca: quote retention must follow actual receipt")
	}
	if err := reference.Validate(); err != nil {
		return nil, err
	}
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	if reference.AssetClass != instrument.AssetClassEquity || reference.Currency != "USD" || contract.Currency != "USD" || contract.InstrumentID != reference.ID {
		return nil, fmt.Errorf("alpaca: stock quote requires an explicit matching USD equity contract")
	}
	path := "/v2/stocks/" + url.PathEscape(e.Ticker) + "/quotes/latest?" + url.Values{"feed": {e.Feed}, "currency": {"USD"}}.Encode()
	decoded, err := decodeStockQuoteEvidence(e.Ticker, e.Feed, path, e.RawResponse, e.ObservedAt)
	if err != nil {
		return nil, err
	}
	if e.RequestPath != path || e.ResponseSHA256 != decoded.ResponseSHA256 || !e.ExchangeAt.Equal(decoded.ExchangeAt) || !e.Bid.Equal(decoded.Bid) || !e.Ask.Equal(decoded.Ask) || !e.BidSize.Equal(decoded.BidSize) || !e.AskSize.Equal(decoded.AskSize) || e.BidExchange != decoded.BidExchange || e.AskExchange != decoded.AskExchange {
		return nil, fmt.Errorf("alpaca: quote receipt fields disagree with retained source bytes")
	}
	// Re-decoding prevents edited structured fields from bypassing raw evidence.
	if e.Tape != decoded.Tape || !slices.Equal(e.Conditions, decoded.Conditions) {
		return nil, fmt.Errorf("alpaca: quote conditions disagree with retained source bytes")
	}
	bidSize, askSize, err := decoded.ShareSizes()
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(struct {
		Schema     string              `json:"schema"`
		DepthScope string              `json:"depth_scope"`
		SizeUnit   string              `json:"size_unit"`
		Receipt    *StockQuoteEvidence `json:"receipt"`
	}{"alpaca-stock-quote-normalization-v1", "top_of_book", "shares", decoded})
	if err != nil {
		return nil, err
	}
	return marketdata.NewQuoteSnapshot(marketdata.QuoteSnapshotInput{
		InstrumentID: reference.ID, VenueContractID: &contract.ID, Provider: "alpaca", Venue: contract.Venue,
		Source: decoded.Feed, ObservationNamespace: "alpaca-stock-quotes/" + decoded.Feed,
		ObservationID: decoded.ResponseSHA256 + ":" + decoded.ObservedAt.Format(time.RFC3339Nano), ExchangeAt: &decoded.ExchangeAt, ReceivedAt: decoded.ObservedAt,
		AvailableAt: &retainedAt, CreatedAt: retainedAt, Bid: &decoded.Bid, Ask: &decoded.Ask,
		BidSize: &bidSize, AskSize: &askSize,
		Bids: []marketdata.DepthLevelInput{{Price: decoded.Bid, Size: bidSize}},
		Asks: []marketdata.DepthLevelInput{{Price: decoded.Ask, Size: askSize}}, Metadata: metadata,
	})
}

package alpaca

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
)

// IEXRegularQuoteStatus denotes a quote's market-maker-open condition, not
// consolidated market coverage or security-wide halt clearance. A simulation
// policy must explicitly admit this source-scoped status to execute against it.
const IEXRegularQuoteStatus = "alpaca:iex:quote:regular_market_maker_open"

// QuoteConditionEvidence retains the source dictionary, not a trading signal.
type QuoteConditionEvidence struct {
	Tape, RequestPath, ResponseSHA256 string
	ObservedAt                        time.Time
	RawResponse                       []byte
}

// QuoteConditions fetches the quote (never trade) dictionary for an exact tape.
// Contract: https://docs.alpaca.markets/us/reference/stockmetaconditions-1.md
func (p *StockQuoteProvider) QuoteConditions(ctx context.Context, tape string) (*QuoteConditionEvidence, error) {
	if p == nil || p.client == nil || p.apiKey == "" || p.apiSecret == "" || (tape != "A" && tape != "B" && tape != "C") {
		return nil, fmt.Errorf("alpaca: quote conditions require credentials and an exact tape")
	}
	path := "/v2/stocks/meta/conditions/quote?tape=" + tape
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("alpaca: create quote conditions request")
	}
	req.Header.Set("APCA-API-KEY-ID", p.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", p.apiSecret)
	response, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("alpaca: quote conditions transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpaca: quote conditions HTTP %d", response.StatusCode)
	}
	const maxResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, fmt.Errorf("alpaca: quote conditions response unreadable or oversized")
	}
	var dictionary map[string]string
	if err := json.Unmarshal(body, &dictionary); err != nil || len(dictionary) == 0 {
		return nil, fmt.Errorf("alpaca: quote conditions require a nonempty dictionary")
	}
	digest := sha256.Sum256(body)
	return &QuoteConditionEvidence{Tape: tape, RequestPath: path, ResponseSHA256: hex.EncodeToString(digest[:]), ObservedAt: time.Now().UTC(), RawResponse: append([]byte(nil), body...)}, nil
}

// QuoteSnapshotWithStatus joins exact source quote, tape dictionary and calendar
// evidence. Only the verified single R condition is currently supported; mixed,
// unknown or nonregular conditions fail closed rather than being discarded.
func (e *StockQuoteEvidence) QuoteSnapshotWithStatus(reference instrument.Instrument, contract instrument.VenueContract, retainedAt time.Time, clock *MarketClockEvidence, conditions *QuoteConditionEvidence) (*marketdata.QuoteSnapshot, error) {
	if e == nil || conditions == nil || conditions.ObservedAt.IsZero() || retainedAt.Before(conditions.ObservedAt) || e.Tape != conditions.Tape || (e.Tape != "A" && e.Tape != "B" && e.Tape != "C") || len(e.Conditions) != 1 || e.Conditions[0] != "R" {
		return nil, fmt.Errorf("alpaca: missing or unsupported exact quote condition evidence")
	}
	digest := sha256.Sum256(conditions.RawResponse)
	var dictionary map[string]string
	if conditions.RequestPath != "/v2/stocks/meta/conditions/quote?tape="+e.Tape || conditions.ResponseSHA256 != hex.EncodeToString(digest[:]) || json.Unmarshal(conditions.RawResponse, &dictionary) != nil || dictionary["R"] != "Regular Market Maker Open" {
		return nil, fmt.Errorf("alpaca: quote condition dictionary provenance or meaning mismatch")
	}
	snapshot, err := e.QuoteSnapshotWithClock(reference, contract, retainedAt, clock)
	if err != nil {
		return nil, err
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(snapshot.Metadata, &metadata); err != nil {
		return nil, err
	}
	metadata["quote_condition_receipt"], err = json.Marshal(conditions)
	if err != nil {
		return nil, err
	}
	metadata["market_status_scope"] = json.RawMessage(`"single_exchange_quote_condition"`)
	snapshot.Metadata, err = json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	snapshot.MarketStatus = IEXRegularQuoteStatus
	snapshot.ObservationNamespace += "/quote-condition-v1"
	snapshot.ObservationID += ":" + conditions.ResponseSHA256 + ":" + conditions.ObservedAt.Format(time.RFC3339Nano)
	return snapshot, nil
}

package dataset

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func snapshotSourceEventTime(fields map[string]json.RawMessage, observed time.Time) (time.Time, error) {
	var stamp string
	if json.Unmarshal(fields["t"], &stamp) != nil {
		return time.Time{}, fmt.Errorf("snapshot source event time required")
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || at.Before(time.Unix(0, 0)) || at.After(observed) {
		return time.Time{}, fmt.Errorf("snapshot source event time outside observation boundary")
	}
	return at, nil
}

func validateAlpacaSnapshotSource(input *MarketPayloadInput) error {
	if input.Snapshot == nil || input.ObservedAt.IsZero() || !input.EffectiveAt.Equal(input.ObservedAt) {
		return fmt.Errorf("snapshot source requires acquisition-time aggregate")
	}
	fields, err := validateSnapshotSourceIdentity(input)
	if err != nil {
		return err
	}
	quote, err := sourceObjectFields(fields["latestQuote"])
	if err != nil {
		return err
	}
	quoteAt, err := snapshotSourceEventTime(quote, input.ObservedAt)
	if err != nil {
		return err
	}
	quoteInput := *input
	quoteInput.Quote, quoteInput.EffectiveAt = &input.Snapshot.Quote, quoteAt
	if err := validateAlpacaSnapshotQuoteSource(&quoteInput); err != nil {
		return err
	}
	if err := validateSnapshotSourceNumber(fields, "impliedVolatility", input.Snapshot.ImpliedVolatility, false, false); err != nil {
		return err
	}
	greeks, err := sourceObjectFields(fields["greeks"])
	if err != nil {
		return err
	}
	for _, field := range []struct{ name, want string }{
		{"delta", input.Snapshot.Delta},
		{"gamma", input.Snapshot.Gamma},
		{"theta", input.Snapshot.Theta},
		{"vega", input.Snapshot.Vega},
		{"rho", input.Snapshot.Rho},
	} {
		if err := validateSnapshotSourceNumber(greeks, field.name, field.want, true, false); err != nil {
			return err
		}
	}
	// The existing aggregate schema cannot express absent trade values. Reject
	// incomplete aggregates rather than inventing zero; quotes remain separate.
	trade, err := sourceObjectFields(fields["latestTrade"])
	if err != nil {
		return fmt.Errorf("complete snapshot aggregate requires an observed trade")
	}
	if err := validateSnapshotSourceNumber(trade, "p", input.Snapshot.LastTradePrice, false, false); err != nil {
		return err
	}
	if err := validateSnapshotSourceNumber(trade, "s", input.Snapshot.LastTradeSize, false, true); err != nil {
		return err
	}
	if input.Snapshot.LastTradePrice == "0" || input.Snapshot.LastTradeSize == "0" {
		return fmt.Errorf("snapshot trade price and size must be positive")
	}
	if _, err := snapshotSourceEventTime(trade, input.ObservedAt); err != nil {
		return err
	}
	id, err := strconv.ParseInt(string(trade["i"]), 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("snapshot trade identity required")
	}
	var exchange string
	if json.Unmarshal(trade["x"], &exchange) != nil || strings.TrimSpace(exchange) == "" || len(exchange) > 32 || strings.IndexFunc(exchange, unicode.IsControl) >= 0 {
		return fmt.Errorf("snapshot trade exchange required")
	}
	return nil
}

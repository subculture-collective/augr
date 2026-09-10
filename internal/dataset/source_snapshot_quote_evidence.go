package dataset

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/shopspring/decimal"
)

func validateSnapshotSourceIdentity(input *MarketPayloadInput) (map[string]json.RawMessage, error) {
	source := input.SourceEvidence
	contract, err := domain.ParseStrictOCC(input.Symbol)
	if err != nil || contract.OCCSymbol != input.Symbol || contract.Underlying != input.UnderlyingSymbol || input.Timeframe != "snapshot" || input.AdjustmentPolicy != "raw" || (input.Feed != "opra" && input.Feed != "indicative") || source.SymbolKey != input.Symbol || source.RequestPath != "/v1beta1/options/snapshots/"+input.UnderlyingSymbol {
		return nil, fmt.Errorf("unsupported exact snapshot source identity")
	}
	query, err := url.ParseQuery(source.Query)
	if err != nil {
		return nil, err
	}
	for key, values := range query {
		switch key {
		case "feed", "limit", "page_token":
			if len(values) != 1 {
				return nil, fmt.Errorf("duplicate snapshot source query parameter")
			}
		default:
			return nil, fmt.Errorf("unsupported snapshot source query parameter")
		}
	}
	if query.Get("feed") != input.Feed || query.Get("limit") != "100" {
		return nil, fmt.Errorf("snapshot source request mismatch")
	}
	return sourceObjectFields(source.Row)
}

func validateSnapshotSourceNumber(fields map[string]json.RawMessage, name, want string, signed, integral bool) error {
	raw := fields[name]
	if len(raw) == 0 || len(raw) > 128 || raw[0] == '"' {
		return fmt.Errorf("missing exact snapshot source number %s", name)
	}
	value, err := decimal.NewFromString(string(raw))
	if err != nil || value.Exponent() < -128 || value.Exponent() > 128 || (!signed && value.IsNegative()) || value.String() != want || (integral && !value.Equal(value.Truncate(0))) {
		return fmt.Errorf("exact snapshot source number %s mismatch", name)
	}
	return nil
}

func validateAlpacaSnapshotQuoteSource(input *MarketPayloadInput) error {
	if input.Quote == nil {
		return fmt.Errorf("snapshot source requires quote payload")
	}
	fields, err := validateSnapshotSourceIdentity(input)
	if err != nil {
		return err
	}
	quote, err := sourceObjectFields(fields["latestQuote"])
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name, want string
		integral   bool
	}{{"bp", input.Quote.BidPrice, false}, {"bs", input.Quote.BidSize, true}, {"ap", input.Quote.AskPrice, false}, {"as", input.Quote.AskSize, true}} {
		if err := validateSnapshotSourceNumber(quote, field.name, field.want, false, field.integral); err != nil {
			return err
		}
	}
	var stamp string
	if json.Unmarshal(quote["t"], &stamp) != nil {
		return fmt.Errorf("snapshot source quote time missing")
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || !at.Equal(input.EffectiveAt) {
		return fmt.Errorf("snapshot source quote time mismatch")
	}
	return nil
}

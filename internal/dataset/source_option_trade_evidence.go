package dataset

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

func validateAlpacaOptionTradeSource(input *MarketPayloadInput) error {
	source := input.SourceEvidence
	if input.Trade == nil || input.Timeframe != "trade" || source.RequestPath != "/v1beta1/options/trades" || source.SymbolKey != input.Symbol || input.AdjustmentPolicy != "raw" || (input.Feed != "opra" && input.Feed != "indicative") {
		return fmt.Errorf("unsupported Alpaca option source identity")
	}
	query, err := url.ParseQuery(source.Query)
	if err != nil {
		return err
	}
	for key, values := range query {
		switch key {
		case "symbols", "feed", "start", "end", "page_token", "limit":
			if len(values) != 1 {
				return fmt.Errorf("duplicate Alpaca option source query parameter")
			}
		default:
			return fmt.Errorf("unsupported Alpaca option source query parameter")
		}
	}
	for key, want := range map[string]string{"symbols": input.Symbol, "feed": input.Feed} {
		if want == "" || len(query[key]) != 1 || query.Get(key) != want {
			return fmt.Errorf("alpaca option source request mismatch: %s", key)
		}
	}
	if len(query["start"]) != 1 || len(query["end"]) != 1 {
		return fmt.Errorf("alpaca option source interval missing")
	}
	from, fromErr := time.Parse(time.RFC3339Nano, query.Get("start"))
	to, toErr := time.Parse(time.RFC3339Nano, query.Get("end"))
	if fromErr != nil || toErr != nil || from.After(to) || input.EffectiveAt.Before(from) || input.EffectiveAt.After(to) {
		return fmt.Errorf("alpaca option source interval mismatch")
	}
	fields, err := sourceObjectFields(source.Row)
	if err != nil {
		return err
	}
	for _, field := range []struct{ name, want string }{
		{"p", input.Trade.Price},
		{"s", input.Trade.Size},
	} {
		raw, ok := fields[field.name]
		if !ok || len(raw) == 0 || len(raw) > 128 || raw[0] == '"' {
			return fmt.Errorf("missing exact option source field %s", field.name)
		}
		value, err := decimal.NewFromString(string(raw))
		if err != nil || value.Exponent() < -128 || value.Exponent() > 128 || !value.IsPositive() || value.String() != field.want {
			return fmt.Errorf("option source field %s mismatch", field.name)
		}
		if field.name == "s" && !value.Equal(value.Truncate(0)) {
			return fmt.Errorf("option source trade size must be integral")
		}
	}
	id, err := strconv.ParseInt(string(fields["i"]), 10, 64)
	if err != nil || id <= 0 || input.Revision != "trade_"+strconv.FormatInt(id, 10) {
		return fmt.Errorf("option source trade identity mismatch")
	}
	var exchange string
	if json.Unmarshal(fields["x"], &exchange) != nil || exchange != input.Trade.Exchange {
		return fmt.Errorf("option source exchange mismatch")
	}
	var stamp string
	if json.Unmarshal(fields["t"], &stamp) != nil {
		return fmt.Errorf("invalid option source timestamp")
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || !at.Equal(input.EffectiveAt) {
		return fmt.Errorf("option source timestamp mismatch")
	}
	return nil
}

package dataset

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/shopspring/decimal"
)

func validateAlpacaOptionBarSource(input *MarketPayloadInput) error {
	source := input.SourceEvidence
	if input.Bar == nil || source.RequestPath != "/v1beta1/options/bars" || source.SymbolKey != input.Symbol || input.AdjustmentPolicy != "raw" || (input.Feed != "opra" && input.Feed != "indicative") {
		return fmt.Errorf("unsupported Alpaca option source identity")
	}
	query, err := url.ParseQuery(source.Query)
	if err != nil {
		return err
	}
	for key, values := range query {
		switch key {
		case "symbols", "feed", "timeframe", "start", "end", "page_token", "limit":
			if len(values) != 1 {
				return fmt.Errorf("duplicate Alpaca option source query parameter")
			}
		default:
			return fmt.Errorf("unsupported Alpaca option source query parameter")
		}
	}
	units := map[string]string{"1d": "1Day", "1Day": "1Day", "1m": "1Min", "1Min": "1Min", "5m": "5Min", "15m": "15Min", "1h": "1Hour"}
	for key, want := range map[string]string{"symbols": input.Symbol, "feed": input.Feed, "timeframe": units[input.Timeframe]} {
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
		{"o", input.Bar.Open},
		{"h", input.Bar.High},
		{"l", input.Bar.Low},
		{"c", input.Bar.Close},
		{"v", input.Bar.Volume},
		{"n", input.Bar.TradeCount},
		{"vw", input.Bar.VWAP},
	} {
		raw, ok := fields[field.name]
		if !ok || len(raw) == 0 || len(raw) > 128 || raw[0] == '"' {
			return fmt.Errorf("missing exact option source field %s", field.name)
		}
		value, err := decimal.NewFromString(string(raw))
		if err != nil || value.Exponent() < -128 || value.Exponent() > 128 || value.String() != field.want {
			return fmt.Errorf("option source field %s mismatch", field.name)
		}
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

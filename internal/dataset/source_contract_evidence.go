package dataset

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// validateAlpacaContractSource binds a current reference observation. Matching
// provider size is not approval of an execution multiplier or deliverable;
// instrument/reference qualification must still succeed separately.
func validateAlpacaContractSource(input *MarketPayloadInput) error {
	source := input.SourceEvidence
	if input.Contract == nil || input.Feed != "reference" || input.Timeframe != "snapshot" || input.AdjustmentPolicy != "raw" || input.PublishedAt != nil || !input.EffectiveAt.Equal(input.ObservedAt) {
		return fmt.Errorf("contract source requires current reference observation metadata")
	}
	if source.RequestPath != "/v2/options/contracts/"+input.Symbol || source.SymbolKey != input.Symbol {
		return fmt.Errorf("contract source request identity mismatch")
	}
	fields, err := sourceObjectFields(source.Row)
	if err != nil {
		return err
	}
	for _, field := range []struct{ name, want string }{
		{"symbol", input.Symbol},
		{"underlying_symbol", input.UnderlyingSymbol},
		{"type", input.Contract.OptionType},
		{"style", input.Contract.Style},
		{"expiration_date", input.Contract.Expiry},
	} {
		var got string
		if json.Unmarshal(fields[field.name], &got) != nil || got != field.want {
			return fmt.Errorf("contract source field %s mismatch", field.name)
		}
	}
	occ, err := domain.ParseStrictOCC(input.Symbol)
	if err != nil || occ.Underlying != input.UnderlyingSymbol || string(occ.OptionType) != input.Contract.OptionType || occ.Expiry.Format("2006-01-02") != input.Contract.Expiry {
		return fmt.Errorf("contract source OCC identity mismatch")
	}
	for _, field := range []struct{ name, want string }{
		{"strike_price", input.Contract.Strike}, {"size", input.Contract.Multiplier},
	} {
		var raw string
		if json.Unmarshal(fields[field.name], &raw) != nil || len(raw) == 0 || len(raw) > 128 {
			return fmt.Errorf("contract source exact decimal required")
		}
		value, err := decimal.NewFromString(raw)
		if err != nil || value.Exponent() < -128 || value.Exponent() > 128 || value.String() != field.want {
			return fmt.Errorf("contract source decimal mismatch")
		}
		if field.name == "size" && (!value.IsPositive() || !value.Equal(value.Truncate(0))) {
			return fmt.Errorf("contract source size must be positive integral")
		}
		if field.name == "strike_price" {
			encoded, err := decimal.NewFromString(input.Symbol[len(input.Symbol)-8:])
			if err != nil || !value.Equal(encoded.Shift(-3)) {
				return fmt.Errorf("contract strike disagrees with OCC symbol")
			}
		}
	}
	for _, name := range []string{"id", "underlying_asset_id"} {
		var raw string
		if json.Unmarshal(fields[name], &raw) != nil {
			return fmt.Errorf("contract provider identity missing")
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || id.String() != raw {
			return fmt.Errorf("contract provider identity invalid")
		}
	}
	var status string
	if json.Unmarshal(fields["status"], &status) != nil || (status != "active" && status != "inactive") {
		return fmt.Errorf("contract source status missing")
	}
	if string(fields["tradable"]) != "true" && string(fields["tradable"]) != "false" {
		return fmt.Errorf("contract source explicit tradable flag required")
	}
	return nil
}

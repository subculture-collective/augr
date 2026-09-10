package alpaca

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func decodeExactOptionContract(raw []byte) (data.ExactOptionContract, error) {
	fields, err := exactOptionFields(raw, 65536)
	if err != nil {
		return data.ExactOptionContract{}, err
	}
	result := data.ExactOptionContract{Raw: bytes.Clone(raw)}
	for _, field := range []struct {
		name string
		dest *string
	}{
		{"id", &result.ProviderID},
		{"symbol", &result.Symbol},
		{"underlying_symbol", &result.UnderlyingSymbol},
		{"underlying_asset_id", &result.UnderlyingAssetID},
		{"type", &result.OptionType},
		{"style", &result.Style},
		{"expiration_date", &result.ExpirationDate},
		{"strike_price", &result.StrikePrice},
		{"size", &result.Size},
		{"status", &result.Status},
	} {
		if json.Unmarshal(fields[field.name], field.dest) != nil || *field.dest == "" || strings.TrimSpace(*field.dest) != *field.dest || len(*field.dest) > 128 {
			return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: exact contract field %s required", field.name)
		}
	}
	for _, id := range []string{result.ProviderID, result.UnderlyingAssetID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: canonical contract source UUID required")
		}
	}
	contract, err := domain.ParseStrictOCC(result.Symbol)
	if err != nil || contract.Underlying != result.UnderlyingSymbol || string(contract.OptionType) != result.OptionType || contract.Expiry.Format("2006-01-02") != result.ExpirationDate {
		return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: exact contract identity mismatch")
	}
	if (result.Style != "american" && result.Style != "european") || (result.Status != "active" && result.Status != "inactive") {
		return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: unsupported explicit contract style or status")
	}
	// JSON null unmarshals into bool without error; reject it explicitly.
	tradable := bytes.TrimSpace(fields["tradable"])
	if (!bytes.Equal(tradable, []byte("true")) && !bytes.Equal(tradable, []byte("false"))) || json.Unmarshal(tradable, &result.Tradable) != nil {
		return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: explicit contract tradable flag required")
	}
	strike, err := decimal.NewFromString(result.StrikePrice)
	if err != nil || strike.Exponent() < -128 || strike.Exponent() > 128 || strike.IsNegative() {
		return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: exact contract strike invalid")
	}
	occStrike, err := decimal.NewFromString(result.Symbol[len(result.Symbol)-8:])
	if err != nil || !strike.Equal(occStrike.Shift(-3)) {
		return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: source strike disagrees with OCC identity")
	}
	size, err := decimal.NewFromString(result.Size)
	if err != nil || size.Exponent() < -128 || size.Exponent() > 128 || !size.IsPositive() || !size.Equal(size.Truncate(0)) {
		return data.ExactOptionContract{}, fmt.Errorf("alpaca/options: exact positive integral contract size required")
	}
	result.StrikePrice, result.Size = strike.String(), size.String()
	return result, nil
}

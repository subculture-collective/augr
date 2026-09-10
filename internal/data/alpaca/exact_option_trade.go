package alpaca

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/shopspring/decimal"
)

func decodeExactOptionTrade(raw []byte) (data.ExactOptionTrade, error) {
	fields, err := exactOptionFields(raw, 65536)
	if err != nil {
		return data.ExactOptionTrade{}, err
	}
	trade := data.ExactOptionTrade{Raw: bytes.Clone(raw)}
	for _, field := range []struct {
		name string
		dest *string
	}{{"p", &trade.Price}, {"s", &trade.Size}} {
		token := fields[field.name]
		if len(token) == 0 || len(token) > 128 {
			return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: missing exact trade number")
		}
		d := json.NewDecoder(bytes.NewReader(token))
		d.UseNumber()
		var value any
		if err := d.Decode(&value); err != nil {
			return data.ExactOptionTrade{}, err
		}
		number, ok := value.(json.Number)
		if !ok {
			return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: nonnumeric exact trade field")
		}
		parsed, err := decimal.NewFromString(number.String())
		if err != nil || parsed.Exponent() < -128 || parsed.Exponent() > 128 || !parsed.IsPositive() {
			return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: exact trade number out of bounds")
		}
		if field.name == "s" && !parsed.Equal(parsed.Truncate(0)) {
			return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: fractional trade size")
		}
		*field.dest = parsed.String()
	}
	// IDs are integers, not floating-point values or decimal exponents.
	id := string(fields["i"])
	parsedID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || parsedID <= 0 {
		return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: invalid exact trade identity")
	}
	trade.ProviderID = strconv.FormatInt(parsedID, 10)
	var stamp string
	if err := json.Unmarshal(fields["t"], &stamp); err != nil {
		return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: missing exact trade timestamp")
	}
	trade.Timestamp, err = time.Parse(time.RFC3339Nano, stamp)
	if err != nil || trade.Timestamp.Before(time.Unix(0, 0)) {
		return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: invalid exact trade timestamp")
	}
	trade.Timestamp = trade.Timestamp.UTC()
	if err := json.Unmarshal(fields["x"], &trade.Exchange); err != nil || trade.Exchange == "" || len(trade.Exchange) > 32 || strings.TrimSpace(trade.Exchange) != trade.Exchange || strings.ContainsAny(trade.Exchange, "\r\n\t\x00") {
		return data.ExactOptionTrade{}, fmt.Errorf("alpaca/options: invalid exact trade exchange")
	}
	return trade, nil
}

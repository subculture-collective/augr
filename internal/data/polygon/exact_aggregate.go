package polygon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/shopspring/decimal"
)

// exactAggregate preserves source bytes and decimal values independently of the
// legacy floating-point OHLCV interface. Empty optional fields mean unknown,
// never a manufactured zero.
type exactAggregate struct {
	Open, High, Low, Close, Volume string
	TradeCount, VWAP               string
	Timestamp                      int64
	Raw                            json.RawMessage
}

func decodeExactAggregate(raw json.RawMessage) (exactAggregate, error) {
	fields, err := exactObjectFields(raw)
	if err != nil {
		return exactAggregate{}, fmt.Errorf("polygon: exact aggregate object: %w", err)
	}
	result := exactAggregate{Raw: bytes.Clone(raw)}
	for _, field := range []struct {
		name     string
		dest     *string
		required bool
	}{
		{"o", &result.Open, true},
		{"h", &result.High, true},
		{"l", &result.Low, true},
		{"c", &result.Close, true},
		{"v", &result.Volume, true},
		{"n", &result.TradeCount, false},
		{"vw", &result.VWAP, false},
	} {
		token, ok := fields[field.name]
		if !ok || bytes.Equal(bytes.TrimSpace(token), []byte("null")) {
			if field.required {
				return exactAggregate{}, fmt.Errorf("polygon: missing exact aggregate field %s", field.name)
			}
			continue
		}
		// Reject quoted numbers and non-numeric JSON without converting to float.
		var value any
		decoder := json.NewDecoder(bytes.NewReader(token))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return exactAggregate{}, fmt.Errorf("polygon: numeric field %s: %w", field.name, err)
		}
		number, ok := value.(json.Number)
		if !ok || len(number.String()) > 128 {
			return exactAggregate{}, fmt.Errorf("polygon: invalid numeric field %s", field.name)
		}
		parsed, err := decimal.NewFromString(number.String())
		if err != nil || parsed.Exponent() < -128 || parsed.Exponent() > 128 || parsed.IsNegative() {
			return exactAggregate{}, fmt.Errorf("polygon: out-of-range numeric field %s", field.name)
		}
		if field.name == "n" && !parsed.Equal(parsed.Truncate(0)) {
			return exactAggregate{}, fmt.Errorf("polygon: fractional trade count")
		}
		*field.dest = parsed.String()
	}
	timestamp := string(fields["t"])
	value, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || value < 0 {
		return exactAggregate{}, fmt.Errorf("polygon: missing or invalid exact timestamp")
	}
	result.Timestamp = value
	return result, nil
}

// exactObjectFields rejects duplicate keys instead of silently allowing the
// JSON decoder's last-value-wins interpretation to become immutable evidence.
func exactObjectFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	return exactObjectFieldsBounded(raw, 65536)
}

func exactObjectFieldsBounded(raw json.RawMessage, limit int) (map[string]json.RawMessage, error) {
	if len(raw) > limit {
		return nil, fmt.Errorf("aggregate exceeds source row size bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("aggregate must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid aggregate key")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate aggregate key %s", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("unterminated aggregate object")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing aggregate data")
	}
	return fields, nil
}

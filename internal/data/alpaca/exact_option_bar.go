package alpaca

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

// exactOptionFields rejects duplicate keys and bounds untrusted source objects.
func exactOptionFields(raw []byte, limit int) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > limit {
		return nil, fmt.Errorf("alpaca/options: exact source size out of bounds")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("alpaca/options: exact source must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("alpaca/options: invalid source key")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("alpaca/options: duplicate source key")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("alpaca/options: invalid source terminator")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("alpaca/options: trailing source data")
	}
	return fields, nil
}

func decodeExactOptionBar(raw []byte) (data.ExactHistoricalBar, error) {
	fields, err := exactOptionFields(raw, 65536)
	if err != nil {
		return data.ExactHistoricalBar{}, err
	}
	bar := data.ExactHistoricalBar{Raw: bytes.Clone(raw)}
	for _, field := range []struct {
		name string
		dest *string
	}{
		{"o", &bar.Open},
		{"h", &bar.High},
		{"l", &bar.Low},
		{"c", &bar.Close},
		{"v", &bar.Volume},
		{"n", &bar.TradeCount},
		{"vw", &bar.VWAP},
	} {
		token, exists := fields[field.name]
		if !exists || len(token) == 0 || len(token) > 128 {
			return data.ExactHistoricalBar{}, fmt.Errorf("alpaca/options: missing exact bar field %s", field.name)
		}
		d := json.NewDecoder(bytes.NewReader(token))
		d.UseNumber()
		var value any
		if err := d.Decode(&value); err != nil {
			return data.ExactHistoricalBar{}, err
		}
		number, ok := value.(json.Number)
		if !ok {
			return data.ExactHistoricalBar{}, fmt.Errorf("alpaca/options: nonnumeric exact bar field %s", field.name)
		}
		parsed, err := decimal.NewFromString(number.String())
		if err != nil || parsed.Exponent() < -128 || parsed.Exponent() > 128 || parsed.IsNegative() {
			return data.ExactHistoricalBar{}, fmt.Errorf("alpaca/options: exact bar number out of bounds")
		}
		if field.name == "n" && !parsed.Equal(parsed.Truncate(0)) {
			return data.ExactHistoricalBar{}, fmt.Errorf("alpaca/options: fractional trade count")
		}
		*field.dest = parsed.String()
	}
	var stamp string
	if err := json.Unmarshal(fields["t"], &stamp); err != nil {
		return data.ExactHistoricalBar{}, fmt.Errorf("alpaca/options: missing exact timestamp")
	}
	bar.Timestamp, err = time.Parse(time.RFC3339Nano, stamp)
	if err != nil || bar.Timestamp.Before(time.Unix(0, 0)) {
		return data.ExactHistoricalBar{}, fmt.Errorf("alpaca/options: invalid exact timestamp")
	}
	bar.Timestamp = bar.Timestamp.UTC()
	return bar, nil
}

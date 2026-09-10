package alpaca

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/shopspring/decimal"
)

func exactSnapshotNumber(fields map[string]json.RawMessage, name string, signed, integral bool) (string, error) {
	token := fields[name]
	if len(token) == 0 || len(token) > 128 {
		return "", fmt.Errorf("alpaca/options: missing snapshot number %s", name)
	}
	decoder := json.NewDecoder(bytes.NewReader(token))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	number, ok := value.(json.Number)
	if !ok {
		return "", fmt.Errorf("alpaca/options: nonnumeric snapshot field %s", name)
	}
	parsed, err := decimal.NewFromString(number.String())
	if err != nil || parsed.Exponent() < -128 || parsed.Exponent() > 128 || (!signed && parsed.IsNegative()) || (integral && !parsed.Equal(parsed.Truncate(0))) {
		return "", fmt.Errorf("alpaca/options: snapshot number out of bounds")
	}
	return parsed.String(), nil
}

func decodeExactOptionSnapshot(raw []byte) (data.ExactOptionSnapshot, error) {
	fields, err := exactOptionFields(raw, 65536)
	if err != nil {
		return data.ExactOptionSnapshot{}, err
	}
	snapshot := data.ExactOptionSnapshot{Raw: bytes.Clone(raw)}
	quote, err := exactOptionFields(fields["latestQuote"], 65536)
	if err != nil {
		return data.ExactOptionSnapshot{}, fmt.Errorf("alpaca/options: exact snapshot quote required")
	}
	for _, field := range []struct {
		name     string
		dest     *string
		integral bool
	}{{"bp", &snapshot.BidPrice, false}, {"bs", &snapshot.BidSize, true}, {"ap", &snapshot.AskPrice, false}, {"as", &snapshot.AskSize, true}} {
		*field.dest, err = exactSnapshotNumber(quote, field.name, false, field.integral)
		if err != nil {
			return data.ExactOptionSnapshot{}, err
		}
	}
	var stamp string
	if json.Unmarshal(quote["t"], &stamp) != nil {
		return data.ExactOptionSnapshot{}, fmt.Errorf("alpaca/options: snapshot quote timestamp required")
	}
	snapshot.QuoteTimestamp, err = time.Parse(time.RFC3339Nano, stamp)
	if err != nil || snapshot.QuoteTimestamp.Before(time.Unix(0, 0)) {
		return data.ExactOptionSnapshot{}, fmt.Errorf("alpaca/options: invalid snapshot quote timestamp")
	}
	snapshot.QuoteTimestamp = snapshot.QuoteTimestamp.UTC()
	snapshot.ImpliedVolatility, err = exactSnapshotNumber(fields, "impliedVolatility", false, false)
	if err != nil {
		return data.ExactOptionSnapshot{}, err
	}
	greeks, err := exactOptionFields(fields["greeks"], 65536)
	if err != nil {
		return data.ExactOptionSnapshot{}, fmt.Errorf("alpaca/options: exact snapshot Greeks required")
	}
	for _, field := range []struct {
		name string
		dest *string
	}{{"delta", &snapshot.Delta}, {"gamma", &snapshot.Gamma}, {"theta", &snapshot.Theta}, {"vega", &snapshot.Vega}, {"rho", &snapshot.Rho}} {
		*field.dest, err = exactSnapshotNumber(greeks, field.name, true, false)
		if err != nil {
			return data.ExactOptionSnapshot{}, err
		}
	}
	if tradeRaw, ok := fields["latestTrade"]; ok && !bytes.Equal(bytes.TrimSpace(tradeRaw), []byte("null")) {
		trade, err := decodeExactOptionTrade(tradeRaw)
		if err != nil {
			return data.ExactOptionSnapshot{}, err
		}
		snapshot.LatestTrade = &trade
	}
	return snapshot, nil
}

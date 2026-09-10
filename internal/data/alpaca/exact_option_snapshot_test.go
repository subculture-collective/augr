package alpaca

import (
	"strings"
	"testing"
	"time"
)

const exactSnapshotTestRow = `{"latestQuote":{"bp":1.234567890123456789,"bs":9007199254740993,"ap":2,"as":3,"t":"2026-01-02T14:30:00.123456789Z"},"impliedVolatility":0,"greeks":{"delta":0,"gamma":0.000000000000000001,"theta":-0.123456789012345678,"vega":0,"rho":-0.1}}`

func TestDecodeExactSnapshotPreservesPresenceAndPrecision(t *testing.T) {
	raw := []byte(exactSnapshotTestRow)
	s, err := decodeExactOptionSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.BidPrice != "1.234567890123456789" || s.BidSize != "9007199254740993" || s.Theta != "-0.123456789012345678" || s.ImpliedVolatility != "0" || s.Delta != "0" || s.LatestTrade != nil || s.QuoteTimestamp.Format(time.RFC3339Nano) != "2026-01-02T14:30:00.123456789Z" {
		t.Fatalf("lost exact snapshot: %+v", s)
	}
	raw[0] = 'x'
	if string(s.Raw) != exactSnapshotTestRow {
		t.Fatal("source aliases input")
	}
}

func TestDecodeExactSnapshotOptionalTrade(t *testing.T) {
	const trade = `{"t":"2026-01-02T14:29:59.123456789Z","i":9007199254740993,"p":1.000000000000000001,"s":9007199254740993,"x":"A"}`
	for _, value := range []string{"null", trade} {
		t.Run(value, func(t *testing.T) {
			raw := strings.TrimSuffix(exactSnapshotTestRow, "}") + `,"latestTrade":` + value + "}"
			snapshot, err := decodeExactOptionSnapshot([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if value == "null" {
				if snapshot.LatestTrade != nil {
					t.Fatal("null trade became an observed trade")
				}
				return
			}
			actual := snapshot.LatestTrade
			if actual == nil || actual.ProviderID != "9007199254740993" || actual.Price != "1.000000000000000001" || actual.Size != "9007199254740993" || actual.Exchange != "A" || string(actual.Raw) != trade || actual.Timestamp.Format(time.RFC3339Nano) != "2026-01-02T14:29:59.123456789Z" {
				t.Fatalf("nested trade lost exact source: %+v", actual)
			}
		})
	}
}

func TestDecodeExactSnapshotRejectsMissingAndMalformedFields(t *testing.T) {
	for name, raw := range map[string]string{
		"missing IV":       strings.Replace(exactSnapshotTestRow, `"impliedVolatility":0,`, "", 1),
		"missing delta":    strings.Replace(exactSnapshotTestRow, `"delta":0,`, "", 1),
		"null IV":          strings.Replace(exactSnapshotTestRow, `"impliedVolatility":0`, `"impliedVolatility":null`, 1),
		"quoted IV":        strings.Replace(exactSnapshotTestRow, `"impliedVolatility":0`, `"impliedVolatility":"0"`, 1),
		"negative IV":      strings.Replace(exactSnapshotTestRow, `"impliedVolatility":0`, `"impliedVolatility":-1`, 1),
		"fractional size":  strings.Replace(exactSnapshotTestRow, `"as":3`, `"as":3.1`, 1),
		"duplicate nested": strings.Replace(exactSnapshotTestRow, `"as":3`, `"as":3,"as":4`, 1),
		"invalid time":     strings.Replace(exactSnapshotTestRow, `2026-01-02T14:30:00.123456789Z`, `bad`, 1),
		"empty quote":      strings.Replace(exactSnapshotTestRow, `"latestQuote"`, `"unrelated"`, 1),
		"missing Greeks":   strings.Replace(exactSnapshotTestRow, `"greeks"`, `"unrelated"`, 1),
		"huge exponent":    strings.Replace(exactSnapshotTestRow, `"delta":0`, `"delta":1e129`, 1),
		"malformed trade":  strings.TrimSuffix(exactSnapshotTestRow, "}") + `,"latestTrade":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeExactOptionSnapshot([]byte(raw)); err == nil {
				t.Fatal("accepted missing or malformed source")
			}
		})
	}
}

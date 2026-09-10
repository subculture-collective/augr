package alpaca

import (
	"bytes"
	"strings"
	"testing"
)

func TestExactOptionBarDecoder(t *testing.T) {
	raw := []byte(`{"t":"2026-01-02T14:30:00.123456789Z","o":10,"h":12,"l":9,"c":11.000000000000000001,"v":100,"n":5,"vw":10.500000000000000001}`)
	bar, err := decodeExactOptionBar(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bar.Close != "11.000000000000000001" || bar.VWAP != "10.500000000000000001" || bar.TradeCount != "5" || bar.Timestamp.Nanosecond() != 123456789 || !bytes.Equal(bar.Raw, raw) {
		t.Fatalf("source precision lost: %+v", bar)
	}
	raw[0] = ' '
	if bar.Raw[0] != '{' {
		t.Fatal("source row aliases caller storage")
	}
	valid := string(bar.Raw)
	for name, invalid := range map[string]string{
		"duplicate":        strings.Replace(valid, `"o":10`, `"o":10,"o":11`, 1),
		"missing count":    strings.Replace(valid, `,"n":5`, "", 1),
		"missing vwap":     strings.Replace(valid, `,"vw":10.500000000000000001`, "", 1),
		"quoted":           strings.Replace(valid, `"o":10`, `"o":"10"`, 1),
		"null":             strings.Replace(valid, `"o":10`, `"o":null`, 1),
		"fractional count": strings.Replace(valid, `"n":5`, `"n":1.5`, 1),
		"negative":         strings.Replace(valid, `"o":10`, `"o":-1`, 1),
		"exponent":         strings.Replace(valid, `"o":10`, `"o":1e999`, 1),
		"timestamp":        strings.Replace(valid, "2026-01-02T14:30:00.123456789Z", "invalid", 1),
		"trailing":         valid + "{}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeExactOptionBar([]byte(invalid)); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

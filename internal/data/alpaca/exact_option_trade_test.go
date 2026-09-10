package alpaca

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeExactOptionTradePreservesSource(t *testing.T) {
	raw := []byte(`{"i":9007199254740993,"p":1.234567890123456789,"s":9007199254740993,"x":"A","t":"2026-09-01T14:00:00.123456789Z"}`)
	trade, err := decodeExactOptionTrade(raw)
	if err != nil {
		t.Fatal(err)
	}
	if trade.ProviderID != "9007199254740993" || trade.Price != "1.234567890123456789" || trade.Size != "9007199254740993" || trade.Exchange != "A" || trade.Timestamp.Format(time.RFC3339Nano) != "2026-09-01T14:00:00.123456789Z" {
		t.Fatalf("lost exact values: %+v", trade)
	}
	before := string(raw)
	raw[0] = 'x'
	if string(trade.Raw) != before {
		t.Fatal("retained source aliases caller bytes")
	}
}

func TestDecodeExactOptionTradeRejectsInvalidSource(t *testing.T) {
	base := `{"i":1,"p":1.25,"s":2,"x":"A","t":"2026-09-01T14:00:00Z"}`
	for name, raw := range map[string]string{
		"duplicate":        strings.Replace(base, `"p":1.25`, `"p":1.25,"p":2`, 1),
		"string price":     strings.Replace(base, `1.25`, `"1.25"`, 1),
		"zero price":       strings.Replace(base, `1.25`, `0`, 1),
		"negative price":   strings.Replace(base, `1.25`, `-1`, 1),
		"huge exponent":    strings.Replace(base, `1.25`, `1e129`, 1),
		"fractional size":  strings.Replace(base, `"s":2`, `"s":2.5`, 1),
		"null size":        strings.Replace(base, `"s":2`, `"s":null`, 1),
		"fractional id":    strings.Replace(base, `"i":1`, `"i":1.5`, 1),
		"overflow id":      strings.Replace(base, `"i":1`, `"i":9223372036854775808`, 1),
		"missing id":       strings.Replace(base, `"i":1,`, ``, 1),
		"empty exchange":   strings.Replace(base, `"A"`, `""`, 1),
		"control exchange": strings.Replace(base, `"A"`, `"A\n"`, 1),
		"invalid time":     strings.Replace(base, `2026-09-01T14:00:00Z`, `bad`, 1),
		"trailing":         base + `{}`,
		"oversize":         strings.Repeat(" ", 65537),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeExactOptionTrade([]byte(raw)); err == nil {
				t.Fatal("accepted invalid source")
			}
		})
	}
}

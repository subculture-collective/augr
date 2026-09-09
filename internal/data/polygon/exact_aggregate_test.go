package polygon

import (
	"bytes"
	"testing"
)

func TestDecodeExactAggregate(t *testing.T) {
	t.Parallel()
	base := []byte(`{"o":100,"h":101,"l":99,"c":100.123456789012345678,"v":9007199254740993,"t":1704067200000}`)
	got, err := decodeExactAggregate(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Close != "100.123456789012345678" || got.Volume != "9007199254740993" || got.TradeCount != "" || got.VWAP != "" || !bytes.Equal(got.Raw, base) {
		t.Fatalf("exact values or presence lost: %+v", got)
	}
	base[0] = ' '
	if got.Raw[0] != '{' {
		t.Fatal("raw source aliases caller buffer")
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"missing", `"c":100.123456789012345678,`, ""},
		{"null", `"c":100.123456789012345678`, `"c":null`},
		{"quoted", `"c":100.123456789012345678`, `"c":"100"`},
		{"negative", `"v":9007199254740993`, `"v":-1`},
		{"huge_exponent", `"v":9007199254740993`, `"v":1e100000`},
		{"timestamp", `"t":1704067200000`, `"t":null`},
		{"duplicate", `"c":100.123456789012345678`, `"c":100,"c":101`},
		{"escaped_duplicate", `"c":100.123456789012345678`, `"c":100,"\u0063":101`},
		{"fractional_count", `"v":9007199254740993`, `"v":1,"n":1.5`},
		{"trailing", `}`, `} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := bytes.Replace(got.Raw, []byte(tc.old), []byte(tc.replacement), 1)
			if _, err := decodeExactAggregate(raw); err == nil {
				t.Fatal("invalid exact aggregate accepted")
			}
		})
	}
	for _, tc := range []struct{ name, fields, count, vwap string }{
		{"explicit_zero", `,"n":0,"vw":0`, "0", "0"},
		{"null_unknown", `,"n":null,"vw":null`, "", ""},
		{"scientific", `,"n":1e2,"vw":1.001e2`, "100", "100.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := bytes.Replace(got.Raw, []byte("}"), []byte(tc.fields+"}"), 1)
			result, err := decodeExactAggregate(raw)
			if err != nil || result.TradeCount != tc.count || result.VWAP != tc.vwap {
				t.Fatalf("optional field semantics: result=%+v err=%v", result, err)
			}
		})
	}
}

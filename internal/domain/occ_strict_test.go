package domain

import "testing"

func TestParseStrictOCC(t *testing.T) {
	for _, symbol := range []string{"AAPL260116C00150000", "SPY280229P00000000", "ABCDEF991231C99999999", "A1260116C00150000"} {
		t.Run(symbol, func(t *testing.T) {
			value, err := ParseStrictOCC(symbol)
			if err != nil || value.OCCSymbol != symbol {
				t.Fatalf("valid canonical identity rejected: %v", err)
			}
		})
	}
	for _, symbol := range []string{"", "O:AAPL260116C00150000", "aapl260116C00150000", " AAPL260116C00150000", "AAPL260230C00150000", "AAPL261301C00150000", "AAPL260000C00150000", "SPY270229C00150000", "AAPL260116C+0150000", "AAPL260116C-0150000", "AAPL+60116C00150000", "AAPL260116X00150000", "ABCDEFG260116C00150000"} {
		t.Run(symbol, func(t *testing.T) {
			if _, err := ParseStrictOCC(symbol); err == nil {
				t.Fatal("accepted noncanonical identity")
			}
		})
	}
}

package automation

import (
	"encoding/json"
	"testing"
)

func TestDecodeResolutionArrays(t *testing.T) {
	outcomes, err := decodeStringArrayMaybeJSON(json.RawMessage(`"[\"Up\",\"Down\"]"`))
	if err != nil {
		t.Fatal(err)
	}
	prices, err := decodeScalarArrayMaybeJSON(json.RawMessage(`[1,0]`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := winnerFromGamma(outcomes, prices); !ok || got != "Up" {
		t.Fatalf("winner = %q,%v", got, ok)
	}

	prices, err = decodeScalarArrayMaybeJSON(json.RawMessage(`"[\"0\",\"1.0\"]"`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := winnerFromGamma([]string{"Yes", "No"}, prices); !ok || got != "No" {
		t.Fatalf("winner = %q,%v", got, ok)
	}
}

func TestWinnerFromGamma(t *testing.T) {
	tests := []struct {
		name             string
		outcomes, prices []string
		want             string
		ok               bool
	}{
		{"up string prices", []string{"Up", "Down"}, []string{"1", "0"}, "Up", true},
		{"yes no array", []string{"Yes", "No"}, []string{"0", "1"}, "No", true},
		{"unresolved", []string{"Yes", "No"}, []string{"0", "0"}, "", false},
		{"over under", []string{"Over", "Under"}, []string{"0", "1.0"}, "Under", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := winnerFromGamma(tc.outcomes, tc.prices)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got %q,%v want %q,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestNormalizePolymarketSide(t *testing.T) {
	for _, side := range []string{"YES", "NO", "Up", "Down", "Over", "Under"} {
		if got, ok := normalizePolymarketSide(side); !ok || got == "" {
			t.Fatalf("failed %s", side)
		}
	}
	if got, ok := normalizePolymarketSide("Foo"); ok || got != "" {
		t.Fatalf("expected unsupported side rejected")
	}
}

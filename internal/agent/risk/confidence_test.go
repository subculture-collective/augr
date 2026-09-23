package risk

import (
	"strings"
	"testing"
)

func TestParseFinalSignalNormalizesConfidence(t *testing.T) {
	t.Parallel()
	base := `{"action":"HOLD","confidence":%s,"adjusted_position_size":0,"adjusted_stop_loss":0,"reasoning":"r","extra_commentary":"ignored"}`
	for raw, want := range map[string]float64{"7": 7, "7.4": 7, "0.85": 9, "1": 10, "12": 10, "0.05": 1} {
		signal, err := ParseFinalSignal(strings.Replace(base, "%s", raw, 1))
		if err != nil {
			t.Fatalf("confidence %s: %v", raw, err)
		}
		if signal.Confidence != want {
			t.Fatalf("confidence %s normalized to %v, want %v", raw, signal.Confidence, want)
		}
	}
	for _, raw := range []string{"0", "-3"} {
		if _, err := ParseFinalSignal(strings.Replace(base, "%s", raw, 1)); err == nil {
			t.Fatalf("confidence %s accepted", raw)
		}
	}
}

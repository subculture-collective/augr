package backtest

import (
	"encoding/json"
	"testing"
)

func TestClosedTradeMetricsJSONCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  int
	}{
		{"legacy bars and fills are not closures", `{"total_bars":1000,"order_fills":40}`, 0},
		{"closed count survives", `{"closed_trades":10,"order_fills":40}`, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m Metrics
			if err := json.Unmarshal([]byte(tc.input), &m); err != nil {
				t.Fatal(err)
			}
			if m.ClosedTrades != tc.want {
				t.Fatalf("closed = %d, want %d", m.ClosedTrades, tc.want)
			}
			encoded, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Metrics
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.ClosedTrades != tc.want {
				t.Fatalf("round trip lost closed count: %s", encoded)
			}
		})
	}
}

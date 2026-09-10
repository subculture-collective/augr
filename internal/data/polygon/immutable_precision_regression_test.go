package polygon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

// Synthetic provider responses only: no network provider or licensed dataset.
// These regressions expose why the legacy float interface cannot qualify an
// exact immutable import. The repair should introduce a separate exact path,
// preserving the legacy live-data interface for its existing consumers.
func TestImmutableImportSourcePreservesDecimalEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, wantClose string }{
		{"decimal_precision", `{"results":[{"o":100,"h":101,"l":99,"c":100.123456789012345678,"v":1,"t":1704067200000}]}`, "100.123456789012345678"},
		{"missing_close", `{"results":[{"o":100,"h":101,"l":99,"v":1,"t":1704067200000}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("write synthetic response: %v", err)
				}
			}))
			defer server.Close()
			client := NewClient("synthetic-key", discardLogger())
			client.baseURL = server.URL
			start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			result, err := NewProvider(client).GetExactOHLCVWithReceipt(context.Background(), "TEST", data.Timeframe1d, start, start.Add(24*time.Hour), "sip", "raw")
			bars, receipt := result.Bars, result.Receipt
			if tc.wantClose == "" {
				if err == nil {
					t.Fatalf("missing source close accepted: bars=%+v receipt=%+v", bars, receipt)
				}
				return
			}
			if err != nil || len(bars) != 1 {
				t.Fatalf("fetch synthetic bar: count=%d err=%v", len(bars), err)
			}
			if got := bars[0].Close; got != tc.wantClose {
				t.Fatalf("immutable import would alter source decimal: got=%s want=%s", got, tc.wantClose)
			}
		})
	}
}

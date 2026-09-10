package alpaca

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

func TestExactOptionsProviderPages(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const row = `{"t":"2026-01-02T14:30:00Z","o":10,"h":12,"l":9,"c":11.000000000000000001,"v":100,"n":5,"vw":10.5}`
	for _, mode := range []string{"success", "repeat", "missing terminal", "wrong symbol", "credential", "forbidden"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1beta1/options/bars" || r.URL.Query().Get("feed") != "opra" || r.URL.Query().Get("symbols") != symbol {
					t.Errorf("request identity changed: %s", r.URL)
				}
				if mode == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				key := symbol
				if mode == "wrong symbol" {
					key = "MSFT260116C00150000"
				}
				token := `"cursor"`
				if calls == 2 && mode != "repeat" {
					token = "null"
				}
				body := fmt.Sprintf(`{"bars":{"%s":[%s]},"next_page_token":%s}`, key, row, token)
				if mode == "missing terminal" {
					body = fmt.Sprintf(`{"bars":{"%s":[%s]}}`, key, row)
				}
				if mode == "credential" {
					body = strings.Replace(body, "cursor", "synthetic-secret", 1)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			provider := NewOptionsDataProvider("synthetic-key", "synthetic-secret", nil)
			provider.SetBaseURL(server.URL)
			at := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
			result, err := provider.GetExactOptionsOHLCVWithReceipt(t.Context(), symbol, data.Timeframe1d, at, at, "opra", "raw")
			if mode != "success" {
				if err == nil || len(result.Bars) != 0 || len(result.Pages) != 0 || result.Receipt.PaginationComplete || result.Receipt.Entitled {
					t.Fatalf("invalid page did not fail closed: %+v err=%v", result, err)
				}
				return
			}
			if err != nil || calls != 2 || len(result.Pages) != 2 || len(result.Bars) != 2 || !result.Receipt.PaginationComplete || !result.Receipt.Entitled {
				t.Fatalf("pagination failed: %+v err=%v calls=%d", result, err, calls)
			}
			if result.Bars[1].PageIndex != 1 || result.Bars[1].Close != "11.000000000000000001" || !bytes.Equal(result.Bars[1].Raw, []byte(row)) || !strings.Contains(result.Pages[1].Query, "page_token=cursor") {
				t.Fatal("source evidence or exact values lost")
			}
		})
	}
}

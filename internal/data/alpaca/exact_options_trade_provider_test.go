package alpaca

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExactOptionsTradeProviderPages(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	const row = `{"t":"2026-01-02T14:30:00Z","i":1,"p":11.000000000000000001,"s":100,"x":"A"}`
	for _, mode := range []string{"success", "repeat", "missing terminal", "wrong symbol", "credential", "forbidden", "duplicate trade"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1beta1/options/trades" || r.URL.Query().Get("feed") != "opra" || r.URL.Query().Get("symbols") != symbol {
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
				actualRow := row
				if calls == 2 && mode != "duplicate trade" {
					actualRow = strings.Replace(row, `"i":1`, `"i":2`, 1)
				}
				body := fmt.Sprintf(`{"trades":{"%s":[%s]},"next_page_token":%s}`, key, actualRow, token)
				if mode == "missing terminal" {
					body = fmt.Sprintf(`{"trades":{"%s":[%s]}}`, key, row)
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
			result, err := provider.GetExactOptionsTradesWithReceipt(t.Context(), symbol, at, at, "opra")
			if mode != "success" {
				if err == nil || len(result.Trades) != 0 || len(result.Pages) != 0 || result.Receipt.PaginationComplete || result.Receipt.Entitled {
					t.Fatalf("invalid page did not fail closed: %+v err=%v", result, err)
				}
				return
			}
			if err != nil || calls != 2 || len(result.Pages) != 2 || len(result.Trades) != 2 || !result.Receipt.PaginationComplete || !result.Receipt.Entitled {
				t.Fatalf("pagination failed: %+v err=%v calls=%d", result, err, calls)
			}
			if result.Trades[1].PageIndex != 1 || result.Trades[1].Price != "11.000000000000000001" || !bytes.Equal(result.Trades[1].Raw, []byte(strings.Replace(row, `"i":1`, `"i":2`, 1))) || !strings.Contains(result.Pages[1].Query, "page_token=cursor") {
				t.Fatal("source evidence or exact values lost")
			}
		})
	}
}

package polygon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

func TestExactProviderPageEvidence(t *testing.T) {
	t.Parallel()
	const row = `{"o":1,"h":2,"l":1,"c":1.123456789012345678,"v":3,"n":7,"vw":1.2,"t":1704067200000}`
	for _, tc := range []struct {
		name, body string
		fail       bool
	}{
		{"valid", `{"status":"OK","results":[` + row + `]}`, false},
		{"provider_error", `{"status":"ERROR","results":[` + row + `]}`, true},
		{"wrong_ticker", `{"ticker":"OTHER","results":[` + row + `]}`, true},
		{"wrong_adjustment", `{"adjusted":true,"results":[` + row + `]}`, true},
		{"duplicate_results", `{"results":[],"results":[` + row + `]}`, true},
		{"credential", `{"results":[],"message":"synthetic-private-key"}`, true},
		{"missing_results", `{"status":"OK"}`, true},
		{"null_results", `{"results":null}`, true},
		{"outside_interval", `{"results":[` + strings.ReplaceAll(row, "1704067200000", "1703980800000") + `]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, err := fmt.Fprint(w, tc.body); err != nil {
					t.Errorf("write fixture: %v", err)
				}
			}))
			defer server.Close()
			client := NewClient("synthetic-private-key", discardLogger())
			client.baseURL = server.URL
			start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			result, err := NewProvider(client).GetExactOHLCVWithReceipt(context.Background(), "TEST", data.Timeframe1d, start, start.Add(24*time.Hour), "sip", "raw")
			if tc.fail {
				if err == nil {
					t.Fatal("invalid source accepted")
				}
				if len(result.Bars) != 0 || len(result.Pages) != 0 {
					t.Fatal("failed request leaked partial evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Pages) != 1 || string(result.Pages[0].Body) != tc.body || len(result.Bars) != 1 || result.Bars[0].TradeCount != "7" || result.Bars[0].VWAP != "1.2" {
				t.Fatalf("source evidence lost: %+v", result)
			}
			if strings.Contains(result.Pages[0].Query, "synthetic-private-key") || strings.Contains(result.Pages[0].Query, "apiKey") {
				t.Fatal("credential in request evidence")
			}
		})
	}
}

func TestExactProviderPagination(t *testing.T) {
	t.Parallel()
	for _, loop := range []bool{false, true} {
		t.Run(fmt.Sprintf("loop_%t", loop), func(t *testing.T) {
			t.Parallel()
			var serverURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := `{"results":[{"o":1,"h":2,"l":1,"c":1.000000000000000001,"v":3,"t":1704067200000}]`
				if r.URL.Query().Get("cursor") == "" || loop {
					body += fmt.Sprintf(`,"next_url":%q`, serverURL+"/v2/aggs/ticker/TEST/range/1/day/1704067200000/1704153600000?cursor=next")
				}
				if _, err := fmt.Fprint(w, body+"}"); err != nil {
					t.Errorf("write fixture: %v", err)
				}
			}))
			serverURL = server.URL
			defer server.Close()
			client := NewClient("synthetic-private-key", discardLogger())
			client.baseURL = serverURL
			start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			result, err := NewProvider(client).GetExactOHLCVWithReceipt(context.Background(), "TEST", data.Timeframe1d, start, start.Add(24*time.Hour), "sip", "raw")
			if loop {
				if err == nil || len(result.Bars) != 0 || len(result.Pages) != 0 {
					t.Fatal("loop returned success or partial source")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Pages) != 2 || len(result.Bars) != 2 || result.Receipt.Pages != 2 || !result.Receipt.PaginationComplete || !result.Receipt.Entitled {
				t.Fatalf("incomplete pagination evidence: %+v", result)
			}
			if !strings.Contains(result.Pages[1].Query, "cursor=next") || result.Bars[1].Close != "1.000000000000000001" {
				t.Fatal("page request or exact value lost")
			}
		})
	}
}

package alpaca

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExactOptionsSnapshotProviderPages(t *testing.T) {
	for _, mode := range []string{"success", "duplicate", "wrong underlying", "missing terminal", "credential", "forbidden", "missing field", "array object", "invalid token", "empty"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1beta1/options/snapshots/AAPL" || r.URL.Query().Get("feed") != "opra" || r.URL.Query().Get("limit") != "100" {
					t.Errorf("request identity changed: %s", r.URL)
				}
				if calls == 2 && r.URL.Query().Get("page_token") != "cursor" {
					t.Error("pagination cursor missing")
				}
				if mode == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				symbol, token := "AAPL260116C00150000", `"cursor"`
				if calls == 2 {
					token = "null"
					if mode != "duplicate" {
						symbol = "AAPL260116C00160000"
					}
				}
				if mode == "wrong underlying" {
					symbol = "MSFT260116C00150000"
				}
				row := exactSnapshotTestRow
				if mode == "missing field" {
					row = strings.Replace(row, `"impliedVolatility":0,`, "", 1)
				}
				if mode == "array object" {
					row = "[" + row + "]"
				}
				if mode == "credential" {
					token = `"synthetic-secret"`
				}
				if mode == "invalid token" {
					token = "7"
				}
				body := fmt.Sprintf(`{"snapshots":{"%s":%s},"next_page_token":%s}`, symbol, row, token)
				if mode == "missing terminal" {
					body = fmt.Sprintf(`{"snapshots":{"%s":%s}}`, symbol, row)
				}
				if mode == "empty" {
					body = `{"snapshots":{},"next_page_token":null}`
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			provider := NewOptionsDataProvider("synthetic-key", "synthetic-secret", nil)
			provider.SetBaseURL(server.URL)
			result, err := provider.GetExactOptionsSnapshotsWithReceipt(t.Context(), "AAPL", "opra")
			if mode == "empty" {
				if err != nil || len(result.Snapshots) != 0 || len(result.Pages) != 1 || !result.Receipt.PaginationComplete {
					t.Fatalf("explicit empty map not preserved: %+v err=%v", result, err)
				}
				return
			}
			if mode != "success" {
				if err == nil || len(result.Snapshots) != 0 || len(result.Pages) != 0 || result.Receipt.PaginationComplete || result.Receipt.Entitled {
					t.Fatalf("invalid snapshot did not fail closed: %+v err=%v", result, err)
				}
				return
			}
			if err != nil || calls != 2 || len(result.Pages) != 2 || len(result.Snapshots) != 2 || !result.Receipt.PaginationComplete || !result.Receipt.Entitled {
				t.Fatalf("pagination failed: %+v err=%v", result, err)
			}
			if result.Snapshots[1].PageIndex != 1 || result.Snapshots[1].Symbol != "AAPL260116C00160000" || result.Snapshots[1].BidSize != "9007199254740993" || !bytes.Equal(result.Snapshots[1].Raw, []byte(exactSnapshotTestRow)) {
				t.Fatal("exact snapshot values or source evidence lost")
			}
		})
	}
}

func TestExactOptionsSnapshotProviderRejectsInvalidRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("invalid request reached the network")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, input := range []struct{ underlying, feed string }{
		{"", "opra"},
		{"aapl", "opra"},
		{" AAPL", "opra"},
		{"AAPL/", "opra"},
		{"AAPL?", "opra"},
		{"ABCDEFG", "opra"},
		{"AAPL", "sip"},
	} {
		t.Run(input.underlying+"/"+input.feed, func(t *testing.T) {
			provider := NewOptionsDataProvider("synthetic-key", "synthetic-secret", nil)
			provider.SetBaseURL(server.URL)
			result, err := provider.GetExactOptionsSnapshotsWithReceipt(t.Context(), input.underlying, input.feed)
			if err == nil || result.Receipt.Pages != 0 || result.Receipt.Entitled {
				t.Fatalf("invalid request accepted: %+v err=%v", result, err)
			}
		})
	}
}

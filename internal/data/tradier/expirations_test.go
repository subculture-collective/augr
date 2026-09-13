package tradier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListedExpirationsDateShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		date string
		want int
	}{
		{"singleton", `"2026-10-16"`, 1},
		{"array", `["2026-10-16","2026-11-20"]`, 2},
		{"null", `null`, 0},
		{"empty", `[]`, 0},
		{"number", `1`, 0},
		{"boolean", `true`, 0},
		{"object", `{}`, 0},
		{"mixed", `["2026-10-16",1]`, 0},
		{"null element", `["2026-10-16",null]`, 0},
		{"invalid scalar date", `"invalid"`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/markets/options/expirations" || r.URL.Query().Get("symbol") != "ADV" {
					t.Errorf("unexpected request: %s", r.URL)
				}
				_, _ = w.Write([]byte(`{"expirations":{"date":` + tc.date + `}}`))
			}))
			defer server.Close()
			provider := NewOptionsProvider("test", true, nil)
			provider.baseURL = server.URL
			got, err := provider.listedExpirations(context.Background(), "ADV")
			if tc.want == 0 {
				if err == nil || len(got) != 0 {
					t.Fatalf("invalid/empty response yielded %v, %v", got, err)
				}
				return
			}
			if err != nil || len(got) != tc.want || got[0].Format("2006-01-02") != "2026-10-16" {
				t.Fatalf("expirations = %v, %v; want %d dates", got, err, tc.want)
			}
		})
	}
}

func TestOptionsChainSingletonExpirationResolution(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		date    string
		zero    bool
		invalid bool
	}{
		{"requested date fallback", `"2026-10-16"`, false, false},
		{"nearest date", `"2026-10-16"`, true, false},
		{"malformed fallback", `["2026-10-16",null]`, false, true},
		{"malformed nearest", `["2026-10-16",1]`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path+":"+r.URL.Query().Get("expiration"))
				switch r.URL.Path {
				case "/v1/markets/options/expirations":
					_, _ = w.Write([]byte(`{"expirations":{"date":` + tc.date + `}}`))
				case "/v1/markets/options/chains":
					if r.URL.Query().Get("expiration") != "2026-10-16" {
						_, _ = w.Write([]byte(`{"options":null}`))
						return
					}
					_, _ = w.Write([]byte(`{"options":{"option":[{"symbol":"ADV261016C00005000","strike":5,"bid":1,"ask":2,"contract_size":100,"option_type":"call","expiration_date":"2026-10-16"}]}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			provider := NewOptionsProvider("test", true, nil)
			provider.baseURL = server.URL
			expiry := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
			if tc.zero {
				expiry = time.Time{}
			}
			chain, err := provider.GetOptionsChain(context.Background(), "ADV", expiry, "")
			wantRequests := 3
			if tc.zero {
				wantRequests--
			}
			if tc.invalid {
				wantRequests--
				if err == nil || len(chain) != 0 {
					t.Fatalf("malformed response produced chain: %v, %v", chain, err)
				}
			} else if err != nil || len(chain) != 1 || chain[0].Contract.Expiry.Format("2006-01-02") != "2026-10-16" {
				t.Fatalf("singleton resolution = %v, %v", chain, err)
			}
			if len(requests) != wantRequests {
				t.Fatalf("requests = %v, want %d", requests, wantRequests)
			}
		})
	}
}

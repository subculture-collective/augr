package alpaca

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMarketClockAt(t *testing.T) {
	at := time.Date(2026, 1, 2, 14, 29, 59, 123456789, time.UTC)
	valid := `{"clocks":[{"market":{"acronym":"IEX","mic":"IEXG"},"timestamp":"2026-01-02T14:29:59.123456789Z","phase_until":"2026-01-02T14:30:00Z","phase":"pre","is_market_day":true}]}`
	for _, name := range []string{"valid", "wrong_market", "wrong_mic", "later_clock", "expired_phase", "missing_day", "contradictory_day", "unknown_phase", "ambiguous", "unsupported_market", "forbidden", "redirect"} {
		t.Run(name, func(t *testing.T) {
			body := valid
			switch name {
			case "wrong_market":
				body = strings.Replace(body, `"IEX"`, `"NYSE"`, 1)
			case "wrong_mic":
				body = strings.Replace(body, "IEXG", "XNYS", 1)
			case "later_clock":
				body = strings.Replace(body, "14:29:59.123456789", "14:30:00", 1)
			case "expired_phase":
				body = strings.Replace(body, "14:30:00", "14:29:00", 1)
			case "missing_day":
				body = strings.Replace(body, `,"is_market_day":true`, "", 1)
			case "contradictory_day":
				body = strings.Replace(body, "true", "false", 1)
			case "unknown_phase":
				body = strings.Replace(body, `"pre"`, `"unknown"`, 1)
			case "ambiguous":
				body = strings.Replace(body, `}]}`, `},{}]}`, 1)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v3/clock" || r.URL.Query().Get("markets") != "IEX" || r.URL.Query().Get("time") != at.Format(time.RFC3339Nano) || r.Header.Get("APCA-API-KEY-ID") != "test-key" || r.Header.Get("APCA-API-SECRET-KEY") != "test-secret" {
					t.Error("clock request contract mismatch")
				}
				if name == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					body = "test-secret"
				}
				if name == "redirect" {
					w.Header().Set("Location", "/credential-trap")
					w.WriteHeader(http.StatusFound)
				}
				if _, err := fmt.Fprint(w, body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			provider := NewMarketClockProvider("test-key", "test-secret")
			provider.baseURL = server.URL
			market := "IEX"
			if name == "unsupported_market" {
				market = "NYSE"
			}
			evidence, err := provider.ClockAt(t.Context(), market, at)
			if name != "valid" {
				if err == nil || strings.Contains(err.Error(), "test-secret") {
					t.Fatal("accepted invalid evidence or leaked response")
				}
			} else {
				digest := sha256.Sum256([]byte(valid))
				if err != nil || evidence.Phase != "pre" || !evidence.At.Equal(at) || evidence.ResponseSHA256 != hex.EncodeToString(digest[:]) || string(evidence.RawResponse) != valid || evidence.ObservedAt.Before(at) {
					t.Fatal("lost clock phase, timing, or source provenance", err)
				}
			}
			wantRequests := 1
			if name == "unsupported_market" {
				wantRequests = 0
			}
			if requests != wantRequests {
				t.Fatalf("requests=%d want=%d", requests, wantRequests)
			}
		})
	}
}

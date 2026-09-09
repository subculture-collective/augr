package polygon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTickerReferenceEvidence(t *testing.T) {
	valid := `{"status":"OK","results":{"ticker":"SPY","market":"stocks","composite_figi":"BBG000BDTBL9","share_class_figi":"BBG001S72SM3","primary_exchange":"ARCX","currency_name":"usd","type":"ETF","active":true,"round_lot":40}}`
	for _, test := range []struct {
		name, body string
		wantError  bool
	}{
		{"valid", valid, false},
		{"wrong ticker", strings.Replace(valid, `"SPY"`, `"QQQ"`, 1), true},
		{"missing identity", strings.Replace(valid, `"BBG001S72SM3"`, `""`, 1), true},
		{"provider failure", strings.Replace(valid, `"OK"`, `"ERROR"`, 1), true},
		{"malformed", `{`, true},
		{"wrong market", strings.Replace(valid, `"stocks"`, `"crypto"`, 1), true},
		{"missing exchange", strings.Replace(valid, `"ARCX"`, `""`, 1), true},
		{"inactive retained", strings.Replace(valid, `"active":true`, `"active":false`, 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v3/reference/tickers/SPY" || r.URL.Query().Get("date") != "2026-09-08" {
					t.Errorf("unexpected reference request")
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := NewClient("private-test-key", discardLogger())
			client.baseURL = server.URL
			before := time.Now().UTC().Truncate(time.Microsecond)
			got, err := client.GetTickerReference(context.Background(), "SPY", "2026-09-08")
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil {
				return
			}
			if string(got.RawResponse) != test.body || got.ResponseSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(test.body))) {
				t.Fatal("raw evidence not preserved")
			}
			if got.ObservedAt.Before(before) || got.ObservedAt.After(time.Now()) {
				t.Fatal("availability backdated")
			}
			if strings.Contains(got.RequestPath, "private-test-key") || got.RequestPath != "/v3/reference/tickers/SPY?date=2026-09-08" {
				t.Fatal("unsafe request identity")
			}
			if test.name == "inactive retained" && got.Active {
				t.Fatal("inactive provider reference was promoted")
			}
		})
	}
}

func TestTickerReferenceCancelledContext(t *testing.T) {
	client := NewClient("key", discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetTickerReference(ctx, "SPY", "2026-09-08"); err == nil {
		t.Fatal("cancelled reference request succeeded")
	}
}

func TestTickerReferenceRejectsInvalidRequest(t *testing.T) {
	client := NewClient("key", discardLogger())
	for _, ticker := range []string{"", " SPY", "SPY?apiKey=x", "../SPY"} {
		if _, err := client.GetTickerReference(context.Background(), ticker, "2026-09-08"); err == nil {
			t.Fatalf("accepted %q", ticker)
		}
	}
	if _, err := client.GetTickerReference(context.Background(), "SPY", "invalid"); err == nil {
		t.Fatal("accepted invalid date")
	}
}

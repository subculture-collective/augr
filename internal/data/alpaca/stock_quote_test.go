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

	"github.com/shopspring/decimal"
)

func TestStockQuoteShareSizeUnits(t *testing.T) {
	for _, name := range []string{"current", "historical", "cutover", "fractional", "overflow"} {
		t.Run(name, func(t *testing.T) {
			evidence := &StockQuoteEvidence{ExchangeAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), BidSize: decimal.NewFromInt(80), AskSize: decimal.NewFromInt(80)}
			switch name {
			case "historical":
				evidence.ExchangeAt = time.Date(2025, 11, 2, 12, 0, 0, 0, time.UTC)
			case "cutover":
				evidence.ExchangeAt = time.Date(2025, 11, 3, 12, 0, 0, 0, time.UTC)
			case "fractional":
				evidence.BidSize = decimal.RequireFromString("1.5")
			case "overflow":
				evidence.BidSize = decimal.NewFromInt(4294967296)
			}
			bid, ask, err := evidence.ShareSizes()
			if name != "current" {
				if err == nil {
					t.Fatal("accepted unqualified share quantity")
				}
				return
			}
			if err != nil || !bid.Equal(decimal.NewFromInt(80)) || !ask.Equal(decimal.NewFromInt(80)) {
				t.Fatal("current quote sizes were incorrectly multiplied by round lot")
			}
		})
	}
}

func TestStockQuoteEvidence(t *testing.T) {
	valid := `{"symbol":"SPY","quote":{"bp":600.010001,"ap":600.020002,"bs":3,"as":4,"bx":"V","ax":"V","t":"2026-01-01T15:00:00.123456789Z"}}`
	for _, name := range []string{"valid", "wrong_symbol", "missing_size", "crossed", "future", "forbidden", "missing_feed", "redirect"} {
		t.Run(name, func(t *testing.T) {
			body := valid
			switch name {
			case "wrong_symbol":
				body = strings.Replace(body, "SPY", "QQQ", 1)
			case "missing_size":
				body = strings.Replace(body, `"bs":3`, `"bs":0`, 1)
			case "crossed":
				body = strings.Replace(body, "600.010001", "700", 1)
			case "future":
				body = strings.Replace(body, "2026-01-01", "2099-01-01", 1)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v2/stocks/SPY/quotes/latest" || r.URL.Query().Get("feed") != "iex" || r.Header.Get("APCA-API-KEY-ID") != "test-key" || r.Header.Get("APCA-API-SECRET-KEY") != "test-secret" {
					t.Error("request contract mismatch")
				}
				if name == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					if _, err := fmt.Fprint(w, "test-secret"); err != nil {
						t.Error(err)
					}
					return
				}
				if name == "redirect" {
					w.Header().Set("Location", "/credential-trap")
					w.WriteHeader(http.StatusFound)
					return
				}
				if _, err := fmt.Fprint(w, body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			provider := NewStockQuoteProvider("test-key", "test-secret")
			provider.baseURL = server.URL
			feed := "iex"
			if name == "missing_feed" {
				feed = ""
			}
			evidence, err := provider.LatestQuote(t.Context(), "SPY", feed)
			if name != "valid" {
				if err == nil || strings.Contains(err.Error(), "test-secret") {
					t.Fatalf("invalid response handling: %v", err)
				}
				if name == "missing_feed" && requests != 0 || name != "missing_feed" && requests != 1 {
					t.Fatalf("unexpected requests: %d", requests)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(body))
			if evidence.ResponseSHA256 != hex.EncodeToString(digest[:]) || string(evidence.RawResponse) != body || evidence.Bid.String() != "600.010001" || evidence.Feed != "iex" || evidence.ExchangeAt.Nanosecond() != 123456789 || !evidence.ObservedAt.After(evidence.ExchangeAt) || strings.Contains(evidence.RequestPath, "test-key") {
				t.Fatal("source bytes, precision, timing or feed not retained")
			}
		})
	}
}

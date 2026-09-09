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

	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

func TestQuoteConditionsSource(t *testing.T) {
	for _, name := range []string{"valid", "empty", "wrong_shape", "forbidden", "invalid_tape", "redirect"} {
		t.Run(name, func(t *testing.T) {
			body := `{"R":"Regular Market Maker Open"}`
			if name == "empty" {
				body = `{}`
			}
			if name == "wrong_shape" {
				body = `{"R":2}`
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v2/stocks/meta/conditions/quote" || r.URL.Query().Get("tape") != "B" || r.Header.Get("APCA-API-KEY-ID") != "key" || r.Header.Get("APCA-API-SECRET-KEY") != "secret" {
					t.Error("incorrect source request")
				}
				if name == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
				}
				if name == "redirect" {
					w.Header().Set("Location", "/trap")
					w.WriteHeader(http.StatusFound)
				}
				if _, err := fmt.Fprint(w, body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			provider := NewStockQuoteProvider("key", "secret")
			provider.baseURL = server.URL
			tape := "B"
			if name == "invalid_tape" {
				tape = "iex"
			}
			evidence, err := provider.QuoteConditions(t.Context(), tape)
			if name == "valid" {
				digest := sha256.Sum256([]byte(body))
				if err != nil || string(evidence.RawResponse) != body || evidence.ResponseSHA256 != hex.EncodeToString(digest[:]) || evidence.ObservedAt.IsZero() {
					t.Fatal("lost source receipt", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid receipt accepted or secret leaked")
			}
			want := 1
			if name == "invalid_tape" {
				want = 0
			}
			if requests != want {
				t.Fatal("unexpected request count", requests)
			}
		})
	}
}

func testQuoteStatusJoin(t *testing.T, quote StockQuoteEvidence, reference instrument.Instrument, contract instrument.VenueContract, retained time.Time, calendar *MarketClockEvidence) {
	t.Helper()
	for _, name := range []string{"valid", "mixed_conditions", "wrong_tape", "changed_hash", "changed_meaning", "future_receipt"} {
		t.Run("status_"+name, func(t *testing.T) {
			raw := []byte(`{"R":"Regular Market Maker Open"}`)
			if name == "changed_meaning" {
				raw = []byte(`{"R":"Seller"}`)
			}
			digest := sha256.Sum256(raw)
			conditions := &QuoteConditionEvidence{Tape: "B", RequestPath: "/v2/stocks/meta/conditions/quote?tape=B", ResponseSHA256: hex.EncodeToString(digest[:]), RawResponse: raw, ObservedAt: calendar.ObservedAt}
			input := quote
			switch name {
			case "mixed_conditions":
				input.Conditions = []string{"R", "H"}
			case "wrong_tape":
				conditions.Tape = "A"
			case "changed_hash":
				conditions.ResponseSHA256 = "changed"
			case "future_receipt":
				conditions.ObservedAt = retained.Add(time.Second)
			}
			snapshot, err := input.QuoteSnapshotWithStatus(reference, contract, retained, calendar, conditions)
			if name != "valid" {
				if err == nil {
					t.Fatal("accepted invalid status evidence")
				}
				return
			}
			if err != nil || snapshot.MarketStatus != IEXRegularQuoteStatus || snapshot.SessionStatus != "pre" || !strings.Contains(string(snapshot.Metadata), "single_exchange_quote_condition") {
				t.Fatal("lost scoped quote status", err)
			}
		})
	}
}

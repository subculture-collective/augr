package alpaca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestSubmitSpreadOrderSendsStableParentClientIdentity(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"parent-1","legs":[{"id":"leg-1"},{"id":"leg-2"}]}`))
	}))
	defer server.Close()
	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	spread := &domain.OptionSpread{Underlying: "AAPL", Legs: []domain.SpreadLeg{
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, ExecutablePrice: 5},
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00155000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 155, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, ExecutablePrice: 2},
	}}
	ids, err := NewOptionsBroker(client).SubmitSpreadOrder(context.Background(), spread, 1, "augr-option-spread-stable")
	if err != nil {
		t.Fatal(err)
	}
	if payload["client_order_id"] != "augr-option-spread-stable" {
		t.Fatalf("client_order_id = %v", payload["client_order_id"])
	}
	if payload["type"] != "limit" || payload["limit_price"] != "3" {
		t.Fatalf("spread was not bounded by exact net limit: %#v", payload)
	}
	if len(ids) != 3 || ids[0] != "parent-1" {
		t.Fatalf("spread ids = %v", ids)
	}
}

func TestPreflightPaperOptionsVerifiesIdentityLevelAndBuyingPower(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/account" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"account_number":"paper-123","status":"ACTIVE","trading_blocked":false,"options_trading_level":3,"options_approved_level":3,"currency":"USD","cash":"10000","buying_power":"20000","options_buying_power":"5000","equity":"10000"}`))
	}))
	defer server.Close()
	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)
	broker := NewOptionsBroker(client).WithExpectedPaperAccount("paper-123")
	if err := broker.PreflightPaperOptions(context.Background(), 4500); err != nil {
		t.Fatal(err)
	}
	if err := broker.PreflightPaperOptions(context.Background(), 5500); paperOptionsPreflightReason(t, err) != "insufficient_options_buying_power" {
		t.Fatalf("insufficient options buying power reason = %v", err)
	}
	if err := NewOptionsBroker(client).WithExpectedPaperAccount("wrong").PreflightPaperOptions(context.Background(), 1); paperOptionsPreflightReason(t, err) != "account_identity_mismatch" {
		t.Fatalf("wrong paper account reason = %v", err)
	}
	liveClient := NewClient("test-key", "test-secret", false, discardLogger())
	liveClient.SetBaseURL(server.URL)
	if err := NewOptionsBroker(liveClient).WithExpectedPaperAccount("paper-123").PreflightPaperOptions(context.Background(), 1); paperOptionsPreflightReason(t, err) != "paper_endpoint_required" {
		t.Fatalf("live endpoint reason = %v", err)
	}
}

func paperOptionsPreflightReason(t *testing.T, err error) string {
	t.Helper()
	var typed *PaperOptionsPreflightError
	if !errors.As(err, &typed) {
		t.Fatalf("preflight error %v is not typed", err)
	}
	return typed.PaperOptionsPreflightReason()
}

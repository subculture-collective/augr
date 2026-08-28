package alpaca

import (
	"context"
	"encoding/json"
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
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1},
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00155000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 155, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1},
	}}
	ids, err := NewOptionsBroker(client).SubmitSpreadOrder(context.Background(), spread, 1, "augr-option-spread-stable")
	if err != nil {
		t.Fatal(err)
	}
	if payload["client_order_id"] != "augr-option-spread-stable" {
		t.Fatalf("client_order_id = %v", payload["client_order_id"])
	}
	if len(ids) != 3 || ids[0] != "parent-1" {
		t.Fatalf("spread ids = %v", ids)
	}
}

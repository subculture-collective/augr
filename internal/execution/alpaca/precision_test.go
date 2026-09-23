package alpaca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestFormatPriceAppliesAlpacaDecimalRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{in: 150.123456, want: "150.12"},
		{in: 150.126, want: "150.13"},
		{in: 1.0, want: "1"},
		{in: 1.006, want: "1.01"},
		{in: 0.123456, want: "0.1235"},
		{in: 0.99999, want: "1"},
		{in: 0.5, want: "0.5"},
		{in: 100, want: "100"},
	} {
		if got := formatPrice(tc.in); got != tc.want {
			t.Errorf("formatPrice(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatQuantityLimitsFractionalPrecision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{in: 1.5, want: "1.5"},
		{in: 0.1234567891234, want: "0.123456789"},
		{in: 3.000000000, want: "3"},
		{in: 2.0000000004, want: "2"},
		{in: 12.10, want: "12.1"},
	} {
		if got := formatQuantity(tc.in); got != tc.want {
			t.Errorf("formatQuantity(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMapSubmitOrderRequestRoundsPricesAndQuantity(t *testing.T) {
	t.Parallel()
	limit := 150.12345
	stop := 0.123456
	request, err := mapSubmitOrderRequest(&domain.Order{Ticker: "AAPL", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeStopLimit, Quantity: 1.1234567891234, LimitPrice: &limit, StopPrice: &stop})
	if err != nil {
		t.Fatal(err)
	}
	if request.Qty != "1.123456789" || request.LimitPrice != "150.12" || request.StopPrice != "0.1235" {
		t.Fatalf("request = %+v", request)
	}
}

func TestMapOrderStatusToleratesUnknownAndListsDocumentedStatuses(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]domain.OrderStatus{
		"pending_new":          domain.OrderStatusPending,
		"pending_review":       domain.OrderStatusPending,
		"pending_replace":      domain.OrderStatusPending,
		"accepted_for_bidding": domain.OrderStatusPending,
		"calculated":           domain.OrderStatusPending,
		"held":                 domain.OrderStatusPending,
		"new":                  domain.OrderStatusSubmitted,
		"replaced":             domain.OrderStatusSubmitted,
		"stopped":              domain.OrderStatusSubmitted,
		"suspended":            domain.OrderStatusSubmitted,
		"done_for_day":         domain.OrderStatusSubmitted,
		"partially_filled":     domain.OrderStatusPartial,
		"filled":               domain.OrderStatusFilled,
		"canceled":             domain.OrderStatusCancelled,
		"expired":              domain.OrderStatusCancelled,
		"rejected":             domain.OrderStatusRejected,
		"some_future_status":   domain.OrderStatusSubmitted,
	} {
		got, err := mapOrderStatus(raw)
		if err != nil || got != want {
			t.Errorf("mapOrderStatus(%q) = %s, %v; want %s", raw, got, err, want)
		}
	}
	if _, err := mapOrderStatus(""); err == nil {
		t.Fatal("blank status must still error")
	}
}

func TestSubmitOptionOrderConvertsMarketWithLimitToLimit(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"opt-1"}`))
	}))
	defer server.Close()
	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)
	intent := domain.PositionIntentBuyToOpen
	limit := 5.123
	id, err := NewOptionsBroker(client).SubmitOptionOrder(context.Background(), &domain.Order{Ticker: "AAPL271217C00150000", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeMarket, Quantity: 1, LimitPrice: &limit, PositionIntent: &intent, ClientOrderID: "augr-opt"})
	if err != nil || id != "opt-1" {
		t.Fatalf("SubmitOptionOrder() = %q, %v", id, err)
	}
	if payload["type"] != "limit" || payload["limit_price"] != "5.12" {
		t.Fatalf("market order with limit price was not converted: %#v", payload)
	}

	// A market order without a limit price stays market and omits limit_price.
	payload = nil
	_, err = NewOptionsBroker(client).SubmitOptionOrder(context.Background(), &domain.Order{Ticker: "AAPL271217C00150000", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeMarket, Quantity: 1, PositionIntent: &intent})
	if err != nil {
		t.Fatal(err)
	}
	if payload["type"] != "market" {
		t.Fatalf("type = %v, want market", payload["type"])
	}
	if _, present := payload["limit_price"]; present {
		t.Fatalf("market order sent limit_price: %#v", payload)
	}
}

func TestGetOptionsContractsPaginates(t *testing.T) {
	t.Parallel()
	var seenTokens []string
	var seenLimits []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/options/contracts" {
			t.Errorf("path = %s", r.URL.Path)
		}
		token := r.URL.Query().Get("page_token")
		seenTokens = append(seenTokens, token)
		seenLimits = append(seenLimits, r.URL.Query().Get("limit"))
		w.Header().Set("Content-Type", "application/json")
		switch token {
		case "":
			_, _ = w.Write([]byte(`{"option_contracts":[{"symbol":"AAPL271217C00150000","style":"american","multiplier":"100"}],"next_page_token":"page-2"}`))
		case "page-2":
			_, _ = w.Write([]byte(`{"option_contracts":[{"symbol":"AAPL271217P00150000","style":"american","multiplier":"100"}],"next_page_token":null}`))
		default:
			t.Errorf("unexpected page token %q", token)
		}
	}))
	defer server.Close()
	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)
	contracts, err := NewOptionsBroker(client).GetOptionsContracts(context.Background(), "aapl")
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 2 {
		t.Fatalf("contracts = %d, want 2 across pages", len(contracts))
	}
	if strings.Join(seenTokens, ",") != ",page-2" || seenLimits[0] != "10000" {
		t.Fatalf("pagination requests = tokens %v limits %v", seenTokens, seenLimits)
	}
}

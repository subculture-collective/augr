package binance

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

func TestBrokerSubmitOrder_MapsSupportedOrderTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		order          *domain.Order
		wantExternalID string
		wantQuery      map[string]string
	}{
		{
			name: "market",
			order: &domain.Order{
				Ticker:    "btcusdt",
				Side:      domain.OrderSideBuy,
				OrderType: domain.OrderTypeMarket,
				Quantity:  0.25,
			},
			wantExternalID: "BTCUSDT:12345",
			wantQuery: map[string]string{
				"symbol":   "BTCUSDT",
				"side":     "BUY",
				"type":     "MARKET",
				"quantity": "0.25",
			},
		},
		{
			name: "limit",
			order: &domain.Order{
				Ticker:     "ETHUSDT",
				Side:       domain.OrderSideSell,
				OrderType:  domain.OrderTypeLimit,
				Quantity:   1.5,
				LimitPrice: floatPtr(3150.5),
			},
			wantExternalID: "ETHUSDT:12345",
			wantQuery: map[string]string{
				"symbol":      "ETHUSDT",
				"side":        "SELL",
				"type":        "LIMIT",
				"quantity":    "1.5",
				"price":       "3150.5",
				"timeInForce": "GTC",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			requests := make(chan struct {
				method string
				path   string
				query  url.Values
			}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v3/exchangeInfo" {
					_, _ = w.Write([]byte(exchangeInfoJSON(r.URL.Query().Get("symbol"), "0.01", "0.01", "0.5", "10")))
					return
				}
				requests <- struct {
					method string
					path   string
					query  url.Values
				}{
					method: r.Method,
					path:   r.URL.Path,
					query:  r.URL.Query(),
				}

				_, _ = w.Write([]byte(`{"symbol":"` + tt.wantQuery["symbol"] + `","orderId":12345}`))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-secret", true, discardLogger())
			client.SetBaseURL(server.URL)
			client.now = func() time.Time {
				return time.UnixMilli(1700000000123)
			}

			broker := NewBroker(client)
			externalID, err := broker.SubmitOrder(context.Background(), tt.order)
			if err != nil {
				t.Fatalf("SubmitOrder() error = %v", err)
			}
			if externalID != tt.wantExternalID {
				t.Fatalf("SubmitOrder() externalID = %q, want %q", externalID, tt.wantExternalID)
			}

			select {
			case request := <-requests:
				if request.method != http.MethodPost {
					t.Fatalf("request method = %s, want %s", request.method, http.MethodPost)
				}
				if request.path != "/api/v3/order" {
					t.Fatalf("request path = %s, want %s", request.path, "/api/v3/order")
				}
				for key, want := range tt.wantQuery {
					if got := request.query.Get(key); got != want {
						t.Fatalf("%s = %q, want %q", key, got, want)
					}
				}
				if request.query.Get("timestamp") == "" {
					t.Fatal("timestamp query param is missing")
				}
				if request.query.Get("signature") == "" {
					t.Fatal("signature query param is missing")
				}
			case <-time.After(time.Second):
				t.Fatal("request details were not captured")
			}
		})
	}
}

func TestBrokerSubmitOrder_RejectsUnsupportedOrderType(t *testing.T) {
	t.Parallel()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	broker := NewBroker(client)

	_, err := broker.SubmitOrder(context.Background(), &domain.Order{
		Ticker:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeStop,
		Quantity:  1,
	})
	if err == nil {
		t.Fatal("SubmitOrder() error = nil, want non-nil")
	}
	if err.Error() != `binance: unsupported order type "stop"` {
		t.Fatalf("SubmitOrder() error = %q, want unsupported type error", err.Error())
	}
}

func TestBrokerSubmitOrder_RequiresOrderSide(t *testing.T) {
	t.Parallel()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	broker := NewBroker(client)

	_, err := broker.SubmitOrder(context.Background(), &domain.Order{
		Ticker:    "BTCUSDT",
		OrderType: domain.OrderTypeMarket,
		Quantity:  1,
	})
	if err == nil {
		t.Fatal("SubmitOrder() error = nil, want non-nil")
	}
	if err.Error() != "binance: order side is required" {
		t.Fatalf("SubmitOrder() error = %q, want required side error", err.Error())
	}
}

func TestBrokerCancelOrder_UsesCompositeExternalID(t *testing.T) {
	t.Parallel()

	requests := make(chan struct {
		method string
		path   string
		query  url.Values
	}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct {
			method string
			path   string
			query  url.Values
		}{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.Query(),
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","orderId":12345}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)
	client.now = func() time.Time {
		return time.UnixMilli(1700000000123)
	}

	broker := NewBroker(client)
	if err := broker.CancelOrder(context.Background(), "btcusdt:12345"); err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}

	select {
	case request := <-requests:
		if request.method != http.MethodDelete {
			t.Fatalf("request method = %s, want %s", request.method, http.MethodDelete)
		}
		if request.path != "/api/v3/order" {
			t.Fatalf("request path = %s, want %s", request.path, "/api/v3/order")
		}
		if got := request.query.Get("symbol"); got != "BTCUSDT" {
			t.Fatalf("symbol = %q, want %q", got, "BTCUSDT")
		}
		if got := request.query.Get("orderId"); got != "12345" {
			t.Fatalf("orderId = %q, want %q", got, "12345")
		}
	case <-time.After(time.Second):
		t.Fatal("request details were not captured")
	}
}

func TestBrokerGetOrderStatus_MapsBinanceStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		apiStatus  string
		wantStatus domain.OrderStatus
	}{
		{name: "pending", apiStatus: "PENDING_CANCEL", wantStatus: domain.OrderStatusPending},
		{name: "submitted", apiStatus: "NEW", wantStatus: domain.OrderStatusSubmitted},
		{name: "partial", apiStatus: "PARTIALLY_FILLED", wantStatus: domain.OrderStatusPartial},
		{name: "filled", apiStatus: "FILLED", wantStatus: domain.OrderStatusFilled},
		{name: "cancelled", apiStatus: "EXPIRED", wantStatus: domain.OrderStatusCancelled},
		{name: "rejected", apiStatus: "REJECTED", wantStatus: domain.OrderStatusRejected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			requests := make(chan url.Values, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.Query()

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"` + tt.apiStatus + `"}`))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-secret", true, discardLogger())
			client.SetBaseURL(server.URL)
			client.now = func() time.Time {
				return time.UnixMilli(1700000000456)
			}

			broker := NewBroker(client)
			got, err := broker.GetOrderStatus(context.Background(), "ETHUSDT:999")
			if err != nil {
				t.Fatalf("GetOrderStatus() error = %v", err)
			}
			if got != tt.wantStatus {
				t.Fatalf("GetOrderStatus() = %q, want %q", got, tt.wantStatus)
			}

			select {
			case query := <-requests:
				if query.Get("symbol") != "ETHUSDT" {
					t.Fatalf("symbol = %q, want %q", query.Get("symbol"), "ETHUSDT")
				}
				if query.Get("orderId") != "999" {
					t.Fatalf("orderId = %q, want %q", query.Get("orderId"), "999")
				}
			case <-time.After(time.Second):
				t.Fatal("request details were not captured")
			}
		})
	}
}

func TestBrokerGetOrderStatus_RejectsMalformedExternalID(t *testing.T) {
	t.Parallel()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	broker := NewBroker(client)

	_, err := broker.GetOrderStatus(context.Background(), "12345")
	if err == nil {
		t.Fatal("GetOrderStatus() error = nil, want non-nil")
	}
	wantErr := `binance: external order id "12345" must be in SYMBOL:ORDER_ID format`
	if err.Error() != wantErr {
		t.Fatalf("GetOrderStatus() error = %q, want %q", err.Error(), wantErr)
	}
}

func TestBrokerGetPositions_MapsAccountBalances(t *testing.T) {
	t.Parallel()

	requests := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Query()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"balances": [
				{"asset":"BTC","free":"0.50","locked":"0.10"},
				{"asset":"USDT","free":"0","locked":"0"},
				{"asset":"ETH","free":"0","locked":"2.00"}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)
	client.now = func() time.Time {
		return time.UnixMilli(1700000000456)
	}

	broker := NewBroker(client)
	positions, err := broker.GetPositions(context.Background())
	if err != nil {
		t.Fatalf("GetPositions() error = %v", err)
	}
	if len(positions) != 2 {
		t.Fatalf("len(GetPositions()) = %d, want %d", len(positions), 2)
	}

	if positions[0].Ticker != "BTC" {
		t.Fatalf("positions[0].Ticker = %q, want %q", positions[0].Ticker, "BTC")
	}
	if positions[0].Side != domain.PositionSideLong {
		t.Fatalf("positions[0].Side = %q, want %q", positions[0].Side, domain.PositionSideLong)
	}
	assertFloatClose(t, positions[0].Quantity, 0.6, 1e-9)
	if positions[0].AvgEntry != 0 {
		t.Fatalf("positions[0].AvgEntry = %v, want 0", positions[0].AvgEntry)
	}

	if positions[1].Ticker != "ETH" {
		t.Fatalf("positions[1].Ticker = %q, want %q", positions[1].Ticker, "ETH")
	}
	assertFloatClose(t, positions[1].Quantity, 2.0, 1e-9)

	select {
	case query := <-requests:
		if query.Get("timestamp") == "" {
			t.Fatal("timestamp query param is missing")
		}
	case <-time.After(time.Second):
		t.Fatal("request details were not captured")
	}
}

func TestBrokerGetPositions_RejectsInvalidNumericField(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"balances": [
				{"asset":"BTC","free":"oops","locked":"0.10"}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)

	broker := NewBroker(client)
	_, err := broker.GetPositions(context.Background())
	if err == nil {
		t.Fatal("GetPositions() error = nil, want non-nil")
	}
	wantErr := `binance: parse free: strconv.ParseFloat: parsing "oops": invalid syntax`
	if err.Error() != wantErr {
		t.Fatalf("GetPositions() error = %q, want %q", err.Error(), wantErr)
	}
}

func TestBrokerGetAccountBalance_PrefersStablecoinCashBalance(t *testing.T) {
	t.Parallel()

	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"balances": [
				{"asset":"BTC","free":"0.50","locked":"0.10"},
				{"asset":"USDT","free":"1000.50","locked":"250.25"}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)

	broker := NewBroker(client)
	balance, err := broker.GetAccountBalance(context.Background())
	if err != nil {
		t.Fatalf("GetAccountBalance() error = %v", err)
	}
	if balance.Currency != "USDT" {
		t.Fatalf("GetAccountBalance() Currency = %q, want %q", balance.Currency, "USDT")
	}
	if balance.Cash != 1000.50 {
		t.Fatalf("GetAccountBalance() Cash = %v, want %v", balance.Cash, 1000.50)
	}
	if balance.BuyingPower != 1000.50 {
		t.Fatalf("GetAccountBalance() BuyingPower = %v, want %v", balance.BuyingPower, 1000.50)
	}
	if balance.Equity != 1250.75 {
		t.Fatalf("GetAccountBalance() Equity = %v, want %v", balance.Equity, 1250.75)
	}

	select {
	case path := <-requests:
		if path != "/api/v3/account" {
			t.Fatalf("request path = %s, want %s", path, "/api/v3/account")
		}
	case <-time.After(time.Second):
		t.Fatal("request details were not captured")
	}
}

func TestBrokerGetAccountBalance_SkipsZeroPreferredStablecoin(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"balances": [
				{"asset":"USDT","free":"0","locked":"0"},
				{"asset":"BTC","free":"0.50","locked":"0.10"}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)

	broker := NewBroker(client)
	balance, err := broker.GetAccountBalance(context.Background())
	if err != nil {
		t.Fatalf("GetAccountBalance() error = %v", err)
	}
	if balance.Currency != "BTC" {
		t.Fatalf("GetAccountBalance() Currency = %q, want %q", balance.Currency, "BTC")
	}
	assertFloatClose(t, balance.Cash, 0.5, 1e-9)
	assertFloatClose(t, balance.BuyingPower, 0.5, 1e-9)
	assertFloatClose(t, balance.Equity, 0.6, 1e-9)
}

func TestBrokerGetAccountBalance_RejectsInvalidResponseFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing asset",
			body:    `{"balances":[{"asset":"   ","free":"1","locked":"0"}]}`,
			wantErr: "binance: balance asset is required",
		},
		{
			name:    "invalid free",
			body:    `{"balances":[{"asset":"USDT","free":"bad","locked":"0"}]}`,
			wantErr: `binance: parse free: strconv.ParseFloat: parsing "bad": invalid syntax`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-secret", true, discardLogger())
			client.SetBaseURL(server.URL)

			broker := NewBroker(client)
			_, err := broker.GetAccountBalance(context.Background())
			if err == nil {
				t.Fatal("GetAccountBalance() error = nil, want non-nil")
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("GetAccountBalance() error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBrokerSubmitOrder_DecodeResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v3/exchangeInfo" {
			_, _ = w.Write([]byte(exchangeInfoJSON("BTCUSDT", "0.00001", "0.00001", "0.01", "5")))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"symbol":  "BTCUSDT",
			"orderId": 12345,
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)

	broker := NewBroker(client)
	externalID, err := broker.SubmitOrder(context.Background(), &domain.Order{
		Ticker:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Quantity:  1,
	})
	if err != nil {
		t.Fatalf("SubmitOrder() error = %v", err)
	}
	if externalID != "BTCUSDT:12345" {
		t.Fatalf("SubmitOrder() externalID = %q, want %q", externalID, "BTCUSDT:12345")
	}
}

func floatPtr(value float64) *float64 {
	return &value
}

func assertFloatClose(t *testing.T, got, want, delta float64) {
	t.Helper()

	if math.Abs(got-want) > delta {
		t.Fatalf("value = %.12f, want %.12f (delta %.12f)", got, want, delta)
	}
}

// exchangeInfoJSON builds a minimal /api/v3/exchangeInfo body for one symbol.
func exchangeInfoJSON(symbol, stepSize, minQty, tickSize, minNotional string) string {
	return `{"symbols":[{"symbol":"` + symbol + `","status":"TRADING","filters":[` +
		`{"filterType":"PRICE_FILTER","minPrice":"0.01","maxPrice":"1000000","tickSize":"` + tickSize + `"},` +
		`{"filterType":"LOT_SIZE","minQty":"` + minQty + `","maxQty":"9000","stepSize":"` + stepSize + `"},` +
		`{"filterType":"NOTIONAL","minNotional":"` + minNotional + `","applyMinToMarket":true}` +
		`]}]}`
}

func newFilterTestBroker(t *testing.T, exchangeInfo string, onOrder func(w http.ResponseWriter, r *http.Request)) (*Broker, *int32) {
	t.Helper()
	var infoCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v3/exchangeInfo" {
			atomic.AddInt32(&infoCalls, 1)
			_, _ = w.Write([]byte(exchangeInfo))
			return
		}
		onOrder(w, r)
	}))
	t.Cleanup(server.Close)
	client := NewClient("test-key", "test-secret", true, discardLogger())
	client.SetBaseURL(server.URL)
	return NewBroker(client), &infoCalls
}

func TestBrokerSubmitOrder_AppliesExchangeFiltersAndClientOrderID(t *testing.T) {
	t.Parallel()

	requests := make(chan url.Values, 4)
	broker, infoCalls := newFilterTestBroker(t, exchangeInfoJSON("BTCUSDT", "0.001", "0.001", "0.1", "10"), func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Query()
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","orderId":77,"status":"NEW","executedQty":"0","cummulativeQuoteQty":"0"}`))
	})

	limit := 30123.456
	order := &domain.Order{ClientOrderID: "client-abc", Ticker: "BTCUSDT", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 0.0123456, LimitPrice: &limit}
	if _, err := broker.SubmitOrder(context.Background(), order); err != nil {
		t.Fatalf("SubmitOrder() error = %v", err)
	}
	query := <-requests
	if query.Get("quantity") != "0.012" || query.Get("price") != "30123.4" || query.Get("newClientOrderId") != "client-abc" || query.Get("newOrderRespType") != "FULL" {
		t.Fatalf("submit query = %v", query)
	}

	// A sell rounds the price up so the order is never more aggressive.
	sell := &domain.Order{ClientOrderID: "client-def", Ticker: "BTCUSDT", Side: domain.OrderSideSell, OrderType: domain.OrderTypeLimit, Quantity: 0.5, LimitPrice: &limit}
	if _, err := broker.SubmitOrder(context.Background(), sell); err != nil {
		t.Fatalf("SubmitOrder(sell) error = %v", err)
	}
	query = <-requests
	if query.Get("price") != "30123.5" || query.Get("quantity") != "0.5" {
		t.Fatalf("sell query = %v", query)
	}
	if got := atomic.LoadInt32(infoCalls); got != 1 {
		t.Fatalf("exchangeInfo calls = %d, want 1 (cached)", got)
	}
}

func TestBrokerSubmitOrder_RejectsBelowMinQtyAndMinNotional(t *testing.T) {
	t.Parallel()

	broker, _ := newFilterTestBroker(t, exchangeInfoJSON("ETHUSDT", "0.001", "0.01", "0.01", "10"), func(w http.ResponseWriter, _ *http.Request) {
		t.Error("order must not be submitted")
		_, _ = w.Write([]byte(`{}`))
	})

	limit := 2000.0
	_, err := broker.SubmitOrder(context.Background(), &domain.Order{Ticker: "ETHUSDT", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 0.0015, LimitPrice: &limit})
	if err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("SubmitOrder() minQty error = %v", err)
	}
	_, err = broker.SubmitOrder(context.Background(), &domain.Order{Ticker: "ETHUSDT", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 0.02, LimitPrice: floatPtr(100)})
	if err == nil || !strings.Contains(err.Error(), "notional") {
		t.Fatalf("SubmitOrder() minNotional error = %v", err)
	}
	_, err = broker.SubmitOrder(context.Background(), &domain.Order{Ticker: "ETHUSDT", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeMarket, Quantity: 0.0004})
	if err == nil || !strings.Contains(err.Error(), "rounds to zero") {
		t.Fatalf("SubmitOrder() step rounding error = %v", err)
	}
}

func TestBrokerSubmitOrder_RejectsUnknownSymbolInExchangeInfo(t *testing.T) {
	t.Parallel()

	broker, _ := newFilterTestBroker(t, `{"symbols":[]}`, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	_, err := broker.SubmitOrder(context.Background(), &domain.Order{Ticker: "NOPEUSDT", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeMarket, Quantity: 1})
	if err == nil || !strings.Contains(err.Error(), "does not include symbol") {
		t.Fatalf("SubmitOrder() error = %v", err)
	}
}

func TestBrokerGetOrderStatusResult_ParsesFillEconomics(t *testing.T) {
	t.Parallel()

	broker, _ := newFilterTestBroker(t, `{}`, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("orderId") != "999" {
			t.Errorf("orderId = %q", r.URL.Query().Get("orderId"))
		}
		_, _ = w.Write([]byte(`{"symbol":"ETHUSDT","orderId":999,"status":"PARTIALLY_FILLED","executedQty":"0.50000000","cummulativeQuoteQty":"1500.00000000","updateTime":1700000000000}`))
	})
	result, err := broker.GetOrderStatusResult(context.Background(), "ETHUSDT:999")
	if err != nil {
		t.Fatalf("GetOrderStatusResult() error = %v", err)
	}
	if result.Status != domain.OrderStatusPartial || result.FilledQuantity != 0.5 || result.FilledAvgPrice == nil || *result.FilledAvgPrice != 3000 {
		t.Fatalf("result = %+v", result)
	}
	if result.FilledAt == nil || result.FilledAt.UnixMilli() != 1700000000000 {
		t.Fatalf("FilledAt = %v", result.FilledAt)
	}
	var _ execution.BrokerOrderStatusProvider = broker
	var _ execution.BrokerClientOrderStatusProvider = broker
}

func TestBrokerOrderEconomics_UsesFillsWhenCumulativeMissing(t *testing.T) {
	t.Parallel()

	var response orderResponse
	if err := json.Unmarshal([]byte(`{"orderId":1,"status":"FILLED","fills":[{"price":"100","qty":"1"},{"price":"110","qty":"1"}]}`), &response); err != nil {
		t.Fatal(err)
	}
	result, err := orderEconomics(response)
	if err != nil {
		t.Fatalf("orderEconomics() error = %v", err)
	}
	if result.Status != domain.OrderStatusFilled || result.FilledQuantity != 2 || result.FilledAvgPrice == nil || *result.FilledAvgPrice != 105 {
		t.Fatalf("result = %+v", result)
	}
}

func TestBrokerGetOrderStatusByClientOrderID(t *testing.T) {
	t.Parallel()

	broker, _ := newFilterTestBroker(t, `{}`, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("symbol") != "BTCUSDT" || query.Get("origClientOrderId") != "client-1" {
			t.Errorf("query = %v", query)
		}
		if query.Get("origClientOrderId") == "client-1" {
			_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","orderId":55,"clientOrderId":"client-1","status":"FILLED","executedQty":"1","cummulativeQuoteQty":"20000"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-2013,"msg":"Order does not exist."}`))
	})
	externalID, status, err := broker.GetOrderStatusByClientOrderIDResult(context.Background(), "BTCUSDT:client-1")
	if err != nil {
		t.Fatalf("GetOrderStatusByClientOrderIDResult() error = %v", err)
	}
	if externalID != "BTCUSDT:55" || status.Status != domain.OrderStatusFilled || status.FilledQuantity != 1 || *status.FilledAvgPrice != 20000 {
		t.Fatalf("result = %q %+v", externalID, status)
	}
	if _, _, err := broker.GetOrderStatusByClientOrderIDResult(context.Background(), "client-1"); err == nil {
		t.Fatal("bare client order id was accepted without a symbol")
	}
}

func TestBrokerGetOrderStatusByClientOrderID_NotFound(t *testing.T) {
	t.Parallel()

	broker, _ := newFilterTestBroker(t, `{}`, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-2013,"msg":"Order does not exist."}`))
	})
	_, _, err := broker.GetOrderStatusByClientOrderIDResult(context.Background(), "BTCUSDT:missing")
	if !errors.Is(err, execution.ErrBrokerOrderNotFound) {
		t.Fatalf("error = %v, want ErrBrokerOrderNotFound", err)
	}
}

func TestBrokerGetPositions_ExcludesQuoteAssets(t *testing.T) {
	t.Parallel()

	broker, _ := newFilterTestBroker(t, `{}`, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"balances":[{"asset":"USDT","free":"1500","locked":"0"},{"asset":"USDC","free":"20","locked":"0"},{"asset":"BTC","free":"0.5","locked":"0"}]}`))
	})
	positions, err := broker.GetPositions(context.Background())
	if err != nil {
		t.Fatalf("GetPositions() error = %v", err)
	}
	if len(positions) != 1 || positions[0].Ticker != "BTC" || positions[0].AvgEntry != 0 {
		t.Fatalf("positions = %+v, want only BTC with unknown entry", positions)
	}
}

func TestApplySymbolFiltersRounding(t *testing.T) {
	t.Parallel()

	filters := symbolFilters{Symbol: "X", StepSize: 0.1, MinQty: 0.1, TickSize: 0.5, MinNotional: 1}
	price := 10.26
	// 1.3/0.1 is 12.999999999999998 in floating point; the epsilon keeps it at 13 steps.
	shaped, err := applySymbolFilters(&domain.Order{Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 1.3, LimitPrice: &price}, filters)
	if err != nil {
		t.Fatalf("applySymbolFilters() error = %v", err)
	}
	if shaped.Quantity != "1.3" || shaped.Price != "10" {
		t.Fatalf("shaped = %+v", shaped)
	}
	shaped, err = applySymbolFilters(&domain.Order{Side: domain.OrderSideSell, OrderType: domain.OrderTypeLimit, Quantity: 2, LimitPrice: &price}, filters)
	if err != nil || shaped.Price != "10.5" {
		t.Fatalf("sell shaped = %+v err = %v", shaped, err)
	}
}

package kalshi

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

func TestHTTPClientCreateOrder_MapsNoSideLimitBuy(t *testing.T) {
	t.Parallel()

	client := &fakeSignedClient{
		postResp: []byte(`{"order":{"order_id":"ord-123"}}`),
	}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatalf("NewLiveHTTPClient() error = %v", err)
	}

	noPrice := int64(58)
	resp, err := adapter.CreateOrder(context.Background(), CreateOrderRequest{
		Ticker:        "KX-EXAMPLE",
		Side:          "no",
		Action:        "buy",
		Count:         2,
		Type:          "limit",
		NoPrice:       &noPrice,
		ClientOrderID: "order-123",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if resp.OrderID != "ord-123" {
		t.Fatalf("CreateOrder() order id = %q", resp.OrderID)
	}
	if client.postPath != "/portfolio/orders" {
		t.Fatalf("postPath = %q", client.postPath)
	}
	var payload map[string]any
	if err := json.Unmarshal(client.postBody, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["ticker"] != "KX-EXAMPLE" || payload["action"] != "buy" || payload["side"] != "no" || payload["count"] != float64(2) || payload["type"] != "limit" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload["no_price"] != float64(58) || payload["client_order_id"] != "order-123" || payload["time_in_force"] != "good_till_canceled" {
		t.Fatalf("payload = %#v", payload)
	}
	for _, forbidden := range []string{"yes_price", "price", "self_trade_prevention_type"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("payload contains %q: %#v", forbidden, payload)
		}
	}
}

func TestHTTPClientCreateOrder_MapsYesSideLimitSell(t *testing.T) {
	t.Parallel()

	client := &fakeSignedClient{postResp: []byte(`{"order":{"order_id":"ord-456"}}`)}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatalf("NewLiveHTTPClient() error = %v", err)
	}
	yesPrice := int64(63)
	if _, err := adapter.CreateOrder(context.Background(), CreateOrderRequest{Ticker: "KX-EXAMPLE", Side: "yes", Action: "sell", Count: 5, Type: "limit", YesPrice: &yesPrice, ClientOrderID: "order-456"}); err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(client.postBody, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["action"] != "sell" || payload["side"] != "yes" || payload["yes_price"] != float64(63) || payload["count"] != float64(5) {
		t.Fatalf("payload = %#v", payload)
	}
	if _, ok := payload["no_price"]; ok {
		t.Fatalf("payload contains no_price: %#v", payload)
	}
}

func TestHTTPClientCancelOrder_UsesDelete(t *testing.T) {
	t.Parallel()

	client := &fakeSignedClient{}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatalf("NewLiveHTTPClient() error = %v", err)
	}
	if err := adapter.CancelOrder(context.Background(), "ord-123"); err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}
	if client.deletePath != "/portfolio/orders/ord-123" {
		t.Fatalf("deletePath = %q", client.deletePath)
	}
}

func TestHTTPClientCreateOrderRejectsMarketOrders(t *testing.T) {
	t.Parallel()

	adapter, err := NewLiveHTTPClient(&fakeSignedClient{})
	if err != nil {
		t.Fatalf("NewLiveHTTPClient() error = %v", err)
	}
	_, err = adapter.CreateOrder(context.Background(), CreateOrderRequest{Ticker: "KX-EXAMPLE", Side: "yes", Action: "buy", Count: 1, Type: "market"})
	if err == nil {
		t.Fatal("CreateOrder() error = nil, want market order disabled error")
	}
}

func TestHTTPClientGetOrder_InfersExecutedWhenRemainingZero(t *testing.T) {
	t.Parallel()

	client := &fakeSignedClient{getResp: []byte(`{"order":{"order_id":"ord-123","remaining_count_fp":"0.00"}}`)}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatalf("NewLiveHTTPClient() error = %v", err)
	}

	resp, err := adapter.GetOrder(context.Background(), "ord-123")
	if err != nil {
		t.Fatalf("GetOrder() error = %v", err)
	}
	if resp.OrderID != "ord-123" || resp.Status != "executed" {
		t.Fatalf("GetOrder() = %#v, want executed order", resp)
	}
	if client.getPath != "/portfolio/orders/ord-123" {
		t.Fatalf("getPath = %q", client.getPath)
	}
}

func TestHTTPClientGetOrderByClientOrderID(t *testing.T) {
	client := &fakeSignedClient{getHandler: func(path string, query map[string]string) ([]byte, error) {
		if path != "/portfolio/orders" {
			return nil, errors.New("unexpected path " + path)
		}
		if query["status"] != "resting" {
			return []byte(`{"orders":[]}`), nil
		}
		// Kalshi ignores client_order_id server-side; the page carries other
		// orders and the adapter must match locally.
		return []byte(`{"orders":[{"order_id":"ord-other","client_order_id":"client-other","status":"resting"},{"order_id":"ord-123","client_order_id":"client-123","status":"resting"}]}`), nil
	}}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatal(err)
	}
	order, err := adapter.GetOrderByClientOrderID(context.Background(), "client-123")
	if err != nil || order.OrderID != "ord-123" || order.ClientOrderID != "client-123" {
		t.Fatalf("GetOrderByClientOrderID() = (%+v, %v)", order, err)
	}
	if len(client.getQueries) != len(clientOrderLookupStatuses) || client.getQueries[0]["status"] != "resting" || client.getQueries[0]["limit"] == "" {
		t.Fatalf("lookup requests = %+v", client.getQueries)
	}
	if _, ok := client.getQueries[0]["client_order_id"]; ok {
		t.Fatalf("lookup sent unsupported client_order_id filter: %+v", client.getQueries[0])
	}
}

func TestHTTPClientGetOrderByClientOrderIDPagesAndReturnsNotFound(t *testing.T) {
	client := &fakeSignedClient{getHandler: func(_ string, query map[string]string) ([]byte, error) {
		if query["status"] == "resting" && query["cursor"] == "" {
			return []byte(`{"orders":[{"order_id":"ord-1","client_order_id":"client-1","status":"resting"}],"cursor":"page-2"}`), nil
		}
		if query["status"] == "resting" && query["cursor"] == "page-2" {
			return []byte(`{"orders":[{"order_id":"ord-2","client_order_id":"client-2","status":"resting"}]}`), nil
		}
		return []byte(`{"orders":[]}`), nil
	}}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.GetOrderByClientOrderID(context.Background(), "client-missing")
	if !errors.Is(err, execution.ErrBrokerOrderNotFound) {
		t.Fatalf("GetOrderByClientOrderID() error = %v, want not found", err)
	}
	if len(client.getQueries) != len(clientOrderLookupStatuses)+1 || client.getQueries[1]["cursor"] != "page-2" {
		t.Fatalf("lookup requests = %+v, want cursor paging", client.getQueries)
	}
}

func TestHTTPClientGetOrderByClientOrderIDDedupesSameOrderAcrossStatuses(t *testing.T) {
	client := &fakeSignedClient{getResp: []byte(`{"orders":[{"order_id":"ord-1","client_order_id":"client-1","status":"executed"}]}`)}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatal(err)
	}
	order, err := adapter.GetOrderByClientOrderID(context.Background(), "client-1")
	if err != nil || order.OrderID != "ord-1" {
		t.Fatalf("GetOrderByClientOrderID() = (%+v, %v)", order, err)
	}
}

func TestHTTPClientGetOrderByClientOrderIDRejectsMultipleMatches(t *testing.T) {
	client := &fakeSignedClient{getResp: []byte(`{"orders":[{"order_id":"ord-1","client_order_id":"client-123","status":"resting"},{"order_id":"ord-2","client_order_id":"client-123","status":"resting"}]}`)}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.GetOrderByClientOrderID(context.Background(), "client-123"); err == nil {
		t.Fatal("multiple client-id matches were accepted")
	}
}

func TestHTTPClientGetOrderByClientOrderIDDoesNotTreatCollection404AsAuthoritativeAbsence(t *testing.T) {
	client := &fakeSignedClient{getErr: errors.New("kalshi: request failed (status=404): not found")}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.GetOrderByClientOrderID(context.Background(), "missing")
	if err == nil || errors.Is(err, execution.ErrBrokerOrderNotFound) {
		t.Fatalf("GetOrderByClientOrderID() error = %v", err)
	}
}

func TestHTTPClientListPositions_PaginatesAndMapsPositions(t *testing.T) {
	t.Parallel()

	client := &fakeSignedClient{}
	client.getHandler = func(path string, query map[string]string) ([]byte, error) {
		if path != "/portfolio/positions" {
			return nil, errors.New("unexpected path: " + path)
		}
		if query["limit"] != "1000" || query["count_filter"] != "position" {
			return nil, errors.New("unexpected query")
		}
		if query["cursor"] == "" {
			return []byte(`{"market_positions":[{"ticker":"KX-YES","position_fp":"2.00","market_exposure_dollars":"1.50"}],"next_cursor":"page2"}`), nil
		}
		if query["cursor"] != "page2" {
			return nil, errors.New("unexpected cursor")
		}
		return []byte(`{"market_positions":[{"ticker":"KX-NO","position_fp":"-1.00","market_exposure_dollars":"0.75"}]}`), nil
	}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatalf("NewLiveHTTPClient() error = %v", err)
	}

	positions, err := adapter.ListPositions(context.Background())
	if err != nil {
		t.Fatalf("ListPositions() error = %v", err)
	}
	if len(positions) != 2 {
		t.Fatalf("ListPositions() len = %d, want 2", len(positions))
	}
	if positions[0].Ticker != "KX-YES" || positions[0].Side != "yes" || positions[0].Count != 2 || positions[0].ValueCents != 150 {
		t.Fatalf("positions[0] = %#v", positions[0])
	}
	if positions[1].Ticker != "KX-NO" || positions[1].Side != "no" || positions[1].Count != 1 || positions[1].ValueCents != 75 {
		t.Fatalf("positions[1] = %#v", positions[1])
	}
	if len(client.getQueries) != 2 || client.getQueries[0]["cursor"] != "" || client.getQueries[1]["cursor"] != "page2" {
		t.Fatalf("getQueries = %#v", client.getQueries)
	}
}

func TestHTTPClientGetBalance_MapsCents(t *testing.T) {
	t.Parallel()

	client := &fakeSignedClient{getResp: []byte(`{"balance":12345,"portfolio_value":67890}`)}
	adapter, err := NewLiveHTTPClient(client)
	if err != nil {
		t.Fatalf("NewLiveHTTPClient() error = %v", err)
	}

	resp, err := adapter.GetBalance(context.Background())
	if err != nil {
		t.Fatalf("GetBalance() error = %v", err)
	}
	if resp.CashCents != 12345 || resp.BuyingPowerCents != 12345 || resp.EquityCents != 67890 {
		t.Fatalf("GetBalance() = %#v", resp)
	}
	if client.getPath != "/portfolio/balance" {
		t.Fatalf("getPath = %q", client.getPath)
	}
}

type fakeSignedClient struct {
	postPath    string
	postBody    []byte
	postResp    []byte
	deletePath  string
	deleteQuery map[string]string
	getPath     string
	getQueries  []map[string]string
	getResp     []byte
	getErr      error
	getHandler  func(path string, query map[string]string) ([]byte, error)
}

func (f *fakeSignedClient) Post(_ context.Context, path string, body any) ([]byte, error) {
	f.postPath = path
	if payload, ok := body.(map[string]any); ok {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		f.postBody = encoded
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		f.postBody = encoded
	}
	return f.postResp, nil
}

func (f *fakeSignedClient) Delete(_ context.Context, path string, query url.Values) ([]byte, error) {
	f.deletePath = path
	f.deleteQuery = map[string]string{}
	for key := range query {
		f.deleteQuery[key] = query.Get(key)
	}
	return []byte(`{}`), nil
}

func (f *fakeSignedClient) Get(_ context.Context, path string, query url.Values, _ bool) ([]byte, error) {
	f.getPath = path
	q := map[string]string{}
	for key := range query {
		q[key] = query.Get(key)
	}
	f.getQueries = append(f.getQueries, q)
	if f.getHandler != nil {
		return f.getHandler(path, q)
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getResp, nil
}

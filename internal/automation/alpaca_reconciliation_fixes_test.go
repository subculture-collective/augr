package automation

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	alpacaexec "github.com/PatrickFanella/get-rich-quick/internal/execution/alpaca"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestAlpacaClientAdapterListOrdersPaginatesWithAfterCursor(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var afters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("direction") != "asc" || query.Get("limit") != strconv.Itoa(alpacaOrdersPageSize) {
			t.Errorf("query = %v", query)
		}
		after := query.Get("after")
		mu.Lock()
		afters = append(afters, after)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if after == "" {
			page := make([]map[string]any, 0, alpacaOrdersPageSize)
			for i := 0; i < alpacaOrdersPageSize; i++ {
				page = append(page, map[string]any{"id": "order-" + strconv.Itoa(i), "symbol": "SPY", "side": "buy", "type": "market", "qty": "1", "status": "filled", "filled_qty": "1", "filled_avg_price": "500", "submitted_at": time.Date(2026, 9, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)})
			}
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "order-last", "symbol": "SPY", "side": "buy", "type": "market", "qty": "1", "filled_qty": "0", "status": "new", "submitted_at": "2026-09-02T00:00:00Z"}})
	}))
	defer server.Close()

	client := alpacaexec.NewClient("test-key", "test-secret", true, slog.New(slog.NewTextHandler(testDiscardWriter{}, nil)))
	client.SetBaseURL(server.URL)
	orders, err := NewAlpacaClientAdapter(client).ListOrders(context.Background())
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if len(orders) != alpacaOrdersPageSize+1 {
		t.Fatalf("orders = %d, want %d across two pages", len(orders), alpacaOrdersPageSize+1)
	}
	if len(afters) != 2 || afters[0] != "" || afters[1] == "" {
		t.Fatalf("after cursors = %v, want an empty first page then a cursor", afters)
	}
}

func TestAlpacaClientAdapterListFillsBoundsLaterWalksByWatermark(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var afters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		afters = append(afters, r.URL.Query().Get("after"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"activity_type":"FILL","id":"act-1","order_id":"order-1","symbol":"SPY","side":"buy","qty":"1","price":"500","transaction_time":"2026-09-10T15:00:00Z","order_status":"filled","type":"fill"}]`))
	}))
	defer server.Close()

	client := alpacaexec.NewClient("test-key", "test-secret", true, slog.New(slog.NewTextHandler(testDiscardWriter{}, nil)))
	client.SetBaseURL(server.URL)
	adapter := NewAlpacaClientAdapter(client)
	for i := 0; i < 2; i++ {
		if _, err := adapter.ListFills(context.Background()); err != nil {
			t.Fatalf("ListFills() #%d error = %v", i, err)
		}
	}
	if len(afters) != 2 || afters[0] != "" {
		t.Fatalf("after params = %v, want a full first walk", afters)
	}
	want := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC).Add(-alpacaFillLookbackSlack)
	got, err := time.Parse(time.RFC3339Nano, afters[1])
	if err != nil || !got.Equal(want) {
		t.Fatalf("second walk after = %q (%v), want %s", afters[1], err, want.Format(time.RFC3339Nano))
	}
}

func TestRealizedPnLFromClosingFills(t *testing.T) {
	t.Parallel()
	opened := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	position := domain.Position{Ticker: "SPY", Side: domain.PositionSideLong, Quantity: 10, AvgEntry: 100, OpenedAt: opened}
	fills := []BrokerFillSnapshot{
		{Ticker: "SPY", Side: domain.OrderSideBuy, Quantity: 10, Price: 100, ExecutedAt: opened},
		{Ticker: "SPY", Side: domain.OrderSideSell, Quantity: 4, Price: 110, Fee: 1, ExecutedAt: opened.Add(time.Hour)},
		{Ticker: "SPY", Side: domain.OrderSideSell, Quantity: 6, Price: 90, ExecutedAt: opened.Add(2 * time.Hour)},
		{Ticker: "QQQ", Side: domain.OrderSideSell, Quantity: 6, Price: 900, ExecutedAt: opened.Add(2 * time.Hour)},
		{Ticker: "SPY", Side: domain.OrderSideSell, Quantity: 6, Price: 50, ExecutedAt: opened.Add(-time.Hour)},
	}
	realized, matched, ok := realizedPnLFromClosingFills(position, fills)
	if !ok || matched != 10 || realized != (4*10-1)+(6*-10) {
		t.Fatalf("realized=%v matched=%v ok=%v", realized, matched, ok)
	}
	if _, _, ok := realizedPnLFromClosingFills(position, fills[:1]); ok {
		t.Fatal("no closing fills must report false so the caller falls back")
	}
}

type transitionRecordingOrderRepo struct {
	statuses []domain.OrderStatus
}

func (r *transitionRecordingOrderRepo) Create(context.Context, *domain.Order) error { return nil }
func (r *transitionRecordingOrderRepo) List(context.Context, repository.OrderFilter, int, int) ([]domain.Order, error) {
	return nil, nil
}

func (r *transitionRecordingOrderRepo) Update(_ context.Context, order *domain.Order) error {
	r.statuses = append(r.statuses, order.Status)
	return nil
}

func TestUpdateOrderThroughTransitionsWritesIntermediateHops(t *testing.T) {
	t.Parallel()
	repo := &transitionRecordingOrderRepo{}
	reconciler := &AlpacaReconciler{orderRepo: repo, logger: slog.New(slog.NewTextHandler(testDiscardWriter{}, nil))}

	order := &domain.Order{ID: uuid.New(), Status: domain.OrderStatusFilled}
	if err := reconciler.updateOrderThroughTransitions(context.Background(), order, domain.OrderStatusPending); err != nil {
		t.Fatal(err)
	}
	// pending -> filled is a legal immediate-fill edge, so no intermediate hop is written.
	if len(repo.statuses) != 1 || repo.statuses[0] != domain.OrderStatusFilled {
		t.Fatalf("pending->filled wrote %v, want a single filled write", repo.statuses)
	}

	repo.statuses = nil
	order = &domain.Order{ID: uuid.New(), Status: domain.OrderStatusPending}
	if err := reconciler.updateOrderThroughTransitions(context.Background(), order, domain.OrderStatusSubmitted); err != nil {
		t.Fatal(err)
	}
	if len(repo.statuses) != 1 || repo.statuses[0] != domain.OrderStatusSubmitted || order.Status != domain.OrderStatusSubmitted {
		t.Fatalf("submitted->pending wrote %v, want submitted retained", repo.statuses)
	}

	repo.statuses = nil
	order = &domain.Order{ID: uuid.New(), Status: domain.OrderStatusCancelled}
	if err := reconciler.updateOrderThroughTransitions(context.Background(), order, domain.OrderStatusSubmitted); err != nil {
		t.Fatal(err)
	}
	if len(repo.statuses) != 1 || repo.statuses[0] != domain.OrderStatusCancelled {
		t.Fatalf("submitted->cancelled wrote %v, want a single write", repo.statuses)
	}
}

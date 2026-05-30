package polymarket

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
)

type fakeBroker struct {
	prepareTmpl *OrderTemplate
	sendCalls   atomic.Int32
	lastTmpl    *OrderTemplate
	mu          sync.Mutex
}

func (f *fakeBroker) PrepareTemplate(order *domain.Order) (*OrderTemplate, error) {
	if f.prepareTmpl != nil {
		return f.prepareTmpl.Clone(), nil
	}
	return NewOrderTemplate([]byte(strings.Repeat("a", 32)), "POST", "https://example.com/v1/orders", []byte(`{}`))
}

func (f *fakeBroker) SendTemplate(ctx context.Context, tmpl *OrderTemplate) (*createOrderResponse, error) {
	f.sendCalls.Add(1)
	f.mu.Lock()
	f.lastTmpl = tmpl
	f.mu.Unlock()
	return &createOrderResponse{ID: "ok"}, nil
}

func TestStopGuard_LongStopBelowFires(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45}); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Price: 0.46, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire, got %d", got)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Price: 0.44, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("expected one fire, got %d", got)
	}
}

func TestStopGuard_ShortStopAboveFires(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(Position{ID: "1", Slug: "slug-a", Side: "SELL", EntryPx: 0.50, Size: 1, StopPx: 0.55}); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Price: 0.54, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire, got %d", got)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Price: 0.56, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("expected one fire, got %d", got)
	}
}

func TestStopGuard_FiresExactlyOnceUnderConcurrentTicks(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Price: 0.44, ReceivedAt: time.Now()})
		}()
	}
	wg.Wait()
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("expected exactly one fire, got %d", got)
	}
}

func TestStopGuard_CancelPreventsFire(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45}); err != nil {
		t.Fatal(err)
	}
	g.Cancel("1")
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Price: 0.44, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire after cancel, got %d", got)
	}
}

func TestStopGuard_OtherSlugIgnored(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45}); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-b", Price: 0.44, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire on other slug, got %d", got)
	}
}

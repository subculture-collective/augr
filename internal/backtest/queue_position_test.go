package backtest

import (
	"sync"
	"testing"
	"time"

	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
)

func TestQueueTracker_RegisterPassiveCapturesAhead(t *testing.T) {
	t.Parallel()
	q := NewQueueTracker()
	if err := q.RegisterPassive(QueueEntry{OrderID: "1", Slug: "mkt", Side: FillBuy, Price: 0.50, Size: 10}, marketdata.BookSnapshot{Bids: []marketdata.Level{{Price: 0.50, Size: 250}}}); err != nil {
		t.Fatalf("RegisterPassive() error = %v", err)
	}
	got, ok := q.Get("1")
	if !ok || got.AheadVolume != 250 {
		t.Fatalf("got %+v, ok=%v", got, ok)
	}
}

func TestQueueTracker_RegisterPassiveLevelMissing_IsZero(t *testing.T) {
	t.Parallel()
	q := NewQueueTracker()
	if err := q.RegisterPassive(QueueEntry{OrderID: "1", Slug: "mkt", Side: FillBuy, Price: 0.50, Size: 10}, marketdata.BookSnapshot{Bids: []marketdata.Level{{Price: 0.49, Size: 100}}}); err != nil {
		t.Fatalf("RegisterPassive() error = %v", err)
	}
	got, _ := q.Get("1")
	if got.AheadVolume != 0 {
		t.Fatalf("AheadVolume = %v, want 0", got.AheadVolume)
	}
}

func TestQueueTracker_ApplyTradeDrainsThenFills(t *testing.T) {
	t.Parallel()
	q := NewQueueTracker()
	if err := q.RegisterPassive(QueueEntry{OrderID: "1", Slug: "mkt", Side: FillBuy, Price: 0.50, Size: 10}, marketdata.BookSnapshot{Bids: []marketdata.Level{{Price: 0.50, Size: 100}}}); err != nil {
		t.Fatalf("RegisterPassive() error = %v", err)
	}
	q.ApplyTrade("mkt", 0.50, 60, time.Now())
	got, _ := q.Get("1")
	if got.AheadVolume != 40 || got.RemainingSize != 10 || got.Filled {
		t.Fatalf("after first trade got %+v", got)
	}
	q.ApplyTrade("mkt", 0.50, 50, time.Now())
	got, _ = q.Get("1")
	if got.AheadVolume != 0 || got.RemainingSize != 0 || !got.Filled || got.FilledAt.IsZero() {
		t.Fatalf("after second trade got %+v", got)
	}
	q.ApplyTrade("mkt", 0.50, 50, time.Now())
	got, _ = q.Get("1")
	if got.RemainingSize != 0 || !got.Filled {
		t.Fatalf("after third trade got %+v", got)
	}
}

func TestQueueTracker_ApplyTradeWrongPriceNoEffect(t *testing.T) {
	t.Parallel()
	q := NewQueueTracker()
	if err := q.RegisterPassive(QueueEntry{OrderID: "1", Slug: "mkt", Side: FillBuy, Price: 0.50, Size: 10}, marketdata.BookSnapshot{Bids: []marketdata.Level{{Price: 0.50, Size: 100}}}); err != nil {
		t.Fatalf("RegisterPassive() error = %v", err)
	}
	q.ApplyTrade("mkt", 0.49, 60, time.Now())
	got, _ := q.Get("1")
	if got.AheadVolume != 100 || got.Filled {
		t.Fatalf("got %+v", got)
	}
}

func TestQueueTracker_ConcurrentApplyTradeSafe(t *testing.T) {
	t.Parallel()
	q := NewQueueTracker()
	if err := q.RegisterPassive(QueueEntry{OrderID: "1", Slug: "mkt", Side: FillBuy, Price: 0.50, Size: 10}, marketdata.BookSnapshot{Bids: []marketdata.Level{{Price: 0.50, Size: 100}}}); err != nil {
		t.Fatalf("RegisterPassive() error = %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				q.ApplyTrade("mkt", 0.50, 1, time.Now())
			}
		}()
	}
	wg.Wait()
	got, ok := q.Get("1")
	if !ok || !got.Filled {
		t.Fatalf("expected filled entry, got %+v ok=%v", got, ok)
	}
}

func TestQueueTracker_ApplyTradeFIFOAcrossEntries(t *testing.T) {
	t.Parallel()
	q := NewQueueTracker()
	book := marketdata.BookSnapshot{Bids: []marketdata.Level{{Price: 0.50, Size: 0}}}
	if err := q.RegisterPassive(QueueEntry{OrderID: "1", Slug: "mkt", Side: FillBuy, Price: 0.50, Size: 5}, book); err != nil {
		t.Fatalf("RegisterPassive() error = %v", err)
	}
	if err := q.RegisterPassive(QueueEntry{OrderID: "2", Slug: "mkt", Side: FillBuy, Price: 0.50, Size: 5}, book); err != nil {
		t.Fatalf("RegisterPassive() error = %v", err)
	}
	ts := time.Now()
	q.ApplyTrade("mkt", 0.50, 12, ts)
	first, _ := q.Get("1")
	second, _ := q.Get("2")
	if !first.Filled || !second.Filled || first.FilledAt.After(second.FilledAt) {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if first.RemainingSize != 0 || second.RemainingSize != 0 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if q.ActiveCount() != 0 {
		t.Fatalf("ActiveCount() = %d, want 0", q.ActiveCount())
	}
}

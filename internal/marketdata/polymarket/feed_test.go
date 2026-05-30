package polymarket

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFeedLifecycleAndRouting(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WSURL = "ws://example.invalid"
	cfg.ConnectionsPerFeed = 1
	cfg.PruneInterval = 0
	cfg.WarmupDuration = 0
	cfg.WarmupMinClean = 1
	f, err := newFeedWithFactory(cfg, func(id int, cfg Config, ticks chan<- Tick, books chan<- BookSnapshot, dropped *atomic.Uint64) poolConnection {
		return &fakePoolConn{id: id, ticks: ticks, books: books, period: 5 * time.Millisecond, done: make(chan struct{})}
	})
	if err != nil {
		t.Fatalf("NewFeed() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := f.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ch := f.Ticks("slug-a")
	f.cleaner.handleTick(Tick{Slug: "slug-a", Price: 1.23, Side: "yes", Size: 10, ReceivedAt: time.Now().UTC()})
	f.cleaner.handleTick(Tick{Slug: "slug-a", Price: 1.24, Side: "yes", Size: 10, ReceivedAt: time.Now().UTC().Add(time.Millisecond)})
	select {
	case tk := <-ch:
		if tk.Slug != "slug-a" {
			t.Fatalf("tick slug = %q", tk.Slug)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for tick")
	}
	if !f.Ready("slug-a") && f.Stats().Pool.Members == 0 {
		t.Fatal("expected feed to be started")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.Close()
	}()
	wg.Wait()
}

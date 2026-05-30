package polymarket

import (
	"context"
	"sync"
)

type Feed struct {
	pool    *Pool
	cleaner *Cleaner

	mu       sync.Mutex
	tickSubs map[string]chan Tick
	bookSubs map[string]chan BookSnapshot
	closed   bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

type FeedStats struct {
	Pool        PoolStats
	Subscribers int
}

func NewFeed(cfg Config) (*Feed, error) {
	return newFeedWithFactory(cfg, nil)
}

func newFeedWithFactory(cfg Config, factory connFactory) (*Feed, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	pool := newPoolWithFactory(cfg, factory)
	return &Feed{
		pool:     pool,
		cleaner:  NewCleaner(cfg, pool.Ticks()),
		tickSubs: make(map[string]chan Tick),
		bookSubs: make(map[string]chan BookSnapshot),
	}, nil
}

func (f *Feed) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	f.mu.Lock()
	f.cancel = cancel
	f.mu.Unlock()
	if err := f.pool.Start(ctx); err != nil {
		cancel()
		return err
	}
	f.cleaner.Start(ctx)
	f.wg.Add(2)
	go f.dispatchTicks()
	go f.dispatchBooks()
	return nil
}

func (f *Feed) dispatchTicks() {
	defer f.wg.Done()
	for tk := range f.cleaner.Out() {
		f.mu.Lock()
		ch := f.tickSubs[tk.Slug]
		f.mu.Unlock()
		if ch == nil {
			continue
		}
		select {
		case ch <- tk:
		default:
		}
	}
}

func (f *Feed) dispatchBooks() {
	defer f.wg.Done()
	for bs := range f.pool.Books() {
		f.mu.Lock()
		ch := f.bookSubs[bs.Slug]
		f.mu.Unlock()
		if ch == nil {
			continue
		}
		select {
		case ch <- bs:
		default:
		}
	}
}

func (f *Feed) Ticks(slug string) <-chan Tick {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch := f.tickSubs[slug]; ch != nil {
		return ch
	}
	ch := make(chan Tick, 64)
	f.tickSubs[slug] = ch
	return ch
}

func (f *Feed) Books(slug string) <-chan BookSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch := f.bookSubs[slug]; ch != nil {
		return ch
	}
	ch := make(chan BookSnapshot, 64)
	f.bookSubs[slug] = ch
	return ch
}

func (f *Feed) Ready(slug string) bool { return f.cleaner.Ready(slug) }

func (f *Feed) Stats() FeedStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return FeedStats{Pool: f.pool.Stats(), Subscribers: len(f.tickSubs) + len(f.bookSubs)}
}

func (f *Feed) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	cancel := f.cancel
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	f.cleaner.Close()
	f.pool.Close()
	f.wg.Wait()
}

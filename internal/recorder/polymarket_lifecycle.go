package recorder

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const defaultPolymarketLifecycleBatchSize = 5000

const defaultPolymarketLifecycleFlushInterval = 500 * time.Millisecond

type polymarketLifecycle struct {
	ticks *polymarketBatcher[domain.PolymarketTick]
	books *polymarketBatcher[domain.PolymarketBookSnapshot]
}

func newPolymarketLifecycle(repo repository.PolymarketMarketDataRepository, cfg RecorderConfig, metrics RecorderMetrics) *polymarketLifecycle {
	return &polymarketLifecycle{
		ticks: newPolymarketBatcher(
			"tick",
			repo.InsertTicks,
			cfg.BatchSize,
			cfg.FlushInterval,
			func(t domain.PolymarketTick) time.Time { return t.ReceivedAt },
			metrics,
		),
		books: newPolymarketBatcher(
			"book",
			repo.InsertBookSnapshots,
			cfg.BatchSize,
			cfg.FlushInterval,
			func(b domain.PolymarketBookSnapshot) time.Time { return b.ReceivedAt },
			metrics,
		),
	}
}

func (l *polymarketLifecycle) Start() {
	l.ticks.Start()
	l.books.Start()
}

func (l *polymarketLifecycle) Close() {
	l.ticks.Close()
	l.books.Close()
}

func (l *polymarketLifecycle) SubmitTick(t domain.PolymarketTick) bool {
	return l.ticks.Submit(t)
}

func (l *polymarketLifecycle) SubmitBook(b domain.PolymarketBookSnapshot) bool {
	return l.books.Submit(b)
}

type polymarketBatcher[T any] struct {
	kind          string
	insert        func(context.Context, []T) error
	receivedAt    func(T) time.Time
	metrics       RecorderMetrics
	batchSize     int
	flushInterval time.Duration
	in            chan T
	done          chan struct{}
	once          sync.Once
	closed        atomic.Bool
	wg            sync.WaitGroup
	mu            sync.Mutex
	buf           []T
}

func newPolymarketBatcher[T any](kind string, insert func(context.Context, []T) error, batchSize int, flushInterval time.Duration, receivedAt func(T) time.Time, metrics RecorderMetrics) *polymarketBatcher[T] {
	if batchSize <= 0 {
		batchSize = defaultPolymarketLifecycleBatchSize
	}
	if flushInterval <= 0 {
		flushInterval = defaultPolymarketLifecycleFlushInterval
	}
	if metrics == nil {
		metrics = noopRecorderMetrics{}
	}
	return &polymarketBatcher[T]{
		kind:          kind,
		insert:        insert,
		receivedAt:    receivedAt,
		metrics:       metrics,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		in:            make(chan T, batchSize),
		done:          make(chan struct{}),
	}
}

func (b *polymarketBatcher[T]) Start() {
	b.wg.Add(1)
	go b.run()
}

func (b *polymarketBatcher[T]) Close() {
	b.once.Do(func() {
		b.closed.Store(true)
		close(b.done)
		b.wg.Wait()
	})
}

func (b *polymarketBatcher[T]) Submit(v T) bool {
	if b.closed.Load() {
		b.metrics.IncDropped(b.kind, 1)
		return false
	}
	select {
	case <-b.done:
		b.metrics.IncDropped(b.kind, 1)
		return false
	default:
	}
	select {
	case <-b.done:
		b.metrics.IncDropped(b.kind, 1)
		return false
	case b.in <- v:
		return true
	default:
		b.metrics.IncDropped(b.kind, 1)
		return false
	}
}

func (b *polymarketBatcher[T]) run() {
	defer b.wg.Done()
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case v := <-b.in:
			b.append(v)
		case <-ticker.C:
			b.flush()
		case <-b.done:
			b.drain()
			b.flush()
			return
		}
	}
}

func (b *polymarketBatcher[T]) append(v T) {
	b.mu.Lock()
	b.buf = append(b.buf, v)
	flushNow := len(b.buf) >= b.batchSize
	b.mu.Unlock()
	if flushNow {
		b.flush()
	}
}

func (b *polymarketBatcher[T]) drain() {
	for {
		select {
		case v := <-b.in:
			b.mu.Lock()
			b.buf = append(b.buf, v)
			b.mu.Unlock()
		default:
			return
		}
	}
}

func (b *polymarketBatcher[T]) flush() {
	batch := b.takeBatch()
	if len(batch) == 0 {
		return
	}
	_ = b.insert(context.Background(), batch)
	b.metrics.IncInserted(b.kind, len(batch))
	b.metrics.ObserveLagSeconds(b.kind, batchLagSeconds(batch, b.receivedAt))
}

func (b *polymarketBatcher[T]) takeBatch() []T {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) == 0 {
		return nil
	}
	batch := append([]T(nil), b.buf...)
	b.buf = nil
	return batch
}

func batchLagSeconds[T any](batch []T, receivedAt func(T) time.Time) float64 {
	if len(batch) == 0 {
		return 0
	}
	oldest := time.Now().UTC()
	for _, v := range batch {
		ts := receivedAt(v)
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		if ts.Before(oldest) {
			oldest = ts
		}
	}
	return time.Since(oldest).Seconds()
}

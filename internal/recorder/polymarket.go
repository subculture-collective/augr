package recorder

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	polymarket "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type RecorderConfig struct {
	BatchSize     int
	FlushInterval time.Duration
	Slugs         []string
}

type RecorderMetrics interface {
	IncInserted(kind string, n int)
	ObserveLagSeconds(kind string, sec float64)
	IncDropped(kind string, n int)
}

type noopRecorderMetrics struct{}

func (noopRecorderMetrics) IncInserted(string, int)           {}
func (noopRecorderMetrics) ObserveLagSeconds(string, float64) {}
func (noopRecorderMetrics) IncDropped(string, int)            {}

type Recorder struct {
	feed    polymarketFeed
	repo    repository.PolymarketMarketDataRepository
	cfg     RecorderConfig
	log     *slog.Logger
	metrics RecorderMetrics
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	tickBuf []domain.PolymarketTick
	bookBuf []domain.PolymarketBookSnapshot
}

type polymarketFeed interface {
	Ticks(slug string) <-chan polymarket.Tick
	Books(slug string) <-chan polymarket.BookSnapshot
}

func New(feed polymarketFeed, repo repository.PolymarketMarketDataRepository, cfg RecorderConfig, log *slog.Logger, metrics RecorderMetrics) *Recorder {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 5000
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 500 * time.Millisecond
	}
	if metrics == nil {
		metrics = noopRecorderMetrics{}
	}
	return &Recorder{feed: feed, repo: repo, cfg: cfg, log: log, metrics: metrics}
}

func (r *Recorder) Start(ctx context.Context) {
	r.ctx, r.cancel = context.WithCancel(ctx)
	for _, slug := range r.cfg.Slugs {
		r.wg.Add(2)
		go r.runTicks(slug)
		go r.runBooks(slug)
	}
}

func (r *Recorder) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	r.flushAll()
}

func (r *Recorder) runTicks(slug string) {
	defer r.wg.Done()
	ch := r.feed.Ticks(slug)
	t := time.NewTicker(r.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case tk, ok := <-ch:
			if !ok {
				return
			}
			r.addTick(domain.PolymarketTick{Slug: tk.Slug, Side: tk.Side, Price: tk.Price, Size: tk.Size, ReceivedAt: tk.ReceivedAt, SeqHint: int64(tk.SeqHint), ConnID: tk.ConnID})
		case <-t.C:
			r.flushTicks()
		}
	}
}

func (r *Recorder) runBooks(slug string) {
	defer r.wg.Done()
	ch := r.feed.Books(slug)
	t := time.NewTicker(r.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case bs, ok := <-ch:
			if !ok {
				return
			}
			r.addBook(domain.PolymarketBookSnapshot{Slug: bs.Slug, BestBid: bs.BestBid, BestAsk: bs.BestAsk, Bids: convertLevels(bs.Bids), Asks: convertLevels(bs.Asks), ReceivedAt: bs.ReceivedAt, ConnID: bs.ConnID})
		case <-t.C:
			r.flushBooks()
		}
	}
}

func convertLevels(in []polymarket.Level) []domain.PolymarketBookLevel {
	out := make([]domain.PolymarketBookLevel, len(in))
	for i, v := range in {
		out[i] = domain.PolymarketBookLevel{Price: v.Price, Size: v.Size}
	}
	return out
}

func (r *Recorder) addTick(t domain.PolymarketTick) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tickBuf) >= r.cfg.BatchSize {
		r.metrics.IncDropped("tick", 1)
		return
	}
	r.tickBuf = append(r.tickBuf, t)
	if len(r.tickBuf) >= r.cfg.BatchSize {
		r.flushTicksUnlocked()
	}
}

func (r *Recorder) addBook(b domain.PolymarketBookSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bookBuf) >= r.cfg.BatchSize {
		r.metrics.IncDropped("book", 1)
		return
	}
	r.bookBuf = append(r.bookBuf, b)
	if len(r.bookBuf) >= r.cfg.BatchSize {
		r.flushBooksUnlocked()
	}
}

func (r *Recorder) flushAll() { r.flushTicks(); r.flushBooks() }

func (r *Recorder) flushTicks() {
	r.mu.Lock()
	r.flushTicksUnlocked()
	r.mu.Unlock()
}

func (r *Recorder) flushBooks() {
	r.mu.Lock()
	r.flushBooksUnlocked()
	r.mu.Unlock()
}

func (r *Recorder) flushTicksUnlocked() {
	batch := append([]domain.PolymarketTick(nil), r.tickBuf...)
	r.tickBuf = nil
	if len(batch) == 0 {
		return
	}
	r.mu.Unlock()
	_ = r.repo.InsertTicks(context.Background(), batch)
	r.metrics.IncInserted("tick", len(batch))
	r.mu.Lock()
}

func (r *Recorder) flushBooksUnlocked() {
	batch := append([]domain.PolymarketBookSnapshot(nil), r.bookBuf...)
	r.bookBuf = nil
	if len(batch) == 0 {
		return
	}
	r.mu.Unlock()
	_ = r.repo.InsertBookSnapshots(context.Background(), batch)
	r.metrics.IncInserted("book", len(batch))
	r.mu.Lock()
}

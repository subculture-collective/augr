package evladder

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	polymarketexec "github.com/PatrickFanella/get-rich-quick/internal/execution/polymarket"
	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
	"github.com/google/uuid"
)

type RunnerConfig struct {
	StrategyID             string
	Slug                   string
	BaseSize               float64
	CancelThresholdDollars float64
	PollInterval           time.Duration
}

type ProbabilityProvider interface {
	MarketProbability(ctx context.Context, slug string) (float64, error)
}

type brokerSender interface {
	PrepareTemplate(req *domain.Order) (*polymarketexec.OrderTemplate, error)
	SendTemplate(ctx context.Context, tpl *polymarketexec.OrderTemplate) (any, error)
}

type activeRung struct {
	Template *polymarketexec.OrderTemplate
	Price    float64
}

type Runner struct {
	cfg         RunnerConfig
	broker      brokerSender
	breaker     risk.Breaker
	ticks       <-chan marketdata.Tick
	books       <-chan marketdata.BookSnapshot
	probSource  ProbabilityProvider
	mu          sync.Mutex
	active      map[string]*activeRung
	lastBookMid float64
	logger      *slog.Logger
}

func NewRunner(cfg RunnerConfig, broker brokerSender, breaker risk.Breaker, probSource ProbabilityProvider, ticks <-chan marketdata.Tick, books <-chan marketdata.BookSnapshot, logger *slog.Logger) *Runner {
	if cfg.CancelThresholdDollars == 0 {
		cfg.CancelThresholdDollars = 0.03
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{cfg: cfg, broker: broker, breaker: breaker, ticks: ticks, books: books, probSource: probSource, active: map[string]*activeRung{}, logger: logger}
}

func (r *Runner) ActiveCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.active) }

func (r *Runner) Run(ctx context.Context) error {
	if _, err := uuid.Parse(r.cfg.StrategyID); err != nil {
		return err
	}
	if r.broker == nil || r.probSource == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	r.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b := <-r.books:
			if b.BestBid > 0 && b.BestAsk > 0 {
				r.handleMid(b)
			}
		case <-ticker.C:
			r.poll(ctx)
		}
	}
}

func (r *Runner) handleMid(b marketdata.BookSnapshot) {
	mid := (b.BestBid + b.BestAsk) / 2
	r.mu.Lock()
	prev := r.lastBookMid
	r.lastBookMid = mid
	if prev != 0 && math.Abs(mid-prev) > r.cfg.CancelThresholdDollars {
		for k, a := range r.active {
			if math.Abs(a.Price-mid) >= r.cfg.CancelThresholdDollars {
				r.logger.Info("would-cancel", "key", k, "price", a.Price, "mid", mid)
				delete(r.active, k)
			}
		}
	}
	r.mu.Unlock()
}

func (r *Runner) poll(ctx context.Context) {
	p, err := r.probSource.MarketProbability(ctx, r.cfg.Slug)
	if err != nil {
		r.logger.Warn("probability lookup failed", "err", err)
		return
	}
	for _, rung := range Compute(DefaultBuckets(), p, r.cfg.BaseSize) {
		key := fmt.Sprintf("BUY|%.2f", rung.Price)
		r.mu.Lock()
		_, ok := r.active[key]
		r.mu.Unlock()
		if ok {
			continue
		}
		if r.breaker != nil {
			if err := r.breaker.Allow(ctx, domain.RiskBreakerScopeGlobal); err != nil {
				continue
			}
			if err := r.breaker.Allow(ctx, domain.RiskBreakerScopeStrategy(r.cfg.StrategyID)); err != nil {
				continue
			}
		}
		price := rung.Price
		size := rung.Size
		order := &domain.Order{Ticker: r.cfg.Slug, Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: size, LimitPrice: &price}
		u, _ := uuid.Parse(r.cfg.StrategyID)
		order.StrategyID = &u
		tpl, err := r.broker.PrepareTemplate(order)
		if err != nil {
			r.logger.Warn("prepare failed", "err", err)
			continue
		}
		if _, err := r.broker.SendTemplate(ctx, tpl); err != nil {
			r.logger.Warn("send failed", "err", err)
			continue
		}
		r.mu.Lock()
		r.active[key] = &activeRung{Template: tpl, Price: rung.Price}
		r.mu.Unlock()
	}
}

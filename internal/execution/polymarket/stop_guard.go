package polymarket

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
)

type StopGuardConfig struct {
	Broker  templateSender
	Logger  *slog.Logger
	Metrics StopGuardMetrics
}

type StopGuardMetrics interface {
	IncTriggered(slug string)
	IncSendError(slug string)
	ObserveTickToFireSeconds(slug string, seconds float64)
}

type Position struct {
	ID      string
	Slug    string
	Side    string
	EntryPx float64
	Size    float64
	StopPx  float64
}

type templateSender interface {
	PrepareTemplate(order *domain.Order) (*OrderTemplate, error)
	SendTemplate(ctx context.Context, tmpl *OrderTemplate) (*createOrderResponse, error)
}

type guardState int32

const (
	guardArmed guardState = iota
	guardFiring
	guardFired
)

type guardEntry struct {
	positionID string
	slug       string
	stopPx     float64
	long       bool
	template   *OrderTemplate
	state      atomic.Int32
	receivedAt time.Time
}

type StopGuard struct {
	broker  templateSender
	logger  *slog.Logger
	metrics StopGuardMetrics

	mu     sync.RWMutex
	bySlug map[string][]*guardEntry
	byID   map[string]*guardEntry
	count  atomic.Int32
}

func NewStopGuard(cfg StopGuardConfig) (*StopGuard, error) {
	if cfg.Broker == nil {
		return nil, errors.New("polymarket: stop guard broker is required")
	}
	return &StopGuard{
		broker:  cfg.Broker,
		logger:  cfg.Logger,
		metrics: cfg.Metrics,
		bySlug:  make(map[string][]*guardEntry),
		byID:    make(map[string]*guardEntry),
	}, nil
}

func (g *StopGuard) RegisterEntry(pos Position) error {
	if g == nil {
		return errors.New("polymarket: stop guard is nil")
	}
	positionID := strings.TrimSpace(pos.ID)
	if positionID == "" {
		return errors.New("polymarket: position id is required")
	}
	slug := strings.TrimSpace(pos.Slug)
	if slug == "" {
		return errors.New("polymarket: slug is required")
	}
	if pos.Size <= 0 {
		return errors.New("polymarket: size must be greater than zero")
	}
	if pos.StopPx <= 0 {
		return errors.New("polymarket: stop price must be greater than zero")
	}
	long := strings.EqualFold(strings.TrimSpace(pos.Side), "BUY")
	short := strings.EqualFold(strings.TrimSpace(pos.Side), "SELL")
	if !long && !short {
		return fmt.Errorf("polymarket: unsupported side %q", pos.Side)
	}
	intent := "ORDER_INTENT_SELL_LONG"
	if short {
		intent = "ORDER_INTENT_BUY_SHORT"
	}
	side := "BUY"
	if long {
		side = "SELL"
	}
	order := &domain.Order{Ticker: slug, Side: domain.OrderSide(side), OrderType: domain.OrderTypeMarket, Quantity: pos.Size, PolymarketIntent: intent}
	tmpl, err := g.broker.PrepareTemplate(order)
	if err != nil {
		return err
	}
	entry := &guardEntry{positionID: positionID, slug: slug, stopPx: pos.StopPx, long: long, template: tmpl, receivedAt: time.Now()}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.byID[positionID]; exists {
		return fmt.Errorf("polymarket: position %q already registered", positionID)
	}
	g.byID[positionID] = entry
	g.bySlug[slug] = append(g.bySlug[slug], entry)
	g.count.Add(1)
	return nil
}

func (g *StopGuard) Cancel(positionID string) {
	if g == nil {
		return
	}
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.byID[positionID]
	if !ok {
		return
	}
	delete(g.byID, positionID)
	entries := g.bySlug[entry.slug]
	for i := range entries {
		if entries[i] == entry {
			entries[i] = entries[len(entries)-1]
			entries = entries[:len(entries)-1]
			break
		}
	}
	if len(entries) == 0 {
		delete(g.bySlug, entry.slug)
	} else {
		g.bySlug[entry.slug] = entries
	}
	g.count.Add(-1)
}

func (g *StopGuard) Active() int {
	if g == nil {
		return 0
	}
	return int(g.count.Load())
}

func (g *StopGuard) OnTick(ctx context.Context, t marketdata.Tick) {
	if g == nil {
		return
	}
	g.mu.RLock()
	entries := g.bySlug[t.Slug]
	g.mu.RUnlock()
	for _, entry := range entries {
		if entry == nil || !entry.state.CompareAndSwap(int32(guardArmed), int32(guardFiring)) {
			continue
		}
		crossed := (entry.long && t.Price <= entry.stopPx) || (!entry.long && t.Price >= entry.stopPx)
		if !crossed {
			entry.state.Store(int32(guardArmed))
			continue
		}
		if g.metrics != nil {
			g.metrics.IncTriggered(entry.slug)
		}
		if g.metrics != nil {
			g.metrics.ObserveTickToFireSeconds(entry.slug, time.Since(t.ReceivedAt).Seconds())
		}
		_, err := g.broker.SendTemplate(ctx, entry.template)
		if err != nil {
			if g.metrics != nil {
				g.metrics.IncSendError(entry.slug)
			}
			if g.logger != nil {
				g.logger.Error("polymarket stop guard send failed", "slug", entry.slug, "position_id", entry.positionID, "err", err)
			}
		}
		entry.state.Store(int32(guardFired))
		g.Cancel(entry.positionID)
	}
}

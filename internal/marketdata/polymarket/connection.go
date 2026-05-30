package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type connection struct {
	id               int
	wsURL            string
	slugs            []string
	dropFirst        bool
	logger           *slog.Logger
	ticks            chan<- Tick
	books            chan<- BookSnapshot
	dropped          *atomic.Uint64
	conn             *websocket.Conn
	ctx              context.Context
	cancel           context.CancelFunc
	firstTickDropped bool
}

func newConnection(id int, cfg Config, ticks chan<- Tick, books chan<- BookSnapshot, dropped *atomic.Uint64) *connection {
	ctx, cancel := context.WithCancel(context.Background())
	return &connection{
		id:        id,
		wsURL:     cfg.WSURL,
		slugs:     append([]string(nil), cfg.PerMarketSlugs...),
		dropFirst: cfg.DropFirstTickPerConn,
		logger:    cfg.Logger,
		ticks:     ticks,
		books:     books,
		dropped:   dropped,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (c *connection) log() *slog.Logger {
	if c.logger != nil {
		return c.logger
	}
	return slog.Default()
}

func (c *connection) Dial(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

func (c *connection) Run(ctx context.Context) error {
	if c.conn == nil {
		if err := c.Dial(ctx); err != nil {
			return err
		}
	}
	defer c.Close()

	sub := map[string]any{"type": "subscribe", "markets": c.slugs}
	if err := c.conn.WriteJSON(sub); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return err
		}
		c.handleMessage(msg)
	}
}

func (c *connection) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type wireMessage struct {
	Event     string     `json:"event"`
	Type      string     `json:"type"`
	Market    string     `json:"market"`
	Bids      [][]string `json:"bids"`
	Asks      [][]string `json:"asks"`
	Side      string     `json:"side"`
	Price     string     `json:"price"`
	Size      string     `json:"size"`
	LastPrice string     `json:"last_price"`
}

// handleMessage decodes provisional Polymarket CLOB websocket frames.
// The shape is intentionally loose and may be refined in later phases.
func (c *connection) handleMessage(msg []byte) {
	var wm wireMessage
	if err := json.Unmarshal(msg, &wm); err != nil {
		c.log().Debug("dropping unparseable message", "conn_id", c.id, "err", err)
		return
	}

	slug := wm.Market
	if slug == "" && len(c.slugs) > 0 {
		slug = c.slugs[0]
	}
	now := time.Now().UTC()
	if len(wm.Bids) > 0 || len(wm.Asks) > 0 || wm.Event == "book" {
		bs := BookSnapshot{Slug: slug, ReceivedAt: now, ConnID: c.id}
		for _, lvl := range wm.Bids {
			if len(lvl) < 2 {
				continue
			}
			p, _ := strconv.ParseFloat(lvl[0], 64)
			s, _ := strconv.ParseFloat(lvl[1], 64)
			bs.Bids = append(bs.Bids, Level{Price: p, Size: s})
		}
		for _, lvl := range wm.Asks {
			if len(lvl) < 2 {
				continue
			}
			p, _ := strconv.ParseFloat(lvl[0], 64)
			s, _ := strconv.ParseFloat(lvl[1], 64)
			bs.Asks = append(bs.Asks, Level{Price: p, Size: s})
		}
		if len(bs.Bids) > 0 {
			bs.BestBid = bs.Bids[0].Price
		}
		if len(bs.Asks) > 0 {
			bs.BestAsk = bs.Asks[0].Price
		}
		select {
		case c.books <- bs:
		default:
			c.addDropped()
		}
	}
	if wm.Side != "" || wm.Price != "" || wm.LastPrice != "" || wm.Event == "price_change" {
		if c.dropFirst && !c.firstTickDropped {
			c.firstTickDropped = true
			return
		}
		priceStr := wm.Price
		if priceStr == "" {
			priceStr = wm.LastPrice
		}
		price, _ := strconv.ParseFloat(priceStr, 64)
		size, _ := strconv.ParseFloat(wm.Size, 64)
		tick := Tick{Slug: slug, Side: wm.Side, Price: price, Size: size, ReceivedAt: now, ConnID: c.id}
		select {
		case c.ticks <- tick:
		default:
			c.addDropped()
		}
	}
}

func (c *connection) addDropped() {
	if c.dropped != nil {
		c.dropped.Add(1)
	}
}

var errUnsupported = errors.New("unsupported")

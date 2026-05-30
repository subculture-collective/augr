package polymarket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConnectionDropFirstTickAndEmitBookAndTicks(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer c.Close()

		_, _, _ = c.ReadMessage()
		msgs := []any{
			map[string]any{"event": "book", "market": "slug-a", "bids": [][]string{{"0.40", "1.0"}}, "asks": [][]string{{"0.60", "2.0"}}},
			map[string]any{"event": "price_change", "market": "slug-a", "side": "BUY", "price": "0.41", "size": "3.0"},
			map[string]any{"event": "price_change", "market": "slug-a", "side": "SELL", "price": "0.42", "size": "4.0"},
		}
		for _, m := range msgs {
			b, _ := json.Marshal(m)
			if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	ticks := make(chan Tick, 4)
	books := make(chan BookSnapshot, 2)
	dropped := &atomic.Uint64{}
	c := newConnection(7, Config{WSURL: "ws" + ts.URL[4:], PerMarketSlugs: []string{"slug-a"}, DropFirstTickPerConn: true}, ticks, books, dropped)

	if err := c.Dial(context.Background()); err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.Run(context.Background()); err == nil {
		t.Fatal("expected read error after scripted server closes")
	}

	select {
	case bs := <-books:
		if bs.Slug != "slug-a" || len(bs.Bids) != 1 || len(bs.Asks) != 1 {
			t.Fatalf("bad book: %+v", bs)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for book")
	}

	var got []Tick
	for len(got) < 1 {
		select {
		case tk := <-ticks:
			got = append(got, tk)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for tick")
		}
	}
	if got[0].Price != 0.42 || got[0].Side != "SELL" {
		t.Fatalf("unexpected tick after drop-first: %+v", got[0])
	}
	if dropped.Load() != 0 {
		t.Fatalf("unexpected dropped count: %d", dropped.Load())
	}
}

func TestConnectionDropsWhenTickChannelFull(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer c.Close()
		_, _, _ = c.ReadMessage()
		b, _ := json.Marshal(map[string]any{"event": "price_change", "market": "slug-a", "side": "BUY", "price": "0.44", "size": "1.0"})
		_ = c.WriteMessage(websocket.TextMessage, b)
	}))
	defer ts.Close()

	ticks := make(chan Tick)
	books := make(chan BookSnapshot, 1)
	dropped := &atomic.Uint64{}
	c := newConnection(1, Config{WSURL: "ws" + ts.URL[4:], PerMarketSlugs: []string{"slug-a"}}, ticks, books, dropped)
	if err := c.Dial(context.Background()); err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.Run(context.Background()); err == nil {
		t.Fatal("expected error from closed server")
	}
	if dropped.Load() == 0 {
		t.Fatal("expected dropped counter to increment")
	}
}

package signal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPolygonMempool_RequiresURLs(t *testing.T) {
	src := NewPolygonMempoolSource(PolygonMempoolSourceConfig{}, nil)
	if _, err := src.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestPolygonMempool_DefaultContracts(t *testing.T) {
	contracts := DefaultPolymarketContracts()
	if len(contracts) == 0 {
		t.Fatal("expected contracts")
	}
	for _, c := range contracts {
		if c != strings.ToLower(c) {
			t.Fatalf("not lowercase: %s", c)
		}
	}
}

func TestPolygonMempool_EmitsOnWatchedAddress(t *testing.T) {
	evt := runMempoolScenario(t, "0x1234567890abcdef1234567890abcdef12345678", "0x1234567890abcdef1234567890abcdef12345678", "0xabc")
	if evt == nil {
		t.Fatal("expected event")
	}
	if evt.Metadata["signal_kind"] != "copy_trade" {
		t.Fatalf("signal_kind=%v", evt.Metadata["signal_kind"])
	}
}

func TestPolygonMempool_SkipsUnwatchedAddress(t *testing.T) {
	evt := runMempoolScenario(t, "0x1234567890abcdef1234567890abcdef12345678", "0xdeadbeef00000000000000000000000000000000", "0xabc")
	if evt != nil {
		t.Fatal("expected no event")
	}
}

func TestPolygonMempool_DedupesByHash(t *testing.T) {
	wsURL, rpcURL, sendHash := setupMempoolServers(t, "0x1234567890abcdef1234567890abcdef12345678")
	src := NewPolygonMempoolSource(PolygonMempoolSourceConfig{WSURL: wsURL, RPCURL: rpcURL, WatchAddresses: []string{"0x1234567890abcdef1234567890abcdef12345678"}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := src.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		sendHash("0xhash1")
		sendHash("0xhash1")
	}()
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case <-out:
			count++
		case <-deadline:
			if count != 1 {
				t.Fatalf("events=%d, want 1", count)
			}
			cancel()
			wg.Wait()
			return
		}
	}
}

func runMempoolScenario(t *testing.T, watchedTo, txTo, hash string) *RawSignalEvent {
	t.Helper()
	wsURL, rpcURL, sendHash := setupMempoolServers(t, txTo)
	src := NewPolygonMempoolSource(PolygonMempoolSourceConfig{WSURL: wsURL, RPCURL: rpcURL, WatchAddresses: []string{watchedTo}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := src.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var evt *RawSignalEvent
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case e := <-out:
			evt = &e
		case <-time.After(300 * time.Millisecond):
		}
	}()
	sendHash(hash)
	wg.Wait()
	return evt
}

func setupMempoolServers(t *testing.T, txTo string) (string, string, func(string)) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ready := make(chan struct{})
	var wsConn *websocket.Conn
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		wsConn = c
		var sub map[string]any
		_ = c.ReadJSON(&sub)
		_ = c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "sub-1"})
		close(ready)
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"from": "0xfrom", "to": txTo, "value": "0x10", "input": "0x1234"}})
	}))
	sendHash := func(hash string) {
		<-ready
		_ = wsConn.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "eth_subscription", "params": map[string]any{"result": hash}})
	}
	return strings.Replace(ws.URL, "http://", "ws://", 1), rpc.URL, sendHash
}

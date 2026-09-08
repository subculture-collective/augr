package api

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

func TestPublishPolymarketEventUsesSystemScope(t *testing.T) {
	t.Parallel()
	hub := NewHub(slog.Default(), uuid.New())
	go hub.Run()
	defer hub.Stop()
	client := &Client{
		hub:                  hub,
		accountID:            uuid.New(),
		send:                 make(chan []byte, sendBufferSize),
		subscribedPolymarket: true,
		subscriptions: Subscriptions{
			StrategyIDs: map[uuid.UUID]bool{},
			RunIDs:      map[uuid.UUID]bool{},
		},
	}
	hub.register <- client
	waitFor(t, func() bool { return hub.ClientCount() == 1 })
	(&Server{hub: hub}).PublishPolymarketEvent(EventPolymarketPriceMove, map[string]string{"slug": "test"})
	raw := <-client.send
	var message WSMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if message.Scope != "system" || message.AccountID != uuid.Nil {
		t.Fatalf("message scope=%q account=%s, want system without account", message.Scope, message.AccountID)
	}
}

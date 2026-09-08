package cli

import (
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestWebSocketURLBindsCanonicalAccount(t *testing.T) {
	t.Parallel()
	accountID := uuid.NewString()
	got, err := websocketURL("https://augr.example/api/v1", accountID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "wss" || parsed.Path != "/ws" || parsed.Query().Get("account_id") != accountID {
		t.Fatalf("websocket URL = %q", got)
	}
}

func TestWebSocketURLRejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()
	if _, err := websocketURL("file:///tmp/augr", uuid.NewString()); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

package opencode_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	"github.com/PatrickFanella/get-rich-quick/internal/llm/opencode"
)

func TestHTTPErrorClassification(t *testing.T) {
	t.Parallel()
	for status, want := range map[int]bool{401: false, 403: false, 404: false, 408: true, 429: true, 500: true, 502: true} {
		err := &opencode.HTTPError{Status: status, Body: "x"}
		if err.StatusCode() != status || err.Retryable() != want {
			t.Fatalf("status %d: StatusCode=%d Retryable=%t want %t", status, err.StatusCode(), err.Retryable(), want)
		}
	}
}

func TestProviderHTTPFailureCarriesStatusAndDeletesSession(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var aborted, deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = w.Write([]byte(`{"id":"s1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/s1/abort":
			aborted = true
			_, _ = w.Write([]byte(`true`))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"overloaded"}`))
		case r.Method == http.MethodDelete:
			deleted = true
			_, _ = w.Write([]byte(`true`))
		}
	}))
	defer server.Close()

	provider, err := opencode.NewProvider(opencode.Config{BaseURL: server.URL, Password: "secret", Model: "openai/gpt-5.6-terra"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{{Role: "user", Content: "x"}}})
	var httpErr *opencode.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("Complete() error = %v, want HTTPError 503", err)
	}
	var sc interface{ StatusCode() int }
	if !errors.As(err, &sc) {
		t.Fatal("error does not expose StatusCode for the retry layer")
	}
	mu.Lock()
	defer mu.Unlock()
	if !aborted || !deleted {
		t.Fatalf("aborted=%t deleted=%t, want best-effort abort then delete after HTTP failure", aborted, deleted)
	}
}

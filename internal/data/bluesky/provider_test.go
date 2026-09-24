package bluesky

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/llm"
)

func TestGetSocialSentimentSearchesPublicAppView(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xrpc/app.bsky.feed.searchPosts" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "$AAPL" {
			t.Errorf("q = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"posts":[]}`))
	}))
	defer server.Close()

	provider := NewProvider(nil, "", nil)
	provider.api.SetBaseURL(server.URL)
	got, err := provider.GetSocialSentiment(context.Background(), " aapl ", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("GetSocialSentiment() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("GetSocialSentiment() = %v, want empty", got)
	}
}

func TestSearchAccessDeniedIsNotEmptySentiment(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("since") == "" || r.URL.Query().Get("until") == "" {
			t.Error("missing search time bounds")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	p := NewProvider(nil, "", nil)
	p.api.SetBaseURL(server.URL)
	got, err := p.GetSocialSentiment(context.Background(), "SPY", time.Now().Add(-time.Hour), time.Now())
	if len(got) != 0 || err == nil || !strings.Contains(err.Error(), "authenticated PDS search") {
		t.Fatalf("got=%v err=%v", got, err)
	}
	_, err = p.GetSocialSentiment(context.Background(), "AAPL", time.Now().Add(-time.Hour), time.Now())
	if err == nil || calls != 1 {
		t.Fatalf("denied search was retried per ticker: calls=%d err=%v", calls, err)
	}
}

func TestSearchUsesPostTimeAndDeduplicates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"posts":[
   {"uri":"at://one","record":{"text":"$SPY","createdAt":"2026-09-24T10:00:00Z"}},
   {"uri":"at://one","record":{"text":"$SPY","createdAt":"2026-09-24T10:00:00Z"}},
   {"uri":"at://future","record":{"text":"$SPY","createdAt":"2026-09-24T12:00:00Z"}}]}`))
	}))
	defer server.Close()
	classifier := llm.ProviderFunc(func(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		if strings.Contains(req.Messages[1].Content, "2.") {
			t.Error("duplicate or future post classified")
		}
		return &llm.CompletionResponse{Content: `[{"mentions_ticker":true,"sentiment":"bullish"}]`}, nil
	})
	p := NewProvider(classifier, "test", nil)
	p.api.SetBaseURL(server.URL)
	from := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	got, err := p.GetSocialSentiment(context.Background(), "SPY", from, from.Add(2*time.Hour))
	if err != nil || len(got) != 1 || got[0].PostCount != 1 || !got[0].MeasuredAt.Equal(from.Add(time.Hour)) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestSearchReportsClassifierFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"posts":[{"uri":"at://one","record":{"text":"$SPY","createdAt":"2026-09-24T10:00:00Z"}}]}`))
	}))
	defer server.Close()
	failure := errors.New("classifier unavailable")
	classifier := llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, failure })
	p := NewProvider(classifier, "test", nil)
	p.api.SetBaseURL(server.URL)
	from := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	got, err := p.GetSocialSentiment(context.Background(), "SPY", from, from.Add(2*time.Hour))
	if !errors.Is(err, failure) || len(got) != 0 {
		t.Fatalf("hidden classifier failure: got=%v err=%v", got, err)
	}
}

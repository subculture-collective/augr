package rss

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCurrentRSSArticleRejectsFabricatedStaleAndFutureDates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		published time.Time
		want      bool
	}{
		{name: "current", published: now.Add(-time.Hour), want: true},
		{name: "zero", published: time.Time{}, want: false},
		{name: "stale", published: now.Add(-25 * time.Hour), want: false},
		{name: "future", published: now.Add(16 * time.Minute), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := currentRSSArticle(test.published, now); got != test.want {
				t.Fatalf("currentRSSArticle() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestFetchWithStatsRetriesUntilArticleIsAcknowledged(t *testing.T) {
	t.Parallel()

	published := time.Now().UTC().Format(time.RFC1123)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><item><guid>x</guid><title>Title</title><pubDate>` + published + `</pubDate></item></channel></rss>`))
	}))
	defer server.Close()

	aggregator := NewAggregator([]Feed{{Name: "test", URL: server.URL}}, nil)
	first := aggregator.FetchWithStats(context.Background())
	second := aggregator.FetchWithStats(context.Background())
	if len(first.Articles) != 1 || len(second.Articles) != 1 {
		t.Fatalf("unacknowledged fetch counts = %d, %d; want retry", len(first.Articles), len(second.Articles))
	}
	aggregator.MarkSeen(first.Articles[0])
	if third := aggregator.FetchWithStats(context.Background()); len(third.Articles) != 0 {
		t.Fatalf("acknowledged fetch count = %d, want 0", len(third.Articles))
	}
}

func TestFetchWithStatsRetriesOneTransientFailure(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	published := time.Now().UTC().Format(time.RFC1123)
	aggregator := NewAggregator([]Feed{{Name: "test", URL: "https://example.test/feed"}}, nil)
	aggregator.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, timeoutError{}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`<rss><channel><item><guid>x</guid><title>Title</title><pubDate>` + published + `</pubDate></item></channel></rss>`)),
		}, nil
	})

	result := aggregator.FetchWithStats(context.Background())
	if len(result.Articles) != 1 || result.FeedsAttempted != 1 || result.FeedsSucceeded != 1 || result.FeedsFailed != 0 || result.FeedRetries != 1 {
		t.Fatalf("FetchWithStats() = %+v, want one recovered retry", result)
	}
}

func TestFetchWithStatsDoesNotRetryPermanentFailure(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	aggregator := NewAggregator([]Feed{{Name: "test", URL: "https://example.test/feed"}}, nil)
	aggregator.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})

	result := aggregator.FetchWithStats(context.Background())
	if calls.Load() != 1 || result.FeedsFailed != 1 || result.FeedRetries != 0 {
		t.Fatalf("FetchWithStats() = %+v after %d calls, want one permanent failure without retry", result, calls.Load())
	}
}

func TestTransientFeedErrorRejectsCancelledCaller(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if transientFeedError(ctx, timeoutError{}) {
		t.Fatal("transientFeedError() = true for cancelled caller")
	}
	if !transientFeedError(context.Background(), feedHTTPStatusError(http.StatusServiceUnavailable)) {
		t.Fatal("transientFeedError() = false for HTTP 503")
	}
	if transientFeedError(context.Background(), errors.New("invalid XML")) {
		t.Fatal("transientFeedError() = true for permanent parse failure")
	}
}

func TestParseRSSLeavesInvalidPublicationDateUntrusted(t *testing.T) {
	t.Parallel()

	articles, err := parseRSS("test", []byte(`<rss><channel><item><guid>x</guid><title>Title</title><pubDate>not-a-date</pubDate></item></channel></rss>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 1 || !articles[0].PublishedAt.IsZero() {
		t.Fatalf("articles = %#v, want one zero-date item", articles)
	}
}

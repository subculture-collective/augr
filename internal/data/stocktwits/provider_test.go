package stocktwits

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDataProviderAggregatesLabeledSentiment(t *testing.T) {
	t.Parallel()

	provider := NewDataProvider(nil)
	provider.client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/streams/symbol/AAPL.json") {
			t.Errorf("request path = %q", r.URL.Path)
		}
		body := `{"messages":[{"entities":{"sentiment":{"basic":"Bullish"}}},{"entities":{"sentiment":{"basic":"Bullish"}}},{"entities":{"sentiment":{"basic":"Bearish"}}},{"entities":{"sentiment":null}}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	got, err := provider.GetSocialSentiment(context.Background(), "AAPL", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetSocialSentiment() error = %v", err)
	}
	if len(got) != 1 || got[0].PostCount != 3 || got[0].Bullish != 2.0/3.0 || got[0].Bearish != 1.0/3.0 || got[0].Score != 1.0/3.0 {
		t.Fatalf("sentiment = %#v", got)
	}
}

func TestDataProviderUnsupportedCapabilities(t *testing.T) {
	t.Parallel()
	provider := NewDataProvider(nil)
	if _, err := provider.GetOHLCV(context.Background(), "AAPL", data.Timeframe1d, time.Time{}, time.Time{}); !errors.Is(err, data.ErrNotImplemented) {
		t.Fatalf("GetOHLCV() error = %v", err)
	}
	if _, err := provider.GetFundamentals(context.Background(), "AAPL"); !errors.Is(err, data.ErrNotImplemented) {
		t.Fatalf("GetFundamentals() error = %v", err)
	}
	if _, err := provider.GetNews(context.Background(), "AAPL", time.Time{}, time.Time{}); !errors.Is(err, data.ErrNotImplemented) {
		t.Fatalf("GetNews() error = %v", err)
	}
}

func TestDataProviderStampsCurrentSnapshotInsideARecentWindow(t *testing.T) {
	t.Parallel()

	provider := NewDataProvider(nil)
	provider.client.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"messages":[{"entities":{"sentiment":{"basic":"Bullish"}}},{"entities":{"sentiment":{"basic":"Bearish"}}}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	// The strategy runner fixes its window end before fetching bars, news,
	// and fundamentals, so the social call runs seconds after "to".
	to := time.Now().UTC().Add(-3 * time.Second)
	got, err := provider.GetSocialSentiment(context.Background(), "SPY", to.Add(-7*24*time.Hour), to)
	if err != nil {
		t.Fatalf("GetSocialSentiment() error = %v", err)
	}
	if len(got) != 1 || !got[0].MeasuredAt.Equal(to) {
		t.Fatalf("recent-window snapshot = %#v, want MeasuredAt at the window end %s", got, to)
	}

	historical := time.Now().UTC().Add(-time.Hour)
	got, err = provider.GetSocialSentiment(context.Background(), "SPY", historical.Add(-time.Hour), historical)
	if err != nil {
		t.Fatalf("GetSocialSentiment() error = %v", err)
	}
	if len(got) != 1 || !got[0].MeasuredAt.After(historical) {
		t.Fatalf("historical-window snapshot = %#v, want the real fetch time so it is not attributed to the past", got)
	}
}

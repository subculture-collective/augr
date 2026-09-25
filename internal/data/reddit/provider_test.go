package reddit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/integration/redditlimit"
)

func TestRelevantPostsSharesBudgetAcrossCommunities(t *testing.T) {
	now := time.Now().UTC()
	posts := []RedditPost{
		{Title: "Unrelated topic", URL: "a", UpdatedAt: now},
		{Title: "$SPY outlook", URL: "b", Subreddit: "ETFs", UpdatedAt: now.Add(-time.Hour)},
		{Title: "SPY outlook", URL: "b", Subreddit: "stocks", UpdatedAt: now},
		{Title: "SPY dividend", URL: "c", Subreddit: "dividends", UpdatedAt: now},
		{Title: "SPYDER is not SPY's ticker", URL: "d", UpdatedAt: now},
		{Title: "SPYDER", URL: "e", UpdatedAt: now},
	}
	got := relevantPosts("SPY", posts)
	if len(got) != 3 || got[0].URL != "c" || got[2].URL != "b" {
		t.Fatalf("selection=%+v", got)
	}
}

func TestScorePostsWithErrorDistinguishesFailureFromNoMentions(t *testing.T) {
	failure := errors.New("model unavailable")
	_, err := ScorePostsWithError(context.Background(), &stubLLMProvider{err: failure}, "test", "SPY", makePosts(1), discardLogger())
	if !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	got, err := ScorePostsWithError(context.Background(), &stubLLMProvider{responses: []string{`[{"mentions_ticker":false,"sentiment":"neutral"}]`}}, "test", "SPY", makePosts(1), discardLogger())
	if err != nil || got.Mentions != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestProviderKeepsInWindowObservationsAfterCollection(t *testing.T) {
	to := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	p := NewProvider(&stubLLMProvider{responses: []string{`[{"mentions_ticker":true,"sentiment":"bullish"}]`}}, "test", nil, discardLogger())
	p.postsAt = time.Now()
	p.posts = []RedditPost{{Title: "$SPY", URL: "one", UpdatedAt: to.Add(-time.Minute)}, {Title: "$SPY", URL: "future", UpdatedAt: to.Add(time.Minute)}, {Title: "$SPY", URL: "unknown"}}
	got, err := p.GetSocialSentiment(context.Background(), "SPY", to.Add(-time.Hour), to)
	if err != nil || len(got) != 1 || got[0].PostCount != 1 || !got[0].MeasuredAt.Equal(to.Add(-time.Minute)) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestCachedPostsLimitsFetchesAndReusesRecentCorpusWhileThrottled(t *testing.T) {
	old := []RedditPost{{Title: "$SPY old", URL: "old"}}
	fresh := []RedditPost{{Title: "$SPY fresh", URL: "fresh"}}
	for name, tc := range map[string]struct {
		age       time.Duration
		fetched   []RedditPost
		fetchErr  error
		wantURL   string
		wantErr   bool
		wantCalls int
	}{
		"within the hour: no request":            {age: 30 * time.Minute, wantURL: "old", wantCalls: 0},
		"expired: refetch":                       {age: 70 * time.Minute, fetched: fresh, wantURL: "fresh", wantCalls: 1},
		"expired, cooldown: reuse recent corpus": {age: 2 * time.Hour, fetchErr: errCooldownActive, wantURL: "old", wantCalls: 1},
		"expired, budget spent: reuse":           {age: 2 * time.Hour, fetchErr: redditlimit.ErrBudgetExhausted, wantURL: "old", wantCalls: 1},
		"expired, 429: reuse":                    {age: 2 * time.Hour, fetchErr: statusError{status: 429}, wantURL: "old", wantCalls: 1},
		"too old to reuse":                       {age: 4 * time.Hour, fetchErr: errCooldownActive, wantErr: true, wantCalls: 1},
		"other failure is reported":              {age: 2 * time.Hour, fetchErr: errors.New("dns"), wantErr: true, wantCalls: 1},
	} {
		t.Run(name, func(t *testing.T) {
			p := NewProvider(nil, "test", nil, discardLogger())
			p.posts, p.postsAt = append([]RedditPost(nil), old...), time.Now().Add(-tc.age)
			calls := 0
			p.fetch = func(context.Context, []string) ([]RedditPost, error) {
				calls++
				return tc.fetched, tc.fetchErr
			}
			got, err := p.cachedPosts(context.Background())
			if calls != tc.wantCalls {
				t.Fatalf("fetch calls = %d, want %d", calls, tc.wantCalls)
			}
			if tc.wantErr {
				if err == nil || len(got) != 0 {
					t.Fatalf("got %v, %v; want an error and no posts", got, err)
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].URL != tc.wantURL {
				t.Fatalf("got %v, %v; want %s", got, err, tc.wantURL)
			}
		})
	}
}

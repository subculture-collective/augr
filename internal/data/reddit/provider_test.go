package reddit

import (
	"context"
	"errors"
	"testing"
	"time"
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

package reddit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
)

// Provider implements data.DataProvider, providing social sentiment from
// Reddit RSS feeds using LLM-based sentiment classification.
type Provider struct {
	client      *Client
	llmProvider llm.Provider
	model       string
	subreddits  []string
	logger      *slog.Logger
	postsMu     sync.Mutex
	posts       []RedditPost
	postsAt     time.Time
	postsErr    error
}

// Compile-time check that Provider satisfies data.DataProvider.
var _ data.DataProvider = (*Provider)(nil)

// NewProvider constructs a Reddit social sentiment provider.
// subreddits controls which subreddits are scanned; pass nil for stock defaults.
func NewProvider(llmProvider llm.Provider, model string, subreddits []string, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	if len(subreddits) == 0 {
		subreddits = StockSubreddits()
	}
	return &Provider{
		client:      NewClient(logger),
		llmProvider: llmProvider,
		model:       model,
		subreddits:  subreddits,
		logger:      logger,
	}
}

// GetOHLCV is not supported by the Reddit provider.
func (p *Provider) GetOHLCV(_ context.Context, _ string, _ data.Timeframe, _, _ time.Time) ([]domain.OHLCV, error) {
	return nil, fmt.Errorf("reddit: GetOHLCV: %w", data.ErrNotImplemented)
}

// GetFundamentals is not supported by the Reddit provider.
func (p *Provider) GetFundamentals(_ context.Context, _ string) (data.Fundamentals, error) {
	return data.Fundamentals{}, fmt.Errorf("reddit: GetFundamentals: %w", data.ErrNotImplemented)
}

// GetNews is not supported by the Reddit provider.
func (p *Provider) GetNews(_ context.Context, _ string, _, _ time.Time) ([]data.NewsArticle, error) {
	return nil, fmt.Errorf("reddit: GetNews: %w", data.ErrNotImplemented)
}

// GetSocialSentiment fetches recent Reddit posts from configured subreddits,
// runs LLM triage to detect ticker mentions and sentiment, and returns an
// aggregated SocialSentiment snapshot.
func (p *Provider) GetSocialSentiment(ctx context.Context, ticker string, from, to time.Time) ([]data.SocialSentiment, error) {
	if p == nil {
		return nil, errors.New("reddit: provider is nil")
	}

	posts, fetchErr := p.cachedPosts(ctx)
	if len(posts) == 0 {
		p.logger.Info("reddit: no posts fetched",
			slog.String("ticker", ticker),
			slog.Int("subreddits", len(p.subreddits)),
		)
		return nil, fetchErr
	}

	// Filter posts to the requested time window. Reddit RSS returns ~25 most
	// recent posts per subreddit; only valid timestamps within [from, to] qualify.
	fromUTC := from.UTC()
	toUTC := to.UTC()
	filtered := make([]RedditPost, 0, len(posts))
	for _, post := range posts {
		if !post.UpdatedAt.IsZero() && !post.UpdatedAt.Before(fromUTC) && !post.UpdatedAt.After(toUTC) {
			filtered = append(filtered, post)
		}
	}

	filtered = relevantPosts(ticker, filtered)
	if len(filtered) == 0 {
		return nil, fetchErr
	}

	p.logger.Info("reddit: scoring posts for sentiment",
		slog.String("ticker", ticker),
		slog.Int("posts", len(filtered)),
	)

	result, scoreErr := ScorePostsWithError(ctx, p.llmProvider, p.model, ticker, filtered, p.logger)
	collectErr := errors.Join(fetchErr, scoreErr)
	if result.Mentions == 0 {
		return nil, collectErr
	}

	total := result.Bullish + result.Bearish + result.Neutral
	var score, bullish, bearish float64
	if total > 0 {
		bullish = float64(result.Bullish) / float64(total)
		bearish = float64(result.Bearish) / float64(total)
		score = bullish - bearish
	}

	latest := filtered[0].UpdatedAt
	return []data.SocialSentiment{{
		Ticker:     ticker,
		Source:     "reddit",
		Score:      score,
		Bullish:    bullish,
		Bearish:    bearish,
		PostCount:  result.Mentions,
		MeasuredAt: latest,
	}}, collectErr
}

// cachedPosts fetches the shared subreddit corpus once per scan window. A
// social run scores many tickers against the same posts; refetching every feed
// per ticker both wastes quota and triggers Reddit's provider-wide throttle.
func (p *Provider) cachedPosts(ctx context.Context) ([]RedditPost, error) {
	p.postsMu.Lock()
	defer p.postsMu.Unlock()
	if !p.postsAt.IsZero() && time.Since(p.postsAt) < 5*time.Minute {
		return append([]RedditPost(nil), p.posts...), p.postsErr
	}
	posts, err := p.client.FetchSubredditsWithError(ctx, p.subreddits)
	p.postsErr = err
	p.posts = append(p.posts[:0], posts...)
	p.postsAt = time.Now()
	return append([]RedditPost(nil), posts...), err
}

// relevantPosts shares the classifier budget across feeds rather than letting
// subreddit order consume it. Symbol matching is only a prefilter; the model
// still checks relevance and rejects ambiguous symbols.
func relevantPosts(ticker string, posts []RedditPost) []RedditPost {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return nil
	}
	pattern := regexp.MustCompile(`(?i)(^|[^a-z0-9])\$?` + regexp.QuoteMeta(ticker) + `([^a-z0-9]|$)`)
	seen := make(map[string]struct{})
	var out []RedditPost
	for _, post := range posts {
		if !pattern.MatchString(post.Title + " " + post.Body) {
			continue
		}
		key := post.URL
		if key == "" {
			key = post.Title + "\n" + post.Body
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, post)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

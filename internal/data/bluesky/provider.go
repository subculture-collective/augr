package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/reddit"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
)

const (
	defaultBaseURL = "https://public.api.bsky.app"
	defaultLimit   = 50
)

// Provider searches the public Bluesky AppView for ticker cashtags and uses
// the existing bounded social classifier to derive a source-specific signal.
type Provider struct {
	api           *data.APIClient
	provider      llm.Provider
	model         string
	logger        *slog.Logger
	accessMu      sync.Mutex
	accessRetryAt time.Time
	accessErr     error
}

var _ data.DataProvider = (*Provider)(nil)

type searchResponse struct {
	Posts []struct {
		URI    string `json:"uri"`
		Record struct {
			Text      string `json:"text"`
			CreatedAt string `json:"createdAt"`
		} `json:"record"`
		Author struct {
			Handle string `json:"handle"`
		} `json:"author"`
		ReplyCount  int `json:"replyCount"`
		RepostCount int `json:"repostCount"`
		LikeCount   int `json:"likeCount"`
	} `json:"posts"`
}

func NewProvider(provider llm.Provider, model string, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		api: data.NewAPIClient(data.APIClientConfig{
			BaseURL:     defaultBaseURL,
			Headers:     http.Header{"Accept": []string{"application/json"}, "User-Agent": []string{"augr/1.0 social-research"}},
			Timeout:     15 * time.Second,
			RateLimiter: data.NewRateLimiter(120, time.Minute),
			Logger:      logger,
			Prefix:      "bluesky",
		}),
		provider: provider,
		model:    model,
		logger:   logger,
	}
}

func (p *Provider) GetOHLCV(context.Context, string, data.Timeframe, time.Time, time.Time) ([]domain.OHLCV, error) {
	return nil, fmt.Errorf("bluesky: GetOHLCV: %w", data.ErrNotImplemented)
}

func (p *Provider) GetFundamentals(context.Context, string) (data.Fundamentals, error) {
	return data.Fundamentals{}, fmt.Errorf("bluesky: GetFundamentals: %w", data.ErrNotImplemented)
}

func (p *Provider) GetNews(context.Context, string, time.Time, time.Time) ([]data.NewsArticle, error) {
	return nil, fmt.Errorf("bluesky: GetNews: %w", data.ErrNotImplemented)
}

func (p *Provider) GetSocialSentiment(ctx context.Context, ticker string, from, to time.Time) ([]data.SocialSentiment, error) {
	if p == nil || p.api == nil {
		return nil, errors.New("bluesky: provider is not configured")
	}
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return nil, errors.New("bluesky: ticker is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.accessMu.Lock()
	accessErr, retryAt := p.accessErr, p.accessRetryAt
	p.accessMu.Unlock()
	if accessErr != nil && time.Now().Before(retryAt) {
		return nil, accessErr
	}
	params := url.Values{"q": {"$" + ticker}, "limit": {fmt.Sprint(defaultLimit)}, "sort": {"latest"}}
	if !from.IsZero() {
		params.Set("since", from.UTC().Format(time.RFC3339Nano))
	}
	if !to.IsZero() {
		params.Set("until", to.UTC().Format(time.RFC3339Nano))
	}
	body, status, err := p.api.Get(ctx, "/xrpc/app.bsky.feed.searchPosts", params)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			accessErr := fmt.Errorf("bluesky: search access denied (HTTP %d); check authenticated PDS search configuration or service access policy: %w", status, err)
			p.accessMu.Lock()
			p.accessErr, p.accessRetryAt = accessErr, time.Now().Add(15*time.Minute)
			p.accessMu.Unlock()
			return nil, accessErr
		}
		return nil, fmt.Errorf("bluesky: search %s: %w", ticker, err)
	}
	var response searchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("bluesky: decode search response: %w", err)
	}

	posts := make([]reddit.RedditPost, 0, len(response.Posts))
	engagement := 0
	var latest time.Time
	seen := make(map[string]struct{})
	for _, item := range response.Posts {
		createdAt, err := time.Parse(time.RFC3339Nano, item.Record.CreatedAt)
		if err != nil || createdAt.Before(from.UTC()) || createdAt.After(to.UTC()) {
			continue
		}
		if _, duplicate := seen[item.URI]; duplicate {
			continue
		}
		seen[item.URI] = struct{}{}
		if createdAt.After(latest) {
			latest = createdAt
		}
		posts = append(posts, reddit.RedditPost{Title: item.Record.Text, URL: item.URI, Author: item.Author.Handle, Subreddit: "bluesky", UpdatedAt: createdAt})
		engagement += item.ReplyCount + item.RepostCount + item.LikeCount
	}
	if len(posts) == 0 {
		return nil, nil
	}
	result, scoreErr := reddit.ScorePostsWithError(ctx, p.provider, p.model, ticker, posts, p.logger)
	if result.Mentions == 0 {
		return nil, scoreErr
	}
	total := result.Bullish + result.Bearish + result.Neutral
	if total == 0 {
		return nil, scoreErr
	}
	bullish := float64(result.Bullish) / float64(total)
	bearish := float64(result.Bearish) / float64(total)
	return []data.SocialSentiment{{
		Ticker: ticker, Source: "bluesky", Score: bullish - bearish,
		Bullish: bullish, Bearish: bearish, PostCount: result.Mentions,
		CommentCount: engagement, MeasuredAt: latest,
	}}, scoreErr
}

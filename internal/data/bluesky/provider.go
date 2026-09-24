package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	defaultServiceURL = "https://bsky.social"
	defaultLimit      = 50
	searchPath        = "/xrpc/app.bsky.feed.searchPosts"
	sessionPath       = "/xrpc/com.atproto.server.createSession"
)

// Credentials is the app-password login Bluesky requires for post search.
// Both public AppView hosts answer unauthenticated searchPosts with 403.
type Credentials struct {
	Identifier  string
	AppPassword string
	// ServiceURL is the account's PDS; it proxies app.bsky.* calls to the
	// AppView. Defaults to https://bsky.social.
	ServiceURL string
}

// Provider searches Bluesky for ticker cashtags as an authenticated account
// and uses the existing bounded social classifier to derive a source-specific
// signal.
type Provider struct {
	http        *http.Client
	serviceURL  string
	credentials Credentials
	limiter     *data.RateLimiter
	provider    llm.Provider
	model       string
	logger      *slog.Logger

	mu          sync.Mutex
	accessToken string
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

type xrpcError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// NewProvider builds an authenticated Bluesky provider.
func NewProvider(provider llm.Provider, model string, credentials Credentials, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	serviceURL := strings.TrimRight(strings.TrimSpace(credentials.ServiceURL), "/")
	if serviceURL == "" {
		serviceURL = defaultServiceURL
	}
	return &Provider{
		http:        &http.Client{Timeout: 15 * time.Second},
		serviceURL:  serviceURL,
		credentials: credentials,
		limiter:     data.NewRateLimiter(120, time.Minute),
		provider:    provider,
		model:       model,
		logger:      logger,
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
	if p == nil || p.http == nil {
		return nil, errors.New("bluesky: provider is not configured")
	}
	if strings.TrimSpace(p.credentials.Identifier) == "" || strings.TrimSpace(p.credentials.AppPassword) == "" {
		return nil, errors.New("bluesky: app-password login is not configured")
	}
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return nil, errors.New("bluesky: ticker is required")
	}
	params := url.Values{"q": {"$" + ticker}, "limit": {fmt.Sprint(defaultLimit)}, "sort": {"latest"}}
	body, err := p.search(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("bluesky: search %s: %w", ticker, err)
	}
	var response searchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("bluesky: decode search response: %w", err)
	}

	posts := make([]reddit.RedditPost, 0, len(response.Posts))
	engagement := 0
	for _, item := range response.Posts {
		createdAt, err := time.Parse(time.RFC3339Nano, item.Record.CreatedAt)
		if err != nil || createdAt.Before(from.UTC()) || createdAt.After(to.UTC()) {
			continue
		}
		posts = append(posts, reddit.RedditPost{Title: item.Record.Text, URL: item.URI, Author: item.Author.Handle, Subreddit: "bluesky", UpdatedAt: createdAt})
		engagement += item.ReplyCount + item.RepostCount + item.LikeCount
	}
	if len(posts) == 0 {
		return nil, nil
	}
	result := reddit.ScorePosts(ctx, p.provider, p.model, ticker, posts, p.logger)
	if result.Mentions == 0 {
		return nil, nil
	}
	total := result.Bullish + result.Bearish + result.Neutral
	if total == 0 {
		return nil, nil
	}
	bullish := float64(result.Bullish) / float64(total)
	bearish := float64(result.Bearish) / float64(total)
	return []data.SocialSentiment{{
		Ticker: ticker, Source: "bluesky", Score: bullish - bearish,
		Bullish: bullish, Bearish: bearish, PostCount: result.Mentions,
		CommentCount: engagement, MeasuredAt: data.CurrentSnapshotAsOf(time.Now().UTC(), to.UTC()),
	}}, nil
}

// search runs searchPosts with the cached session, logging in first and once
// more if the access token has expired.
func (p *Provider) search(ctx context.Context, params url.Values) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := p.session(ctx, attempt > 0)
		if err != nil {
			return nil, err
		}
		if err := p.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("wait for rate limiter: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.serviceURL+searchPath+"?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "augr/1.0 social-research")
		req.Header.Set("Authorization", "Bearer "+token)
		status, body, err := p.do(req)
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			return body, nil
		}
		if attempt == 0 && tokenRejected(status, body) {
			continue
		}
		return nil, statusError(status, body)
	}
	return nil, errors.New("access token rejected after a fresh login")
}

// session returns the cached access token, logging in when none is cached or
// when renew is set.
func (p *Provider) session(ctx context.Context, renew bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accessToken != "" && !renew {
		return p.accessToken, nil
	}
	payload, err := json.Marshal(map[string]string{
		"identifier": strings.TrimSpace(p.credentials.Identifier),
		"password":   strings.TrimSpace(p.credentials.AppPassword),
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.serviceURL+sessionPath, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	status, body, err := p.do(req)
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	if status != http.StatusOK {
		p.accessToken = ""
		return "", fmt.Errorf("login: %w", statusError(status, body))
	}
	var created struct {
		AccessJwt string `json:"accessJwt"`
	}
	if err := json.Unmarshal(body, &created); err != nil || strings.TrimSpace(created.AccessJwt) == "" {
		return "", errors.New("login: response did not include an access token")
	}
	p.accessToken = created.AccessJwt
	return p.accessToken, nil
}

func (p *Provider) do(req *http.Request) (int, []byte, error) {
	resp, err := p.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func tokenRejected(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	var xe xrpcError
	return status == http.StatusBadRequest && json.Unmarshal(body, &xe) == nil && (xe.Error == "ExpiredToken" || xe.Error == "InvalidToken")
}

// statusError reports the XRPC error name and message, never the request.
func statusError(status int, body []byte) error {
	var xe xrpcError
	if json.Unmarshal(body, &xe) == nil && xe.Error != "" {
		return fmt.Errorf("status %d: %s: %s", status, xe.Error, xe.Message)
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 120 {
		snippet = snippet[:120]
	}
	return fmt.Errorf("status %d: %s", status, snippet)
}

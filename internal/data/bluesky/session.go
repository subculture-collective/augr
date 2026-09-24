package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AuthConfig selects an account's normal PDS session flow. Secrets are kept in
// memory and must not be included in logs, cache entries, or test snapshots.
type AuthConfig struct {
	Identifier  string
	AppPassword string
	PDSURL      string
}

type sessionClient struct {
	mu      sync.Mutex
	config  AuthConfig
	client  *http.Client
	access  string
	refresh string
	retryAt time.Time
	lastErr error
}

func newSessionClient(config AuthConfig) *sessionClient {
	if config.PDSURL == "" {
		config.PDSURL = "https://bsky.social"
	}
	config.PDSURL = strings.TrimRight(config.PDSURL, "/")
	return &sessionClient{config: config, client: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (s *sessionClient) search(ctx context.Context, params url.Values) ([]byte, int, error) {
	// Serialize renewal so concurrent ticker requests reuse one session and never
	// rotate the same refresh token in parallel.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if s.lastErr != nil && time.Now().Before(s.retryAt) {
		return nil, 0, s.lastErr
	}
	if s.access == "" {
		if err := s.login(ctx); err != nil {
			return nil, 0, s.authFailure(err)
		}
	}
	body, status, err := s.request(ctx, http.MethodGet, "app.bsky.feed.searchPosts", params, nil, s.access)
	if status != http.StatusUnauthorized {
		return body, status, err
	}
	if err := s.renew(ctx); err != nil {
		return nil, 0, s.authFailure(err)
	}
	return s.request(ctx, http.MethodGet, "app.bsky.feed.searchPosts", params, nil, s.access)
}

func (s *sessionClient) authFailure(err error) error {
	s.lastErr = err
	s.retryAt = time.Now().Add(time.Minute)
	return err
}

func (s *sessionClient) login(ctx context.Context) error {
	payload, err := json.Marshal(map[string]string{"identifier": s.config.Identifier, "password": s.config.AppPassword})
	if err != nil {
		return errors.New("bluesky: encode session request")
	}
	body, _, err := s.request(ctx, http.MethodPost, "com.atproto.server.createSession", nil, payload, "")
	if err != nil {
		return err
	}
	return s.acceptSession(body)
}

func (s *sessionClient) renew(ctx context.Context) error {
	body, status, err := s.request(ctx, http.MethodPost, "com.atproto.server.refreshSession", nil, nil, s.refresh)
	if status == http.StatusUnauthorized || status == http.StatusBadRequest {
		s.access, s.refresh = "", ""
		return s.login(ctx)
	}
	if err != nil {
		return err
	}
	return s.acceptSession(body)
}

func (s *sessionClient) acceptSession(body []byte) error {
	var session struct {
		Access  string `json:"accessJwt"`
		Refresh string `json:"refreshJwt"`
	}
	if json.Unmarshal(body, &session) != nil || session.Access == "" || session.Refresh == "" {
		return errors.New("bluesky: invalid session response")
	}
	s.access, s.refresh = session.Access, session.Refresh
	s.lastErr = nil
	return nil
}

func (s *sessionClient) request(ctx context.Context, method, endpoint string, params url.Values, body []byte, token string) ([]byte, int, error) {
	target := s.config.PDSURL + "/xrpc/" + endpoint
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("bluesky: invalid PDS request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "augr/1.0 social-research")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if endpoint == "app.bsky.feed.searchPosts" {
		req.Header.Set("atproto-proxy", "did:web:api.bsky.app#bsky_appview")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, errors.New("bluesky: PDS transport failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Never echo a session response body: it may contain credentials or tokens.
		return nil, resp.StatusCode, fmt.Errorf("bluesky: %s HTTP %d", endpoint, resp.StatusCode)
	}
	const maxResponse = 2 * 1024 * 1024
	result, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(result) > maxResponse {
		return nil, resp.StatusCode, errors.New("bluesky: invalid or oversized PDS response")
	}
	return result, resp.StatusCode, nil
}

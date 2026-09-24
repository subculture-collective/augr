package bluesky

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetSocialSentimentLogsInOnceAndSearchesWithBearer(t *testing.T) {
	t.Parallel()

	var logins, searches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case sessionPath:
			logins.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["identifier"] != "augr.bsky.social" || body["password"] != "app-pass" {
				t.Errorf("login body = %v", body)
			}
			_, _ = w.Write([]byte(`{"accessJwt":"token-1"}`))
		case searchPath:
			searches.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
				t.Errorf("Authorization = %q", got)
			}
			if got := r.URL.Query().Get("q"); got != "$AAPL" {
				t.Errorf("q = %q", got)
			}
			_, _ = w.Write([]byte(`{"posts":[]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewProvider(nil, "", Credentials{Identifier: "augr.bsky.social", AppPassword: "app-pass", ServiceURL: server.URL}, nil)
	for range 2 {
		got, err := provider.GetSocialSentiment(context.Background(), " aapl ", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("GetSocialSentiment() error = %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("GetSocialSentiment() = %v, want empty", got)
		}
	}
	if logins.Load() != 1 || searches.Load() != 2 {
		t.Fatalf("logins = %d searches = %d, want one login reused for two searches", logins.Load(), searches.Load())
	}
}

func TestGetSocialSentimentLogsInAgainAfterExpiredToken(t *testing.T) {
	t.Parallel()

	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case sessionPath:
			n := logins.Add(1)
			_, _ = w.Write([]byte(`{"accessJwt":"token-` + string(rune('0'+n)) + `"}`))
		case searchPath:
			if r.Header.Get("Authorization") == "Bearer token-1" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"ExpiredToken","message":"Token has expired"}`))
				return
			}
			_, _ = w.Write([]byte(`{"posts":[]}`))
		}
	}))
	defer server.Close()

	provider := NewProvider(nil, "", Credentials{Identifier: "id", AppPassword: "pw", ServiceURL: server.URL}, nil)
	if _, err := provider.GetSocialSentiment(context.Background(), "SPY", time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatalf("GetSocialSentiment() error = %v", err)
	}
	if logins.Load() != 2 {
		t.Fatalf("logins = %d, want a second login after ExpiredToken", logins.Load())
	}
}

func TestGetSocialSentimentReportsLoginFailureWithoutSecrets(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"AuthenticationRequired","message":"Invalid identifier or password"}`))
	}))
	defer server.Close()

	provider := NewProvider(nil, "", Credentials{Identifier: "id", AppPassword: "secret-app-pass", ServiceURL: server.URL}, nil)
	_, err := provider.GetSocialSentiment(context.Background(), "SPY", time.Now().Add(-time.Hour), time.Now())
	if err == nil || !strings.Contains(err.Error(), "AuthenticationRequired") || strings.Contains(err.Error(), "secret-app-pass") {
		t.Fatalf("GetSocialSentiment() error = %v, want the XRPC error without the password", err)
	}
}

func TestGetSocialSentimentRequiresCredentials(t *testing.T) {
	t.Parallel()

	provider := NewProvider(nil, "", Credentials{}, nil)
	if _, err := provider.GetSocialSentiment(context.Background(), "SPY", time.Now().Add(-time.Hour), time.Now()); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("GetSocialSentiment() error = %v, want not configured", err)
	}
}

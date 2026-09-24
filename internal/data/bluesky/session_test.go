package bluesky

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionReuseAndRefresh(t *testing.T) {
	var logins, refreshes, searches int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "augr/1.0 social-research" {
			t.Error("missing application identification")
		}
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			logins++
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["identifier"] != "test.example" || body["password"] != "synthetic-password" {
				t.Error("invalid login payload")
			}
			if r.Method != http.MethodPost {
				t.Error("login must be POST")
			}
			_, _ = fmt.Fprint(w, `{"accessJwt":"first-access","refreshJwt":"first-refresh"}`)
		case "/xrpc/com.atproto.server.refreshSession":
			refreshes++
			if r.Header.Get("Authorization") != "Bearer first-refresh" {
				t.Error("refresh did not use refresh token")
			}
			_, _ = fmt.Fprint(w, `{"accessJwt":"second-access","refreshJwt":"second-refresh"}`)
		case "/xrpc/app.bsky.feed.searchPosts":
			searches++
			if r.Header.Get("atproto-proxy") != "did:web:api.bsky.app#bsky_appview" {
				t.Error("missing AppView proxy header")
			}
			if searches == 2 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			want := "Bearer first-access"
			if searches > 2 {
				want = "Bearer second-access"
			}
			if r.Header.Get("Authorization") != want {
				t.Error("incorrect search token")
			}
			_, _ = fmt.Fprint(w, `{"posts":[]}`)
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := newSessionClient(AuthConfig{Identifier: "test.example", AppPassword: "synthetic-password", PDSURL: server.URL})
	for i := 0; i < 3; i++ {
		_, status, err := c.search(context.Background(), url.Values{"q": {"$SPY"}})
		if err != nil || status != 200 {
			t.Fatalf("search %d: status=%d err=%v", i, status, err)
		}
	}
	if logins != 1 || refreshes != 1 || searches != 4 {
		t.Fatalf("login=%d refresh=%d search=%d", logins, refreshes, searches)
	}
}

func TestSessionConcurrentSearchesShareLogin(t *testing.T) {
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "createSession") {
			logins.Add(1)
			_, _ = fmt.Fprint(w, `{"accessJwt":"access","refreshJwt":"refresh"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"posts":[]}`)
	}))
	defer server.Close()
	c := newSessionClient(AuthConfig{Identifier: "test.example", AppPassword: "synthetic-password", PDSURL: server.URL})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := c.search(context.Background(), nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if logins.Load() != 1 {
		t.Fatalf("login count=%d", logins.Load())
	}
}

func TestSessionFailureRedactsResponseAndBacksOff(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(401)
		_, _ = fmt.Fprint(w, "synthetic-password reflected-secret")
	}))
	defer server.Close()
	c := newSessionClient(AuthConfig{Identifier: "test.example", AppPassword: "synthetic-password", PDSURL: server.URL})
	for i := 0; i < 2; i++ {
		_, _, err := c.search(context.Background(), nil)
		if err == nil || strings.Contains(err.Error(), "synthetic-password") || strings.Contains(err.Error(), "reflected-secret") {
			t.Fatalf("unsafe error=%v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("bad credentials retried for every ticker: calls=%d", calls)
	}
}

func TestSessionRefusesRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetCalls.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c := newSessionClient(AuthConfig{Identifier: "test.example", AppPassword: "synthetic-password", PDSURL: server.URL})
	if _, _, err := c.search(context.Background(), nil); err == nil {
		t.Fatal("redirect succeeded")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("credentials followed redirect")
	}
}

func TestProviderUsesAuthenticatedSearch(t *testing.T) {
	var login bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "createSession") {
			login = true
			_, _ = fmt.Fprint(w, `{"accessJwt":"access","refreshJwt":"refresh"}`)
			return
		}
		if !login || r.Header.Get("Authorization") != "Bearer access" {
			t.Error("provider bypassed session")
		}
		_, _ = fmt.Fprint(w, `{"posts":[]}`)
	}))
	defer server.Close()
	p := NewProviderWithAuth(nil, "", nil, AuthConfig{Identifier: "test.example", AppPassword: "synthetic-password", PDSURL: server.URL})
	_, err := p.GetSocialSentiment(context.Background(), "SPY", time.Now().Add(-time.Hour), time.Now())
	if err != nil || !login {
		t.Fatalf("login=%v err=%v", login, err)
	}
}

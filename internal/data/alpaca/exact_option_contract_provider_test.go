package alpaca

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExactContractProviderRetainsCurrentReference(t *testing.T) {
	const symbol = "AAPL260116C00150000"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v2/options/contracts/"+symbol || r.URL.RawQuery != "" || r.Header.Get("APCA-API-KEY-ID") != "synthetic-key" || r.Header.Get("APCA-API-SECRET-KEY") != "synthetic-secret" {
			t.Errorf("unexpected reference request method/path/query/credentials")
		}
		if _, err := fmt.Fprint(w, exactContractRow); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	p, err := NewExactContractProvider(server.URL, "synthetic-key", "synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	result, err := p.GetExactOptionContract(context.Background(), symbol)
	if err != nil || calls != 1 || result.Contract.Symbol != symbol || result.Contract.Size != "9007199254740993" || string(result.Page.Body) != exactContractRow || result.Page.RequestPath != "/v2/options/contracts/"+symbol || result.ObservedAt.Before(before) {
		t.Fatalf("current reference lost: result=%+v calls=%d err=%v", result, calls, err)
	}
	result.Page.Body[0] = 'x'
	if string(result.Contract.Raw) != exactContractRow {
		t.Fatal("retained row aliases page")
	}
}

func TestExactContractProviderRejectsUnsafeOrMismatchedEvidence(t *testing.T) {
	for _, mode := range []string{"wrong symbol", "redirect", "denied", "echo", "invalid symbol", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch mode {
				case "redirect":
					http.Redirect(w, r, "/must-not-follow", http.StatusFound)
				case "denied":
					w.WriteHeader(http.StatusForbidden)
				case "echo":
					if _, err := fmt.Fprint(w, strings.Replace(exactContractRow, `"status":"active"`, `"status":"synthetic-secret"`, 1)); err != nil {
						t.Error(err)
					}
				default:
					if _, err := fmt.Fprint(w, exactContractRow); err != nil {
						t.Error(err)
					}
				}
			}))
			defer server.Close()
			p, err := NewExactContractProvider(server.URL, "synthetic-key", "synthetic-secret")
			if err != nil {
				t.Fatal(err)
			}
			symbol := "AAPL260116C00150000"
			if mode == "wrong symbol" {
				symbol = "MSFT260116C00150000"
			}
			if mode == "invalid symbol" {
				symbol = "../accounts"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			result, err := p.GetExactOptionContract(ctx, symbol)
			if err == nil || result.Contract.Symbol != "" || len(result.Page.Body) != 0 || !result.ObservedAt.IsZero() || calls > 1 {
				t.Fatalf("unsafe reference accepted or partial evidence escaped: err=%v calls=%d", err, calls)
			}
			if (mode == "invalid symbol" || mode == "cancelled") && calls != 0 {
				t.Fatal("request made for rejected input")
			}
		})
	}
	for _, origin := range []string{"", "https://data.alpaca.markets", "https://untrusted.example", "http://paper-api.alpaca.markets", "https://paper-api.alpaca.markets/path", "https://user:pass@paper-api.alpaca.markets", "https://paper-api.alpaca.markets?x=y"} {
		if _, err := NewExactContractProvider(origin, "synthetic-key", "synthetic-secret"); err == nil {
			t.Fatalf("accepted unsafe origin %q", origin)
		}
	}
}

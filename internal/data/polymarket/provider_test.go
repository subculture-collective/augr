package polymarket

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
)

func TestGetOHLCVUsesYesTokenForPriceHistory(t *testing.T) {
	t.Parallel()

	const (
		slug     = "will-example-happen"
		yesToken = "yes-token"
	)
	var gotMarket string
	var clobMarketsHits int
	clob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/markets":
			clobMarketsHits++
			http.Error(w, "CLOB /markets does not filter by slug", http.StatusBadRequest)
		case "/prices-history":
			gotMarket = r.URL.Query().Get("market")
			_, _ = fmt.Fprint(w, `{"history":[{"t":1700000000,"p":0.61}]}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(clob.Close)
	gamma := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets" || r.URL.Query().Get("slug") != slug {
			t.Errorf("unexpected gamma request %s %s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = fmt.Fprintf(w, `[{"slug":%q,"conditionId":"cond-1","clobTokenIds":"[\"%s\",\"no-token\"]","outcomes":"[\"Yes\",\"No\"]"}]`, slug, yesToken)
	}))
	t.Cleanup(gamma.Close)

	provider := NewProvider(clob.URL, nil)
	provider.client = clob.Client()
	provider.SetGammaClient(NewGammaClient(gamma.URL, gamma.Client()))
	from := time.Unix(1699999900, 0).UTC()
	to := time.Unix(1700000100, 0).UTC()
	bars, err := provider.GetOHLCV(context.Background(), slug, data.Timeframe5m, from, to)
	if err != nil {
		t.Fatalf("GetOHLCV() error = %v", err)
	}
	if gotMarket != yesToken {
		t.Fatalf("prices-history market = %q, want yes token %q", gotMarket, yesToken)
	}
	if clobMarketsHits != 0 {
		t.Fatalf("CLOB /markets hits = %d, want slug lookup to use Gamma", clobMarketsHits)
	}
	if len(bars) != 1 || bars[0].Close != 0.61 {
		t.Fatalf("unexpected bars: %+v", bars)
	}
}

func TestResolvePriceHistoryMarketIDRejectsSubstitutedGammaMarket(t *testing.T) {
	t.Parallel()

	gamma := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[{"slug":"substitute","conditionId":"cond-x","clobTokenIds":"[\"wrong\",\"no\"]","outcomes":"[\"Yes\",\"No\"]"}]`)
	}))
	defer gamma.Close()
	provider := NewProvider("http://127.0.0.1:1", nil)
	provider.SetGammaClient(NewGammaClient(gamma.URL, gamma.Client()))
	_, err := provider.resolvePriceHistoryMarketID(context.Background(), "exact")
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("resolvePriceHistoryMarketID() error = %v, want gamma identity mismatch", err)
	}
}

package alpaca

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type stockDailyCacheStub struct {
	entry  *domain.MarketData
	key    repository.MarketDataCacheKey
	writes int
}

func TestStockDailyCompletedSessionDelay(t *testing.T) {
	for _, minute := range []int{0, 14, 15, 25} {
		t.Run(fmt.Sprint(minute), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("end") != "2026-09-10T20:00:00Z" {
					t.Error("SIP query must end at the completed close, not future midnight")
				}
				if _, err := fmt.Fprint(w, `{"symbol":"ALP","next_page_token":null,"bars":[{"t":"2026-09-10T04:00:00Z","o":5,"h":6,"l":4,"c":5,"v":100,"n":10,"vw":5}]}`); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			p := NewStockDailyProvider("synthetic-key", "synthetic-secret")
			p.transport.SetBaseURL(server.URL)
			p.now = func() time.Time { return time.Date(2026, 9, 10, 20, minute, 0, 0, time.UTC) }
			got, err := p.GetDailyBars(context.Background(), "ALP", time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))
			if minute < 15 {
				if err == nil || calls != 0 || len(got.Bars) != 0 {
					t.Fatalf("provisional/real-time acquisition accepted: calls=%d err=%v", calls, err)
				}
			} else if err != nil || calls != 1 || len(got.Bars) != 1 {
				t.Fatalf("completed delayed series rejected: calls=%d err=%v", calls, err)
			}
		})
	}
}

func (s *stockDailyCacheStub) Get(_ context.Context, key repository.MarketDataCacheKey) (*domain.MarketData, error) {
	s.key = key
	return s.entry, nil
}

func (s *stockDailyCacheStub) Set(_ context.Context, entry *domain.MarketData) error {
	s.entry = entry
	s.writes++
	return nil
}

func (*stockDailyCacheStub) Expire(context.Context, repository.MarketDataCacheExpireFilter) error {
	return nil
}

func TestStockDailyCacheSeparatesAndRevalidatesSource(t *testing.T) {
	const raw = `{"symbol":"ALP","next_page_token":null,"bars":[{"t":"2026-09-09T04:00:00Z","o":5,"h":6,"l":4,"c":5.4141,"v":152208,"n":10,"vw":5}]}`
	for _, mode := range []string{"valid", "wrong source", "expired", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if _, err := fmt.Fprint(w, raw); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			cache := &stockDailyCacheStub{}
			p := NewCachedStockDailyProvider("synthetic-key", "synthetic-secret", cache)
			p.transport.SetBaseURL(server.URL)
			p.now = func() time.Time { return time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC) }
			from, to := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			first, err := p.GetDailyBars(context.Background(), "ALP", from, to)
			if err != nil || first.Receipt.Pages != 1 || cache.writes != 1 || cache.key.Provider != StockDailySource || cache.entry.Provider != StockDailySource || string(cache.entry.Data) != raw {
				t.Fatalf("source retention failed: err=%v", err)
			}
			switch mode {
			case "wrong source":
				cache.entry.Provider = "stock-chain"
			case "expired":
				cache.entry.ExpiresAt = p.now()
			case "corrupt":
				cache.entry.Data = []byte(`{"symbol":"ALP"}`)
			}
			second, err := p.GetDailyBars(context.Background(), "ALP", from, to)
			if err != nil || len(second.Bars) != 1 {
				t.Fatalf("read failed: %v", err)
			}
			wantCalls, wantPages := 2, 1
			if mode == "valid" {
				wantCalls, wantPages = 1, 0
			}
			if calls != wantCalls || second.Receipt.Pages != wantPages {
				t.Fatalf("calls=%d receiptPages=%d", calls, second.Receipt.Pages)
			}
		})
	}
}

func TestStockDailyPreservesSplitAdjustedSIPSource(t *testing.T) {
	const raw = `{"symbol":"ALP","next_page_token":null,"bars":[{"t":"2026-09-08T04:00:00Z","o":5.035,"h":5.405,"l":4.955,"c":5.385,"v":66311,"n":4230,"vw":5.118},{"t":"2026-09-09T04:00:00Z","o":5.01,"h":5.78,"l":4.8,"c":5.4141,"v":152208,"n":5719,"vw":5.168233}]}`
	for _, mode := range []string{"valid", "wrong symbol", "missing terminal", "null price", "out of interval", "redirect", "denied", "missing session", "intraday timestamp", "zero price", "invalid bounds"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != "/v2/stocks/ALP/bars" || q.Get("feed") != "sip" || q.Get("adjustment") != "split" || q.Get("timeframe") != "1Day" || q.Get("end") != "2026-09-10T00:00:00Z" {
					t.Error("incorrect source contract")
				}
				body := raw
				switch mode {
				case "missing session":
					body = strings.Replace(body, `,{"t":"2026-09-09T04:00:00Z","o":5.01,"h":5.78,"l":4.8,"c":5.4141,"v":152208,"n":5719,"vw":5.168233}`, "", 1)
				case "intraday timestamp":
					body = strings.Replace(body, "2026-09-09T04:00:00Z", "2026-09-09T13:30:00Z", 1)
				case "zero price":
					body = strings.Replace(body, `"c":5.4141`, `"c":0`, 1)
				case "invalid bounds":
					body = strings.Replace(body, `"c":5.4141`, `"c":50`, 1)
				case "wrong symbol":
					body = strings.Replace(body, `"ALP"`, `"MSFT"`, 1)
				case "missing terminal":
					body = strings.Replace(body, `"next_page_token":null,`, "", 1)
				case "null price":
					body = strings.Replace(body, `"c":5.4141`, `"c":null`, 1)
				case "out of interval":
					body = strings.Replace(body, "2026-09-09T04:00:00Z", "2026-09-10T04:00:00Z", 1)
				case "redirect":
					http.Redirect(w, r, "/never-follow", http.StatusFound)
					return
				case "denied":
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if _, err := fmt.Fprint(w, body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			p := NewStockDailyProvider("synthetic-key", "synthetic-secret")
			p.transport.SetBaseURL(server.URL)
			p.now = func() time.Time { return time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC) }
			from, to := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			result, err := p.GetDailyBars(context.Background(), "ALP", from, to)
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			if mode != "valid" {
				if err == nil || len(result.Bars) != 0 || len(result.Pages) != 0 {
					t.Fatal("invalid/partial source escaped")
				}
				return
			}
			if err != nil || len(result.Bars) != 2 || result.Bars[1].Close != "5.4141" || result.Bars[1].Volume != "152208" || result.Receipt.Feed != "sip" || result.Receipt.AdjustmentPolicy != "split" || string(result.Pages[0].Body) != raw {
				t.Fatalf("source lost: %+v err=%v", result, err)
			}
			for _, symbol := range []string{"../ALP", "alp", "ALP?feed=iex"} {
				if _, err := p.GetDailyBars(context.Background(), symbol, from, to); err == nil {
					t.Fatal("unsafe symbol accepted")
				}
			}
			if _, err := p.GetDailyBars(context.Background(), "ALP", from, to.Add(24*time.Hour)); err == nil {
				t.Fatal("provisional day accepted")
			}
			if calls != 1 {
				t.Fatal("invalid input triggered acquisition")
			}
		})
	}
}

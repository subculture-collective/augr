package automation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/universe"
)

type operationalUniverseRepo struct {
	watchlist []universe.TrackedTicker
	err       error
	limit     int
	listCalls int
}

func (r *operationalUniverseRepo) Upsert(context.Context, *universe.TrackedTicker) error { return nil }

func (r *operationalUniverseRepo) UpsertBatch(context.Context, []universe.TrackedTicker) error {
	return nil
}

func (r *operationalUniverseRepo) List(context.Context, universe.ListFilter, int, int) ([]universe.TrackedTicker, error) {
	r.listCalls++
	return nil, nil
}

func (r *operationalUniverseRepo) Watchlist(_ context.Context, limit int) ([]universe.TrackedTicker, error) {
	r.limit = limit
	return append([]universe.TrackedTicker(nil), r.watchlist...), r.err
}

func (r *operationalUniverseRepo) UpdateScore(context.Context, string, float64) error { return nil }

func (r *operationalUniverseRepo) Count(context.Context) (int, error) { return 0, nil }

type failingOperationalPositionRepo struct{ *recordingPositionRepo }

func (r *failingOperationalPositionRepo) GetOpen(context.Context, repository.PositionFilter, int, int) ([]domain.Position, error) {
	return nil, errors.New("positions unavailable")
}

type operationalStrategyRepo struct {
	*kalshiStrategyRepoStub
	err error
}

func (r *operationalStrategyRepo) Count(context.Context, repository.StrategyFilter) (int, error) {
	return len(r.strategies), r.err
}

func TestSelectOperationalStockTickersNormalizesDeduplicatesAndFiltersMarkets(t *testing.T) {
	owned := uuid.New()
	repo := &operationalUniverseRepo{watchlist: []universe.TrackedTicker{{Ticker: " msft "}, {Ticker: "NVDA"}, {Ticker: "nvda"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{
		PositionRepo: newRecordingPositionRepo(
			&domain.Position{ID: uuid.New(), Ticker: " aapl ", MarketType: domain.MarketTypeStock},
			&domain.Position{ID: uuid.New(), Ticker: "legacy", AssetClass: domain.AssetClassEquity},
			&domain.Position{ID: uuid.New(), Ticker: "OWNED-LEGACY", StrategyID: &owned, AssetClass: domain.AssetClassEquity},
			&domain.Position{ID: uuid.New(), Ticker: "AAPL260821C00150000", MarketType: domain.MarketTypeStock, AssetClass: domain.AssetClassOption},
			&domain.Position{ID: uuid.New(), Ticker: "UNKNOWN", AssetClass: domain.AssetClassOption},
			&domain.Position{ID: uuid.New(), Ticker: "AMBIGUOUS"},
			&domain.Position{ID: uuid.New(), Ticker: "KX:YES", MarketType: domain.MarketTypeKalshi},
			&domain.Position{ID: uuid.New(), Ticker: "event-slug", MarketType: domain.MarketTypePolymarket, AssetClass: domain.AssetClassEquity},
		),
		StrategyRepo: &operationalStrategyRepo{kalshiStrategyRepoStub: &kalshiStrategyRepoStub{strategies: []domain.Strategy{
			{Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
			{Ticker: " msft", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive},
			{Ticker: "KX:NO", MarketType: domain.MarketTypeKalshi, Status: domain.StrategyStatusActive},
		}}},
		Universe:                     universe.NewUniverse(repo, nil, nil),
		HistoryRefreshWatchlistLimit: 17,
	})

	got, err := orch.selectOperationalStockTickers(context.Background())
	if err != nil {
		t.Fatalf("selectOperationalStockTickers() error = %v", err)
	}
	if want := []string{"AAPL", "LEGACY", "MSFT", "NVDA"}; !reflect.DeepEqual(got.Tickers, want) {
		t.Fatalf("tickers = %v, want %v", got.Tickers, want)
	}
	if got.Positions != 2 || got.Strategies != 2 || got.Watchlist != 2 {
		t.Fatalf("source counts = positions:%d strategies:%d watchlist:%d", got.Positions, got.Strategies, got.Watchlist)
	}
	if repo.limit != 17 {
		t.Fatalf("watchlist limit = %d, want 17", repo.limit)
	}
}

func TestSelectOperationalStockTickersSourceFailureIsTerminal(t *testing.T) {
	for _, test := range []struct {
		name string
		deps OrchestratorDeps
		want string
	}{
		{
			name: "positions",
			deps: OrchestratorDeps{PositionRepo: &failingOperationalPositionRepo{newRecordingPositionRepo()}},
			want: "positions unavailable",
		},
		{
			name: "strategies",
			deps: OrchestratorDeps{PositionRepo: newRecordingPositionRepo(), StrategyRepo: &operationalStrategyRepo{kalshiStrategyRepoStub: &kalshiStrategyRepoStub{}, err: errors.New("strategies unavailable")}},
			want: "strategies unavailable",
		},
		{
			name: "watchlist",
			deps: OrchestratorDeps{PositionRepo: newRecordingPositionRepo(), StrategyRepo: &kalshiStrategyRepoStub{}, Universe: universe.NewUniverse(&operationalUniverseRepo{err: errors.New("watchlist unavailable")}, nil, nil)},
			want: "watchlist unavailable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.deps.StrategyRepo == nil {
				test.deps.StrategyRepo = &kalshiStrategyRepoStub{}
			}
			if test.deps.Universe == nil {
				test.deps.Universe = universe.NewUniverse(&operationalUniverseRepo{}, nil, nil)
			}
			orch := NewJobOrchestrator(test.deps)
			if _, err := orch.selectOperationalStockTickers(context.Background()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("selectOperationalStockTickers() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRefreshedTickerStateIsPerOrchestratorAndRaceSafe(t *testing.T) {
	first := NewJobOrchestrator(OrchestratorDeps{})
	second := NewJobOrchestrator(OrchestratorDeps{})
	first.setRefreshedTickers([]string{"AAPL", "MSFT"})
	if got := second.getRefreshedTickers(); len(got) != 0 {
		t.Fatalf("new orchestrator refreshed tickers = %v, want empty", got)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			first.setRefreshedTickers([]string{"NVDA"})
		}()
		go func() {
			defer wg.Done()
			_ = first.getRefreshedTickers()
		}()
	}
	wg.Wait()
}

func TestHistoryAndDeepScansUseOperationalScope(t *testing.T) {
	repo := &operationalUniverseRepo{watchlist: []universe.TrackedTicker{{Ticker: "MSFT"}}}
	orch := NewJobOrchestrator(OrchestratorDeps{
		PositionRepo:                 newRecordingPositionRepo(&domain.Position{ID: uuid.New(), Ticker: "AAPL", MarketType: domain.MarketTypeStock}),
		StrategyRepo:                 &kalshiStrategyRepoStub{},
		Universe:                     universe.NewUniverse(repo, nil, nil),
		DataService:                  data.NewDataService(config.Config{}, nil, nil, nil, nil),
		HistoryRefreshWatchlistLimit: 7,
	})
	orch.Register("history_refresh", "test", historyRefreshSpec, orch.historyRefresh)
	orch.Register("deep_scan", "test", deepScanSpec, orch.deepScan)

	if err := orch.historyRefresh(context.Background()); err == nil {
		t.Fatal("historyRefresh() error = nil, want unavailable persistence error")
	}
	if got := singleJobStatus(t, orch, "history_refresh").LastSummary["selected"]; got != 2 {
		t.Fatalf("history refresh selected = %d, want 2 operational tickers", got)
	}
	if err := orch.deepScan(context.Background()); err == nil {
		t.Fatal("deepScan() error = nil, want unavailable market data error")
	}
	if got := singleJobStatus(t, orch, "deep_scan").LastSummary["selected"]; got != 2 {
		t.Fatalf("deep scan selected = %d, want 2 operational tickers", got)
	}
	if repo.listCalls != 0 {
		t.Fatalf("history/deep scans queried full active universe %d times", repo.listCalls)
	}
	if repo.limit != 7 {
		t.Fatalf("operational watchlist limit = %d, want 7", repo.limit)
	}
}

package automation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const defaultHistoryRefreshWatchlistLimit = 250

type operationalTickerSelection struct {
	Tickers    []string
	Positions  int
	Strategies int
	Watchlist  int
}

func (o *JobOrchestrator) selectOperationalStockTickers(ctx context.Context) (operationalTickerSelection, error) {
	if o.deps.PositionRepo == nil {
		return operationalTickerSelection{}, fmt.Errorf("position repository unavailable")
	}
	positions, err := listAllOpenPositions(ctx, o.deps.PositionRepo)
	if err != nil {
		return operationalTickerSelection{}, fmt.Errorf("list open positions: %w", err)
	}
	if o.deps.StrategyRepo == nil {
		return operationalTickerSelection{}, fmt.Errorf("strategy repository unavailable")
	}
	strategies, err := listAllStrategies(ctx, o.deps.StrategyRepo, repository.StrategyFilter{
		Status:     domain.StrategyStatusActive,
		MarketType: domain.MarketTypeStock,
	})
	if err != nil {
		return operationalTickerSelection{}, fmt.Errorf("list active stock strategies: %w", err)
	}
	if o.deps.Universe == nil {
		return operationalTickerSelection{}, fmt.Errorf("universe unavailable")
	}
	watchlist, err := o.deps.Universe.GetWatchlist(ctx, o.deps.HistoryRefreshWatchlistLimit)
	if err != nil {
		return operationalTickerSelection{}, fmt.Errorf("get watchlist: %w", err)
	}

	selection := operationalTickerSelection{}
	seen := make(map[string]struct{}, len(positions)+len(strategies)+len(watchlist))
	normalize := func(raw string) string {
		ticker := strings.ToUpper(strings.TrimSpace(raw))
		if ticker == "" {
			return ""
		}
		return ticker
	}
	add := func(ticker string) {
		if _, exists := seen[ticker]; exists {
			return
		}
		seen[ticker] = struct{}{}
		selection.Tickers = append(selection.Tickers, ticker)
	}
	positionTickers := make(map[string]struct{})
	for _, position := range positions {
		if ticker := normalize(position.Ticker); operationalStockPosition(position) && ticker != "" {
			positionTickers[ticker] = struct{}{}
			add(ticker)
		}
	}
	selection.Positions = len(positionTickers)
	strategyTickers := make(map[string]struct{})
	for _, strategy := range strategies {
		if ticker := normalize(strategy.Ticker); strategy.Status == domain.StrategyStatusActive && strategy.MarketType.Normalize() == domain.MarketTypeStock && ticker != "" {
			strategyTickers[ticker] = struct{}{}
			add(ticker)
		}
	}
	selection.Strategies = len(strategyTickers)
	watchlistTickers := make(map[string]struct{})
	for _, tracked := range watchlist {
		if ticker := normalize(tracked.Ticker); ticker != "" {
			watchlistTickers[ticker] = struct{}{}
			add(ticker)
		}
	}
	selection.Watchlist = len(watchlistTickers)
	sort.Strings(selection.Tickers)
	return selection, nil
}

func operationalStockPosition(position domain.Position) bool {
	if position.AssetClass == domain.AssetClassOption {
		return false
	}
	if position.MarketType.Normalize() == domain.MarketTypeStock {
		return true
	}
	return position.MarketType == "" && position.AssetClass == domain.AssetClassEquity && position.StrategyID == nil
}

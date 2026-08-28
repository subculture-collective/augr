package execution

import (
	"context"
	"fmt"
	"math"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

const riskSnapshotPositionLimit = 1000

// BuildRiskPortfolioSnapshot captures the current portfolio exposure needed for
// truthful pre-trade risk checks and status reporting.
func BuildRiskPortfolioSnapshot(ctx context.Context, account domain.ExecutionAccountBinding, broker Broker, positionRepo repository.PositionRepository) (risk.Portfolio, error) {
	if broker == nil {
		return risk.Portfolio{}, fmt.Errorf("broker is required")
	}

	balance, err := broker.GetAccountBalance(ctx)
	if err != nil {
		return risk.Portfolio{}, fmt.Errorf("get account balance: %w", err)
	}

	return BuildRiskPortfolioSnapshotFromBalance(ctx, account, balance, positionRepo)
}

// BuildRiskPortfolioSnapshotFromBalance converts the current open positions into
// the exposure shape expected by the risk engine using the provided account equity.
func BuildRiskPortfolioSnapshotFromBalance(ctx context.Context, account domain.ExecutionAccountBinding, balance Balance, positionRepo repository.PositionRepository) (risk.Portfolio, error) {
	if positionRepo == nil {
		return risk.Portfolio{}, fmt.Errorf("position repository is required")
	}

	if err := account.Validate(); err != nil {
		return risk.Portfolio{}, fmt.Errorf("execution account: %w", err)
	}
	scoped, ok := positionRepo.(repository.AccountScopedPositionRepository)
	if !ok {
		return risk.Portfolio{}, fmt.Errorf("account-scoped position repository is required")
	}
	var positions []domain.Position
	for offset := 0; ; offset += riskSnapshotPositionLimit {
		page, err := scoped.GetOpenByAccount(ctx, account.AccountID(), account.Environment(), repository.PositionFilter{}, riskSnapshotPositionLimit, offset)
		if err != nil {
			return risk.Portfolio{}, fmt.Errorf("get open positions: %w", err)
		}
		for _, position := range page {
			if position.AccountID != account.AccountID() || position.Environment != account.Environment() {
				return risk.Portfolio{}, fmt.Errorf("position %s escaped account scope", position.ID)
			}
		}
		positions = append(positions, page...)
		if len(page) < riskSnapshotPositionLimit {
			break
		}
	}

	return BuildRiskPortfolioSnapshotFromPositions(balance, positions)
}

func BuildRiskPortfolioSnapshotFromPositions(balance Balance, positions []domain.Position) (risk.Portfolio, error) {
	portfolio := risk.Portfolio{
		ConcurrentPositions:      len(positions),
		PositionExposureBySymbol: make(map[string]float64, len(positions)),
		MarketExposurePct:        make(map[domain.MarketType]float64, len(positions)),
	}
	if len(positions) == 0 {
		return portfolio, nil
	}
	if balance.Equity <= 0 {
		return risk.Portfolio{}, fmt.Errorf("account equity must be positive")
	}

	for _, position := range positions {
		notional, err := positionNotional(position)
		if err != nil {
			return risk.Portfolio{}, err
		}
		exposure := notional / balance.Equity
		portfolio.TotalExposurePct += exposure
		portfolio.PositionExposureBySymbol[position.Ticker] += exposure
		if position.MarketType != "" {
			portfolio.MarketExposurePct[position.MarketType] += exposure
		}
	}

	return portfolio, nil
}

func positionNotional(position domain.Position) (float64, error) {
	if position.Ticker == "" {
		return 0, fmt.Errorf("position ticker is required")
	}
	if position.Quantity == 0 {
		return 0, nil
	}

	price := position.AvgEntry
	if position.CurrentPrice != nil && *position.CurrentPrice > 0 {
		price = *position.CurrentPrice
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, fmt.Errorf("position %s has invalid price", position.Ticker)
	}

	quantity := math.Abs(position.Quantity)
	if math.IsNaN(quantity) || math.IsInf(quantity, 0) {
		return 0, fmt.Errorf("position %s has invalid quantity", position.Ticker)
	}

	multiplier := 1.0
	if position.AssetClass == domain.AssetClassOption {
		multiplier = position.ContractMultiplier
		if multiplier <= 0 {
			multiplier = 100
		}
	}
	return quantity * price * multiplier, nil
}

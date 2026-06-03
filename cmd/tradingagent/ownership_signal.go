package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const ownershipLookupLimit = 10_000

func normalizeUnownedSellSignal(
	ctx context.Context,
	positionRepo repository.PositionRepository,
	strategy domain.Strategy,
	ticker string,
	signal domain.PipelineSignal,
	logger *slog.Logger,
) (domain.PipelineSignal, error) {
	if signal != domain.PipelineSignalSell || strategy.MarketType.Normalize() != domain.MarketTypeStock {
		return signal, nil
	}

	owned, err := hasOpenLongPositionForStrategy(ctx, positionRepo, strategy.ID, ticker)
	if err != nil {
		return signal, err
	}
	if owned {
		return signal, nil
	}

	if logger != nil {
		logger.InfoContext(ctx, "normalizing unowned stock sell signal to hold", "ticker", ticker, "strategy_id", strategy.ID)
	}

	return domain.PipelineSignalHold, nil
}

func hasOpenLongPositionForStrategy(ctx context.Context, positionRepo repository.PositionRepository, strategyID uuid.UUID, ticker string) (bool, error) {
	positions, err := positionRepo.GetByStrategy(ctx, strategyID, repository.PositionFilter{
		Ticker: ticker,
		Side:   domain.PositionSideLong,
	}, ownershipLookupLimit, 0)
	if err != nil {
		return false, fmt.Errorf("get open long position for %s: %w", ticker, err)
	}

	for _, position := range positions {
		if position.ClosedAt == nil && position.Quantity > 0 {
			return true, nil
		}
	}

	return false, nil
}

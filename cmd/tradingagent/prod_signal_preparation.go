package main

import (
	"context"
	"fmt"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

type strategySignalProcessor interface {
	ProcessSignal(context.Context, execution.ExecutionScope, execution.FinalSignal, execution.TradingPlan) error
	ProcessSignalWithPreparation(context.Context, execution.ExecutionScope, execution.FinalSignal, execution.TradingPlan, execution.SignalOrderPreparation) error
}

// processStrategySignal keeps stock preparation separate from native prediction
// and live broker routes. A configured evidence owner must never fall back to
// unprepared execution when its evidence is missing or invalid.
func (r *realStrategyRunner) processStrategySignal(ctx context.Context, manager strategySignalProcessor, scope execution.ExecutionScope, signal execution.FinalSignal, plan execution.TradingPlan) error {
	if r.preparePaperStockSignal == nil || scope.Environment() != domain.AccountEnvironmentPaperScored || plan.MarketType.Normalize() != domain.MarketTypeStock || signal.Signal == domain.PipelineSignalHold {
		return manager.ProcessSignal(ctx, scope, signal, plan)
	}
	preparation, err := r.preparePaperStockSignal(ctx, scope, plan)
	if err != nil {
		return fmt.Errorf("prepare paper stock signal: %w", err)
	}
	if preparation == nil {
		return fmt.Errorf("prepare paper stock signal: evidence owner returned nil preparation")
	}
	return manager.ProcessSignalWithPreparation(ctx, scope, signal, plan, preparation)
}

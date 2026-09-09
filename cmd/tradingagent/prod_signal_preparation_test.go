package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/google/uuid"
)

type recordingStrategySignalProcessor struct{ legacy, prepared int }

func (m *recordingStrategySignalProcessor) ProcessSignal(context.Context, execution.ExecutionScope, execution.FinalSignal, execution.TradingPlan) error {
	m.legacy++
	return nil
}

func (m *recordingStrategySignalProcessor) ProcessSignalWithPreparation(context.Context, execution.ExecutionScope, execution.FinalSignal, execution.TradingPlan, execution.SignalOrderPreparation) error {
	m.prepared++
	return nil
}

type fixtureStockPreparation struct{}

func (*fixtureStockPreparation) Resolve(context.Context, execution.ExecutionScope, execution.FinalSignal, execution.TradingPlan, float64) (uuid.UUID, float64, error) {
	return uuid.Nil, 0, errors.New("dispatch test must not resolve execution")
}

func (*fixtureStockPreparation) PersistApproved(context.Context, execution.ExecutionScope, domain.Order) error {
	return errors.New("dispatch test must not write execution")
}

func TestProcessStrategySignalPreparationDispatch(t *testing.T) {
	scope, err := execution.NewStrategyExecutionScope(uuid.New(), domain.AccountEnvironmentPaperScored, uuid.New(), domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"prepared", "factory_error", "nil_preparation", "unconfigured", "hold", "prediction"} {
		t.Run(name, func(t *testing.T) {
			manager := &recordingStrategySignalProcessor{}
			called := 0
			sentinel := errors.New("missing retained facts")
			runner := &realStrategyRunner{preparePaperStockSignal: func(context.Context, execution.ExecutionScope, execution.TradingPlan) (execution.SignalOrderPreparation, error) {
				called++
				if name == "factory_error" {
					return nil, sentinel
				}
				if name == "nil_preparation" {
					return nil, nil
				}
				return &fixtureStockPreparation{}, nil
			}}
			signal := execution.FinalSignal{Signal: domain.PipelineSignalBuy}
			plan := execution.TradingPlan{MarketType: domain.MarketTypeStock, Ticker: "SPY"}
			if name == "unconfigured" {
				runner.preparePaperStockSignal = nil
			}
			if name == "hold" {
				signal.Signal = domain.PipelineSignalHold
			}
			if name == "prediction" {
				plan.MarketType = domain.MarketTypeKalshi
			}
			err := runner.processStrategySignal(t.Context(), manager, scope, signal, plan)
			switch name {
			case "factory_error", "nil_preparation":
				if err == nil || called != 1 || manager.legacy != 0 || manager.prepared != 0 {
					t.Fatalf("failed preparation dispatched: error=%v factory=%d manager=%+v", err, called, manager)
				}
				if name == "factory_error" && !errors.Is(err, sentinel) {
					t.Fatal("factory error lost its cause")
				}
			case "prepared":
				if err != nil || called != 1 || manager.prepared != 1 || manager.legacy != 0 {
					t.Fatalf("prepared dispatch: error=%v factory=%d manager=%+v", err, called, manager)
				}
			default:
				if err != nil || called != 0 || manager.legacy != 1 || manager.prepared != 0 {
					t.Fatalf("legacy dispatch: error=%v factory=%d manager=%+v", err, called, manager)
				}
			}
		})
	}
}

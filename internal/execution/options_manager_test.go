package execution_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

type mockOptionsBroker struct {
	submitOptionOrderFn func(ctx context.Context, order *domain.Order) (string, error)
	submitSpreadOrderFn func(ctx context.Context, spread *domain.OptionSpread, quantity float64) ([]string, error)
}

func (b *mockOptionsBroker) SubmitOptionOrder(ctx context.Context, order *domain.Order) (string, error) {
	if b.submitOptionOrderFn != nil {
		return b.submitOptionOrderFn(ctx, order)
	}
	return "opt-ext-123", nil
}

func (b *mockOptionsBroker) SubmitSpreadOrder(ctx context.Context, spread *domain.OptionSpread, quantity float64) ([]string, error) {
	if b.submitSpreadOrderFn != nil {
		return b.submitSpreadOrderFn(ctx, spread, quantity)
	}
	return []string{"leg-1"}, nil
}

func newTestOptionsManager(broker *mockOptionsBroker, orderRepo *mockOrderRepo, positionRepo *mockPositionRepo, tradeRepo *mockTradeRepo, riskEng *mockRiskEngine) *execution.OptionsOrderManager {
	return execution.NewOptionsOrderManager(broker, orderRepo, positionRepo, tradeRepo, riskEng, slog.Default())
}

func TestProcessOptionSignal_PaperPathStillSubmits(t *testing.T) {
	broker := &mockOptionsBroker{}
	orderRepo := &mockOrderRepo{}
	positionRepo := &mockPositionRepo{}
	tradeRepo := &mockTradeRepo{}
	riskEng := &mockRiskEngine{}

	mgr := newTestOptionsManager(broker, orderRepo, positionRepo, tradeRepo, riskEng)
	plan := execution.TradingPlan{Ticker: "AAPL", EntryType: "market", EntryPrice: 2.5, PositionSize: 1}
	err := mgr.ProcessOptionSignal(context.Background(), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, plan, uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("ProcessOptionSignal() unexpected error: %v", err)
	}
	if len(orderRepo.orders) != 1 {
		t.Fatalf("expected 1 order created, got %d", len(orderRepo.orders))
	}
	if len(orderRepo.updates) != 1 {
		t.Fatalf("expected 1 order update, got %d", len(orderRepo.updates))
	}
	if got := orderRepo.updates[0].Status; got != domain.OrderStatusSubmitted {
		t.Fatalf("order update status = %s, want %s", got, domain.OrderStatusSubmitted)
	}
}

func TestProcessOptionSignal_LiveGateAllowsConfiguredBrokerName(t *testing.T) {
	strategyID := uuid.New()
	broker := &mockOptionsBroker{}
	orderRepo := &mockOrderRepo{}
	positionRepo := &mockPositionRepo{}
	tradeRepo := &mockTradeRepo{}
	riskEng := &mockRiskEngine{}

	mgr := newTestOptionsManager(broker, orderRepo, positionRepo, tradeRepo, riskEng).
		WithBrokerName("alpaca").
		WithLiveTrading(true).
		WithLiveGate(execution.LiveGateConfig{
			EnableLiveTrading: true,
			AllowedStrategies: map[uuid.UUID]bool{strategyID: true},
			AllowedBrokers:    map[string]bool{"alpaca": true},
		})

	err := mgr.ProcessOptionSignal(context.Background(), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL", EntryType: "market", EntryPrice: 2.5, PositionSize: 1}, strategyID, uuid.New())
	if err != nil {
		t.Fatalf("ProcessOptionSignal() unexpected error: %v", err)
	}
	if len(orderRepo.orders) != 1 {
		t.Fatalf("expected 1 order created, got %d", len(orderRepo.orders))
	}
	if len(orderRepo.updates) != 1 {
		t.Fatalf("expected 1 order update, got %d", len(orderRepo.updates))
	}
	if got := orderRepo.updates[0].Status; got != domain.OrderStatusSubmitted {
		t.Fatalf("order update status = %s, want %s", got, domain.OrderStatusSubmitted)
	}
}

func TestProcessOptionSignal_LiveGateDenies(t *testing.T) {
	broker := &mockOptionsBroker{}
	orderRepo := &mockOrderRepo{}
	positionRepo := &mockPositionRepo{}
	tradeRepo := &mockTradeRepo{}
	riskEng := &mockRiskEngine{}

	mgr := newTestOptionsManager(broker, orderRepo, positionRepo, tradeRepo, riskEng).
		WithLiveTrading(true).
		WithLiveGate(execution.LiveGateConfig{EnableLiveTrading: true, AllowedBrokers: map[string]bool{"alpaca": true}})

	err := mgr.ProcessOptionSignal(context.Background(), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL", EntryType: "market", EntryPrice: 2.5, PositionSize: 1}, uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected live gate error")
	}
	if len(orderRepo.orders) != 0 {
		t.Fatalf("expected 0 orders, got %d", len(orderRepo.orders))
	}
}

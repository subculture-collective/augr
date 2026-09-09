package execution_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/paper"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
	"github.com/google/uuid"
)

func TestAllocatorPreparedSignalPersistsAfterRiskAndBeforeFill(t *testing.T) {
	orders, positions, trades := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	orderID := uuid.New()
	riskChecks, writes, checks := 0, 0, 0
	engine := &mockRiskEngine{checkPreTradeFn: func(_ context.Context, order *domain.Order, _ risk.Portfolio) (bool, string, error) {
		riskChecks++
		if writes != 0 || order.ID != orderID {
			t.Fatal("allocator risk did not precede canonical persistence")
		}
		return true, "approved", nil
	}}
	writer := &preparedSignalWriter{testAcceptedOrderFillWriter: &testAcceptedOrderFillWriter{orders: orders, positions: positions, trades: trades}, check: func(order *domain.Order) error {
		checks++
		if writes != 1 || order.ID != orderID {
			t.Fatal("allocator checker did not see persisted command")
		}
		return nil
	}}
	processor := portfolio.NewPaperOrderManagerProcessor(portfolio.PaperOrderManagerProcessorDeps{
		OrderRepo: orders, PositionRepo: positions, TradeRepo: trades, AuditLogRepo: &mockAuditLogRepo{}, RiskEngine: engine, EconomicWriter: writer,
		InitialBalance: 100000, PaperBroker: paper.NewPaperBroker(100000, 0, 0),
		PrepareSignal: func(context.Context, portfolio.PaperOrderRequest) (execution.SignalOrderPreparation, error) {
			return signalPreparationStub{resolve: func(requested float64) (uuid.UUID, float64, error) { return orderID, requested, nil }, persist: func(domain.Order) error {
				if riskChecks != 1 {
					t.Fatal("allocator persisted before real risk check")
				}
				writes++
				return nil
			}}, nil
		},
	})
	_, err := processor.ProcessPaperOrder(context.Background(), portfolio.PaperOrderRequest{NotionalUSD: 2000, Scope: strategyScope(uuid.New(), uuid.New()), Signal: defaultSignal(), Plan: defaultPlan()})
	if err != nil {
		t.Fatal(err)
	}
	if riskChecks != 1 || writes != 1 || checks < 1 || len(orders.orders) != 1 || len(trades.trades) != 1 || len(positions.positions) != 1 {
		t.Fatalf("risk=%d writes=%d checks=%d orders=%d trades=%d positions=%d", riskChecks, writes, checks, len(orders.orders), len(trades.trades), len(positions.positions))
	}
}

type lostAllocatorClaim struct {
	repository.OpportunityRepository
	renewals int
}

func (r *lostAllocatorClaim) RenewAllocationClaim(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) {
	r.renewals++
	return false, nil
}

func TestAllocatorPreparedSignalCannotFillAfterClaimLost(t *testing.T) {
	orders, positions, trades := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	claims := &lostAllocatorClaim{}
	writes := 0
	writer := &preparedSignalWriter{testAcceptedOrderFillWriter: &testAcceptedOrderFillWriter{orders: orders, positions: positions, trades: trades}, check: func(*domain.Order) error { return nil }}
	processor := portfolio.NewPaperOrderManagerProcessor(portfolio.PaperOrderManagerProcessorDeps{
		OrderRepo: orders, PositionRepo: positions, TradeRepo: trades, AuditLogRepo: &mockAuditLogRepo{}, RiskEngine: &mockRiskEngine{}, EconomicWriter: writer,
		OpportunityRepo: claims, InitialBalance: 100000, PaperBroker: paper.NewPaperBroker(100000, 0, 0),
		PrepareSignal: func(context.Context, portfolio.PaperOrderRequest) (execution.SignalOrderPreparation, error) {
			return signalPreparationStub{resolve: func(requested float64) (uuid.UUID, float64, error) { return uuid.New(), requested, nil }, persist: func(domain.Order) error { writes++; return nil }}, nil
		},
	})
	_, err := processor.ProcessPaperOrder(context.Background(), portfolio.PaperOrderRequest{NotionalUSD: 2000, Scope: strategyScope(uuid.New(), uuid.New()), Signal: defaultSignal(), Plan: defaultPlan(), OpportunityID: uuid.New(), ClaimID: uuid.New()})
	if err == nil || !strings.Contains(err.Error(), "claim ownership lost") || claims.renewals == 0 {
		t.Fatalf("claim not fenced: %v renewals=%d", err, claims.renewals)
	}
	if writes != 0 || len(orders.orders) != 0 || len(trades.trades) != 0 || len(positions.positions) != 0 {
		t.Fatal("lost claim produced canonical or financial effects")
	}
}

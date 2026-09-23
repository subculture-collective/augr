package execution_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

// Unprepared orders (no canonical routed command) must still dispatch and fill.
func TestProcessSignal_UnpreparedOrderFillsWithoutCanonicalGate(t *testing.T) {
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	writer := &testAcceptedOrderFillWriter{orders: orderRepo, positions: positionRepo, trades: tradeRepo}
	mgr := execution.NewOrderManager(&mockBroker{}, "paper", &mockRiskEngine{}, positionRepo, orderRepo, tradeRepo, &mockAuditLogRepo{}, nil, execution.SizingConfig{Method: execution.PositionSizingMethodFixedFractional, FractionPct: 0.02}, nil).
		WithAcceptedOrderFillWriter(writer)

	if err := mgr.ProcessSignal(context.Background(), strategyScope(uuid.New(), uuid.New()), defaultSignal(), defaultPlan()); err != nil {
		t.Fatalf("ProcessSignal() error = %v", err)
	}
	if writer.preparedChecks != 0 {
		t.Fatalf("preparation checks = %d, want 0 for an unprepared order", writer.preparedChecks)
	}
	if len(orderRepo.orders) != 1 || len(tradeRepo.trades) != 1 || len(positionRepo.positions) != 1 {
		t.Fatalf("orders=%d trades=%d positions=%d, want one each", len(orderRepo.orders), len(tradeRepo.trades), len(positionRepo.positions))
	}
}

// Prepared-path orders still require the canonical routed command.
func TestProcessPreparedSignal_StillRequiresPreparation(t *testing.T) {
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	writer := &testAcceptedOrderFillWriter{orders: orderRepo, positions: positionRepo, trades: tradeRepo, prepared: map[uuid.UUID]bool{}}
	submissions := 0
	broker := &mockBroker{submitOrderFn: func(context.Context, *domain.Order) (string, error) { submissions++; return "ext-1", nil }}
	mgr := execution.NewOrderManager(broker, "paper", &mockRiskEngine{}, positionRepo, orderRepo, tradeRepo, &mockAuditLogRepo{}, nil, execution.SizingConfig{Method: execution.PositionSizingMethodFixedFractional, FractionPct: 0.02}, nil).
		WithAcceptedOrderFillWriter(writer)

	orderID := uuid.New()
	err := mgr.ProcessPreparedSignal(context.Background(), strategyScope(uuid.New(), uuid.New()), defaultSignal(), defaultPlan(), orderID)
	if err == nil || !errors.Is(err, execution.ErrAcceptedOrderNotPrepared) || !strings.Contains(err.Error(), "not prepared") {
		t.Fatalf("unprepared canonical dispatch error = %v, want preparation failure", err)
	}
	if submissions != 0 || len(orderRepo.orders) != 0 {
		t.Fatalf("unprepared canonical dispatch reached broker or persistence: submissions=%d orders=%d", submissions, len(orderRepo.orders))
	}

	writer.prepared[orderID] = true
	if err := mgr.ProcessPreparedSignal(context.Background(), strategyScope(uuid.New(), uuid.New()), defaultSignal(), defaultPlan(), orderID); err != nil {
		t.Fatalf("prepared dispatch error = %v", err)
	}
	if writer.preparedChecks < 2 || submissions != 1 || len(orderRepo.orders) != 1 || orderRepo.orders[0].ID != orderID {
		t.Fatalf("prepared dispatch checks=%d submissions=%d orders=%d", writer.preparedChecks, submissions, len(orderRepo.orders))
	}
}

func TestProcessSignal_ZeroSizeRecordsRejectedDecision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		equity float64
		price  float64
		reason string
	}{
		{name: "zero equity", equity: 0, price: 150, reason: "position_size_zero"},
		{name: "invalid entry price", equity: 100000, price: 0, reason: "entry_price_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &mockDecisionRecorder{}
			broker := &mockBroker{getAccountBalanceFn: func(context.Context) (execution.Balance, error) {
				return execution.Balance{Currency: "USD", Equity: tc.equity, Cash: tc.equity, BuyingPower: tc.equity}, nil
			}}
			orderRepo := &mockOrderRepo{}
			mgr := newTestOrderManager(broker, &mockRiskEngine{}, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockAuditLogRepo{}).WithDecisionRecorder(recorder)
			plan := defaultPlan()
			plan.EntryPrice = tc.price
			err := mgr.ProcessSignal(context.Background(), strategyScope(uuid.New(), uuid.New()), defaultSignal(), plan)
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error = %v, want reason %s", err, tc.reason)
			}
			if len(orderRepo.orders) != 0 {
				t.Fatalf("orders = %d, want none", len(orderRepo.orders))
			}
			if len(recorder.decisions) != 1 || recorder.decisions[0].Status != domain.TradeDecisionStatusRejected || !slices.Contains(recorder.decisions[0].RiskReasons, tc.reason) {
				t.Fatalf("decisions = %+v, want one rejected decision with %s", recorder.decisions, tc.reason)
			}
		})
	}
}

func TestNonRunEffectIdentityDoesNotCollideAcrossMinutes(t *testing.T) {
	scope, err := execution.NewNonRunExecutionScope(testExecutionAccountBinding.AccountID(), testExecutionAccountBinding.Environment(), ledger.ExecutionOriginOperator, "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	mgr := newTestOrderManager(&mockBroker{}, &mockRiskEngine{}, orderRepo, positionRepo, tradeRepo, &mockAuditLogRepo{})
	base := time.Date(2026, 9, 23, 14, 0, 0, 0, time.UTC)
	mgr.SetNowFunc(func() time.Time { return base })
	if err := mgr.ProcessSignal(context.Background(), scope, defaultSignal(), defaultPlan()); err != nil {
		t.Fatalf("first operator order error = %v", err)
	}
	mgr.SetNowFunc(func() time.Time { return base.Add(2 * time.Minute) })
	if err := mgr.ProcessSignal(context.Background(), scope, defaultSignal(), defaultPlan()); err != nil {
		t.Fatalf("second operator order error = %v", err)
	}
	if len(orderRepo.orders) != 2 || orderRepo.orders[0].ID == orderRepo.orders[1].ID {
		t.Fatalf("operator orders = %d with ids %v, want two distinct identities", len(orderRepo.orders), []uuid.UUID{orderRepo.orders[0].ID, orderRepo.orders[len(orderRepo.orders)-1].ID})
	}
}

func TestReconcilePersistedOrderRefreshesRestingOrderUntilMaxResting(t *testing.T) {
	for _, tc := range []struct {
		name        string
		submittedAt time.Duration
		wantCancel  int
		wantStatus  domain.OrderStatus
	}{
		{name: "fresh resting order is refreshed", submittedAt: -30 * time.Minute, wantCancel: 0, wantStatus: domain.OrderStatusSubmitted},
		{name: "expired resting order is cancelled", submittedAt: -9 * time.Hour, wantCancel: 1, wantStatus: domain.OrderStatusCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := strategyScope(uuid.New(), uuid.New())
			originType, originID := scope.Origin()
			run, _ := scope.PipelineRun()
			submittedAt := time.Now().UTC().Add(tc.submittedAt)
			order := &domain.Order{ID: uuid.New(), AccountID: scope.AccountID(), Environment: scope.Environment(), OriginType: string(originType), OriginID: originID, PipelineRunID: &run.ID, PipelineRunTradeDate: &run.TradeDate, ExternalID: "paper-resting", Ticker: "AAPL", MarketType: domain.MarketTypeStock, Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 1, Status: domain.OrderStatusSubmitted, SubmittedAt: &submittedAt, CreatedAt: submittedAt}
			cancels := 0
			broker := &mockBroker{
				getOrderResultFn: func(context.Context, string) (execution.BrokerOrderStatus, error) {
					if cancels > 0 {
						return execution.BrokerOrderStatus{Status: domain.OrderStatusCancelled}, nil
					}
					return execution.BrokerOrderStatus{Status: domain.OrderStatusSubmitted}, nil
				},
				cancelOrderFn: func(context.Context, string) error { cancels++; return nil },
			}
			orderRepo := &mockOrderRepo{getFn: func(context.Context, uuid.UUID) (*domain.Order, error) { cloned := *order; return &cloned, nil }}
			mgr := newTestOrderManager(broker, &mockRiskEngine{}, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockAuditLogRepo{}).WithDecisionRecorder(&recoveryDecisionRecorder{decisionID: uuid.New()})
			status, err := mgr.ReconcilePersistedOrder(context.Background(), scope, order)
			if err != nil || status != tc.wantStatus || cancels != tc.wantCancel {
				t.Fatalf("status=%s cancels=%d err=%v, want %s/%d", status, cancels, err, tc.wantStatus, tc.wantCancel)
			}
			if len(orderRepo.updates) == 0 || orderRepo.updates[len(orderRepo.updates)-1].Status != tc.wantStatus {
				t.Fatalf("persisted updates = %+v, want final status %s", orderRepo.updates, tc.wantStatus)
			}
		})
	}
}

func TestWithMaxRestingDurationOverridesDefault(t *testing.T) {
	scope := strategyScope(uuid.New(), uuid.New())
	originType, originID := scope.Origin()
	run, _ := scope.PipelineRun()
	submittedAt := time.Now().UTC().Add(-10 * time.Minute)
	order := &domain.Order{ID: uuid.New(), AccountID: scope.AccountID(), Environment: scope.Environment(), OriginType: string(originType), OriginID: originID, PipelineRunID: &run.ID, PipelineRunTradeDate: &run.TradeDate, ExternalID: "paper-resting", Ticker: "AAPL", MarketType: domain.MarketTypeStock, Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 1, Status: domain.OrderStatusSubmitted, SubmittedAt: &submittedAt, CreatedAt: submittedAt}
	cancels := 0
	broker := &mockBroker{
		getOrderResultFn: func(context.Context, string) (execution.BrokerOrderStatus, error) {
			if cancels > 0 {
				return execution.BrokerOrderStatus{Status: domain.OrderStatusCancelled}, nil
			}
			return execution.BrokerOrderStatus{Status: domain.OrderStatusSubmitted}, nil
		},
		cancelOrderFn: func(context.Context, string) error { cancels++; return nil },
	}
	orderRepo := &mockOrderRepo{getFn: func(context.Context, uuid.UUID) (*domain.Order, error) { cloned := *order; return &cloned, nil }}
	mgr := newTestOrderManager(broker, &mockRiskEngine{}, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockAuditLogRepo{}).WithDecisionRecorder(&recoveryDecisionRecorder{decisionID: uuid.New()}).WithMaxRestingDuration(5 * time.Minute)
	status, err := mgr.ReconcilePersistedOrder(context.Background(), scope, order)
	if err != nil || status != domain.OrderStatusCancelled || cancels != 1 {
		t.Fatalf("status=%s cancels=%d err=%v, want cancelled after a 5m limit", status, cancels, err)
	}
}

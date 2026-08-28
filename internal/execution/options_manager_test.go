package execution_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/paper"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

type mockOptionsBroker struct {
	submitOptionOrderFn func(ctx context.Context, order *domain.Order) (string, error)
	submitSpreadOrderFn func(ctx context.Context, spread *domain.OptionSpread, quantity float64, clientOrderID string) ([]string, error)
	optionFillReportFn  func(ctx context.Context, order *domain.Order) (execution.OptionFillReport, error)
	getOrderStatusFn    func(context.Context, string) (execution.BrokerOrderStatus, error)
	spreadStatusFn      func(context.Context, string) (execution.BrokerSpreadOrderStatus, error)
}

func (b *mockOptionsBroker) GetOrderStatusResult(ctx context.Context, id string) (execution.BrokerOrderStatus, error) {
	if b.getOrderStatusFn != nil {
		return b.getOrderStatusFn(ctx, id)
	}
	return execution.BrokerOrderStatus{}, execution.ErrBrokerOrderNotFound
}

func (b *mockOptionsBroker) GetOrderStatusByClientOrderIDResult(ctx context.Context, id string) (string, execution.BrokerOrderStatus, error) {
	result, err := b.GetOrderStatusResult(ctx, id)
	return id, result, err
}

func (b *mockOptionsBroker) GetSpreadOrderStatusByClientOrderIDResult(ctx context.Context, id string) (execution.BrokerSpreadOrderStatus, error) {
	if b.spreadStatusFn != nil {
		return b.spreadStatusFn(ctx, id)
	}
	status, err := b.GetOrderStatusResult(ctx, id)
	if err != nil {
		return execution.BrokerSpreadOrderStatus{}, err
	}
	return execution.BrokerSpreadOrderStatus{ParentExternalID: id, Legs: []execution.BrokerSpreadLegStatus{{ExternalID: "leg-1", Status: status}, {ExternalID: "leg-2", Status: status}}}, nil
}

type malformedAsyncSpreadBroker struct{}

func (malformedAsyncSpreadBroker) SubmitOptionOrder(context.Context, *domain.Order) (string, error) {
	return "", nil
}
func (malformedAsyncSpreadBroker) SubmitSpreadOrder(context.Context, *domain.OptionSpread, float64, string) ([]string, error) {
	return []string{"only-one"}, nil
}
func (malformedAsyncSpreadBroker) PreflightSpread(context.Context, *domain.OptionSpread, float64) error {
	return nil
}
func (malformedAsyncSpreadBroker) GetAccountBalance(context.Context) (execution.Balance, error) {
	return execution.Balance{Cash: 100000, BuyingPower: 100000, Equity: 100000}, nil
}

type recordingOptionFillRepo struct {
	mu               sync.Mutex
	batches          [][]repository.OptionFillInput
	err              error
	resolveCommitted bool
	resolveErr       error
}

func (r *recordingOptionFillRepo) ResolveOptionFillCommit(_ context.Context, inputs []repository.OptionFillInput) ([]repository.OptionFillResult, bool, error) {
	if r.resolveErr != nil || !r.resolveCommitted {
		return nil, false, r.resolveErr
	}
	results := make([]repository.OptionFillResult, len(inputs))
	for i := range inputs {
		results[i] = repository.OptionFillResult{OrderID: inputs[i].Order.ID, PositionID: uuid.New(), TradeID: uuid.New()}
	}
	return results, true, nil
}

func (r *recordingOptionFillRepo) ApplyOptionFills(_ context.Context, inputs []repository.OptionFillInput) ([]repository.OptionFillResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	batch := append([]repository.OptionFillInput(nil), inputs...)
	r.batches = append(r.batches, batch)
	results := make([]repository.OptionFillResult, len(inputs))
	for index, input := range inputs {
		positionID := uuid.New()
		if input.PositionID != nil {
			positionID = *input.PositionID
		}
		results[index] = repository.OptionFillResult{OrderID: input.Order.ID, PositionID: positionID, TradeID: uuid.New()}
	}
	return results, nil
}

func TestReconcileCancelledOptionAppliesPartialFillBeforeTerminalStatus(t *testing.T) {
	price, filledAt := 2.5, time.Now().UTC()
	strategyID := uuid.New()
	optionType, strike := domain.OptionTypeCall, 150.0
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	intent := domain.PositionIntentBuyToOpen
	order := domain.Order{ID: uuid.New(), AccountID: testExecutionAccountBinding.AccountID(), Environment: testExecutionAccountBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), StrategyID: &strategyID, ClientOrderID: "partial-cancel", ExternalID: "alpaca-partial", Ticker: "AAPL271217C00150000", MarketType: domain.MarketTypeOptions, AssetClass: domain.AssetClassOption, UnderlyingTicker: "AAPL", OptionType: &optionType, Strike: &strike, Expiry: &expiry, ContractMultiplier: 100, PositionIntent: &intent, Side: domain.OrderSideBuy, Quantity: 2, FilledQuantity: 1, FilledAvgPrice: &price, FilledAt: &filledAt, Status: domain.OrderStatusCancelled, Broker: "alpaca", SubmittedAt: &filledAt}
	broker := &mockOptionsBroker{getOrderStatusFn: func(context.Context, string) (execution.BrokerOrderStatus, error) {
		return execution.BrokerOrderStatus{Status: domain.OrderStatusCancelled, FilledQuantity: 1, FilledAvgPrice: &price, FilledAt: &filledAt}, nil
	}}
	fillRepo := &recordingOptionFillRepo{}
	orderRepo := &mockOrderRepo{getFn: func(context.Context, uuid.UUID) (*domain.Order, error) { copy := order; return &copy, nil }}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{}, fillRepo)
	if err := mgr.ReconcilePendingOptionOrders(context.Background(), testExecutionAccountBinding, []domain.Order{order}, nil); err != nil {
		t.Fatal(err)
	}
	if len(fillRepo.batches) != 1 || fillRepo.batches[0][0].FillQuantity != 1 || fillRepo.batches[0][0].Order.Status != domain.OrderStatusCancelled {
		t.Fatalf("partial cancelled fill lifecycle = %+v", fillRepo.batches)
	}
	if len(orderRepo.updates) != 0 {
		t.Fatalf("terminal status was persisted outside atomic fill: %+v", orderRepo.updates)
	}
}

func (b *mockOptionsBroker) GetAccountBalance(context.Context) (execution.Balance, error) {
	return execution.Balance{Cash: 100000, BuyingPower: 100000, Equity: 100000, Currency: "USD"}, nil
}

func (b *mockOptionsBroker) OptionFillReport(ctx context.Context, order *domain.Order) (execution.OptionFillReport, error) {
	if b.optionFillReportFn != nil {
		return b.optionFillReportFn(ctx, order)
	}
	return execution.OptionFillReport{Premium: order.FilledQuantity * 100 * 2.5, Fee: order.FilledQuantity * 0.65}, nil
}

func (b *mockOptionsBroker) SubmitOptionOrder(ctx context.Context, order *domain.Order) (string, error) {
	if b.submitOptionOrderFn != nil {
		return b.submitOptionOrderFn(ctx, order)
	}
	return "opt-ext-123", nil
}

func (b *mockOptionsBroker) SubmitSpreadOrder(ctx context.Context, spread *domain.OptionSpread, quantity float64, clientOrderID string) ([]string, error) {
	if b.submitSpreadOrderFn != nil {
		return b.submitSpreadOrderFn(ctx, spread, quantity, clientOrderID)
	}
	return []string{"leg-1"}, nil
}

func (b *mockOptionsBroker) PreflightSpread(context.Context, *domain.OptionSpread, float64) error {
	return nil
}

func (b *mockOptionsBroker) RollbackOptionOrder(context.Context, string) error { return nil }

func (b *mockOptionsBroker) RollbackOptionSpread(context.Context, []string) error { return nil }

func (b *mockOptionsBroker) FinalizeOptionSpread([]string) error { return nil }

func newTestOptionsManager(broker *mockOptionsBroker, orderRepo *mockOrderRepo, positionRepo *mockPositionRepo, tradeRepo *mockTradeRepo, riskEng *mockRiskEngine) *execution.OptionsOrderManager {
	return newTestOptionsManagerWithFillRepo(broker, orderRepo, positionRepo, tradeRepo, riskEng, &recordingOptionFillRepo{})
}

func newTestOptionsManagerWithFillRepo(broker execution.OptionsBroker, orderRepo *mockOrderRepo, positionRepo *mockPositionRepo, tradeRepo *mockTradeRepo, riskEng *mockRiskEngine, fillRepo repository.OptionFillRepository) *execution.OptionsOrderManager {
	return execution.NewOptionsOrderManager(broker, orderRepo, positionRepo, tradeRepo, riskEng, slog.Default()).WithOptionFillRepo(fillRepo)
}

func optionExecutionScope(strategyID, runID uuid.UUID) execution.ExecutionScope {
	scope, err := execution.NewStrategyExecutionScope(testExecutionAccountBinding.AccountID(), testExecutionAccountBinding.Environment(), uuid.NewSHA1(uuid.NameSpaceOID, []byte("option-version:"+strategyID.String())), domain.PipelineRunRef{ID: runID, TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}, strategyID)
	if err != nil {
		panic(err)
	}
	return scope
}

func TestProcessOptionSignal_PersistsExplicitContractMetadata(t *testing.T) {
	broker := &mockOptionsBroker{}
	orderRepo := &mockOrderRepo{}
	positionRepo := &mockPositionRepo{}
	tradeRepo := &mockTradeRepo{}
	riskEng := &mockRiskEngine{}

	mgr := newTestOptionsManager(broker, orderRepo, positionRepo, tradeRepo, riskEng)
	plan := execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryType: "market", EntryPrice: 2.5, PositionSize: 1}
	strategyID, runID := uuid.New(), uuid.New()
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(strategyID, runID), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, plan)
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
	created := orderRepo.orders[0]
	if created.MarketType != domain.MarketTypeOptions || created.AssetClass != domain.AssetClassOption {
		t.Fatalf("order classification = %s/%s, want options/option", created.MarketType, created.AssetClass)
	}
	if created.UnderlyingTicker != "AAPL" || created.OptionType == nil || *created.OptionType != domain.OptionTypeCall || created.Strike == nil || *created.Strike != 150 || created.Expiry == nil || created.ContractMultiplier != 100 {
		t.Fatalf("option contract metadata not persisted: %+v", created)
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

	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(strategyID, uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryType: "market", EntryPrice: 2.5, PositionSize: 1})
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

	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryType: "market", EntryPrice: 2.5, PositionSize: 1})
	if err == nil {
		t.Fatal("expected live gate error")
	}
	if len(orderRepo.orders) != 0 {
		t.Fatalf("expected 0 orders, got %d", len(orderRepo.orders))
	}
}

func TestProcessOptionSignal_RejectsGenericTickerBeforePersistence(t *testing.T) {
	orderRepo := &mockOrderRepo{}
	mgr := newTestOptionsManager(&mockOptionsBroker{}, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{})
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL", EntryPrice: 2.5, PositionSize: 1})
	if err == nil || len(orderRepo.orders) != 0 {
		t.Fatalf("generic ticker should fail before persistence: err=%v orders=%d", err, len(orderRepo.orders))
	}
}

func TestProcessOptionSignal_PreTradeRiskRejection(t *testing.T) {
	orderRepo := &mockOrderRepo{}
	riskEng := &mockRiskEngine{checkPreTradeFn: func(context.Context, *domain.Order, risk.Portfolio) (bool, string, error) {
		return false, "options exposure limit", nil
	}}
	mgr := newTestOptionsManager(&mockOptionsBroker{}, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, riskEng)
	greeks := &domain.OptionGreeks{Delta: 0.4, Gamma: 0.02, Theta: -0.1, Vega: 0.2}
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2.5, PositionSize: 1, OptionGreeks: greeks})
	if err == nil || len(orderRepo.orders) != 0 {
		t.Fatalf("risk rejection should fail before persistence: err=%v orders=%d", err, len(orderRepo.orders))
	}
}

func TestProcessOptionSignalDefinitiveRejectionUsesAtomicTerminalizer(t *testing.T) {
	orderRepo := &mockOrderRepo{}
	broker := &mockOptionsBroker{submitOptionOrderFn: func(context.Context, *domain.Order) (string, error) {
		return "", errors.Join(execution.ErrBrokerOrderRejected, errors.New("insufficient buying power"))
	}}
	mgr := newTestOptionsManager(broker, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{})
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2.5, PositionSize: 1})
	if err == nil || len(orderRepo.rejectedOptionOrderIDs) != 1 || len(orderRepo.orders) != 1 || orderRepo.rejectedOptionOrderIDs[0] != orderRepo.orders[0].ID {
		t.Fatalf("definitive rejection err=%v rejected=%v orders=%v", err, orderRepo.rejectedOptionOrderIDs, orderRepo.orders)
	}
}

func TestProcessOptionSignal_PositionLimitUsesContractMultiplier(t *testing.T) {
	var exposure float64
	riskEng := &mockRiskEngine{checkPositionLimitsFn: func(_ context.Context, _ string, quantity float64, _ risk.Portfolio) (bool, string, error) {
		exposure = quantity
		return true, "", nil
	}}
	mgr := newTestOptionsManager(&mockOptionsBroker{}, &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}, riskEng)
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2.5, PositionSize: 2})
	if err != nil {
		t.Fatalf("ProcessOptionSignal() error = %v", err)
	}
	if math.Abs(exposure-0.005) > 1e-12 {
		t.Fatalf("exposure = %f, want 0.005 including 100x multiplier", exposure)
	}
}

func TestProcessOptionSignal_PreservesImmediatePaperFill(t *testing.T) {
	orderRepo := &mockOrderRepo{}
	positionRepo := &mockPositionRepo{}
	tradeRepo := &mockTradeRepo{}
	fillRepo := &recordingOptionFillRepo{}
	broker := &mockOptionsBroker{submitOptionOrderFn: func(_ context.Context, order *domain.Order) (string, error) {
		price := 2.5
		filledAt := time.Now().UTC()
		order.Status = domain.OrderStatusFilled
		order.FilledQuantity = order.Quantity
		order.FilledAvgPrice = &price
		order.FilledAt = &filledAt
		return "paper-option-1", nil
	}}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, positionRepo, tradeRepo, &mockRiskEngine{}, fillRepo)
	greeks := &domain.OptionGreeks{Delta: 0.4, Gamma: 0.02, Theta: -0.1, Vega: 0.2}
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2.5, PositionSize: 1, OptionGreeks: greeks})
	if err != nil {
		t.Fatalf("ProcessOptionSignal() error = %v", err)
	}
	if len(orderRepo.updates) != 0 {
		t.Fatalf("filled order must be committed only by the atomic repository, got %d pre-updates", len(orderRepo.updates))
	}
	if len(fillRepo.batches) != 1 || len(fillRepo.batches[0]) != 1 {
		t.Fatalf("filled option lifecycle was not one atomic batch: %+v", fillRepo.batches)
	}
	input := fillRepo.batches[0][0]
	if input.PositionID != nil || input.Order.UnderlyingTicker != "AAPL" || input.Order.OptionGreeks == nil || input.Order.OptionGreeks.Delta != 0.4 || input.Premium != 250 || input.Fee != 0.65 {
		t.Fatalf("filled option accounting not preserved: %+v", input)
	}
}

func TestProcessOptionSignal_RollsBackPaperFillWhenAtomicPersistenceFails(t *testing.T) {
	broker := paper.NewPaperBroker(10000, 0, 0)
	fillRepo := &recordingOptionFillRepo{err: errors.New("database unavailable")}
	orderRepo := &mockOrderRepo{}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{}, fillRepo).WithBrokerName("paper")
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2.5, PositionSize: 1})
	if err == nil {
		t.Fatal("expected atomic persistence failure")
	}
	balance, balanceErr := broker.GetAccountBalance(context.Background())
	if balanceErr != nil || balance.Cash != 10000 {
		t.Fatalf("paper fill was not compensated: balance=%+v err=%v", balance, balanceErr)
	}
	if len(orderRepo.updates) != 1 || orderRepo.updates[0].Status != domain.OrderStatusRejected || orderRepo.updates[0].FilledQuantity != 0 || orderRepo.updates[0].FilledAt != nil {
		t.Fatalf("compensated order was not durably rejected: %+v", orderRepo.updates)
	}
}

func TestProcessOptionSignalCommitAckLossKeepsConfirmedPaperFill(t *testing.T) {
	broker := paper.NewPaperBroker(10000, 0, 0)
	fillRepo := &recordingOptionFillRepo{err: errors.New("commit acknowledgement lost"), resolveCommitted: true}
	orderRepo := &mockOrderRepo{}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{}, fillRepo).WithBrokerName("paper")
	if err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2.5, PositionSize: 1}); err != nil {
		t.Fatalf("commit readback recovery error = %v", err)
	}
	balance, _ := broker.GetAccountBalance(context.Background())
	positions, _ := broker.GetPositions(context.Background())
	if balance.Cash == 10000 || len(positions) != 1 || len(orderRepo.updates) != 0 {
		t.Fatalf("confirmed commit was compensated: balance=%+v positions=%+v updates=%+v", balance, positions, orderRepo.updates)
	}
}

func TestProcessOptionSignalUncertainReadbackDoesNotCompensate(t *testing.T) {
	broker := paper.NewPaperBroker(10000, 0, 0)
	fillRepo := &recordingOptionFillRepo{err: errors.New("commit acknowledgement lost"), resolveErr: errors.New("readback unavailable")}
	orderRepo := &mockOrderRepo{}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{}, fillRepo).WithBrokerName("paper")
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2.5, PositionSize: 1})
	if err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("uncertain readback error = %v", err)
	}
	balance, _ := broker.GetAccountBalance(context.Background())
	positions, _ := broker.GetPositions(context.Background())
	if balance.Cash == 10000 || len(positions) != 1 || len(orderRepo.updates) != 0 {
		t.Fatalf("uncertain effect was compensated: balance=%+v positions=%+v updates=%+v", balance, positions, orderRepo.updates)
	}
}

func TestCloseOptionPositionPersistsLifecycle(t *testing.T) {
	orderRepo := &mockOrderRepo{}
	positionRepo := &mockPositionRepo{}
	tradeRepo := &mockTradeRepo{}
	fillRepo := &recordingOptionFillRepo{}
	broker := &mockOptionsBroker{submitOptionOrderFn: func(_ context.Context, order *domain.Order) (string, error) {
		price := *order.LimitPrice
		filledAt := time.Now().UTC()
		order.Status, order.FilledQuantity, order.FilledAvgPrice, order.FilledAt = domain.OrderStatusFilled, order.Quantity, &price, &filledAt
		return "paper-close-1", nil
	}, optionFillReportFn: func(_ context.Context, order *domain.Order) (execution.OptionFillReport, error) {
		return execution.OptionFillReport{Premium: *order.FilledAvgPrice * order.FilledQuantity * 100, Fee: 0.65}, nil
	}}
	strategyID, runID := uuid.New(), uuid.New()
	optionType, strike := domain.OptionTypeCall, 150.0
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	position := &domain.Position{ID: uuid.New(), StrategyID: &strategyID, MarketType: domain.MarketTypeOptions, Ticker: "AAPL271217C00150000", Side: domain.PositionSideLong, Quantity: 2, AvgEntry: 2.5, AssetClass: domain.AssetClassOption, UnderlyingTicker: "AAPL", OptionType: &optionType, Strike: &strike, Expiry: &expiry, ContractMultiplier: 100}
	scope := optionExecutionScope(strategyID, runID)
	originType, originID := scope.Origin()
	position.AccountID, position.Environment, position.OriginType, position.OriginID = scope.AccountID(), scope.Environment(), string(originType), originID
	positionRepo.getFn = func(context.Context, uuid.UUID) (*domain.Position, error) { return position, nil }
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, positionRepo, tradeRepo, &mockRiskEngine{}, fillRepo)
	if err := mgr.CloseOptionPosition(context.Background(), scope, position, 3.5, "profit target"); err != nil {
		t.Fatalf("CloseOptionPosition() error = %v", err)
	}
	if len(orderRepo.orders) != 1 || orderRepo.orders[0].PositionIntent == nil || *orderRepo.orders[0].PositionIntent != domain.PositionIntentSellToClose {
		t.Fatalf("close order missing sell-to-close intent: %+v", orderRepo.orders)
	}
	if len(fillRepo.batches) != 1 || len(fillRepo.batches[0]) != 1 {
		t.Fatalf("close lifecycle was not one atomic batch: %+v", fillRepo.batches)
	}
	input := fillRepo.batches[0][0]
	if input.PositionID == nil || *input.PositionID != position.ID || input.ExitReason != "profit target" || input.Premium != 700 || input.Fee != 0.65 {
		t.Fatalf("closing fill not preserved: %+v", input)
	}
}

func TestCloseOptionPositionRejectsIncompletePersistence(t *testing.T) {
	mgr := newTestOptionsManager(&mockOptionsBroker{}, &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{})
	strategyID, runID := uuid.New(), uuid.New()
	position := &domain.Position{ID: uuid.New(), StrategyID: &strategyID, AssetClass: domain.AssetClassOption, Quantity: 1}
	mgr = newTestOptionsManager(&mockOptionsBroker{}, &mockOrderRepo{}, &mockPositionRepo{getFn: func(context.Context, uuid.UUID) (*domain.Position, error) { return position, nil }}, &mockTradeRepo{}, &mockRiskEngine{})
	err := mgr.CloseOptionPosition(context.Background(), optionExecutionScope(strategyID, runID), position, 2, "")
	if err == nil {
		t.Fatal("expected incomplete persisted contract to fail closed")
	}
}

func TestProcessSpreadSignalPreflightPreventsOrphanOrders(t *testing.T) {
	orderRepo := &mockOrderRepo{}
	mgr := execution.NewOptionsOrderManager(paper.NewPaperBroker(100000, 0, 0), orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{}, slog.Default())
	spread := &domain.OptionSpread{Underlying: "AAPL", MaxRisk: 500, Legs: []domain.SpreadLeg{{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC), Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1}}}
	err := mgr.ProcessSpreadSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), spread, 1)
	if err == nil {
		t.Fatal("expected unsupported paper spread to fail preflight")
	}
	if len(orderRepo.orders) != 0 {
		t.Fatalf("preflight failure persisted %d orphan leg orders", len(orderRepo.orders))
	}
}

func TestProcessSpreadSignalPersistsAtomicPaperLegs(t *testing.T) {
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	broker := paper.NewPaperBroker(100000, 0, 0)
	fillRepo := &recordingOptionFillRepo{}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, positionRepo, tradeRepo, &mockRiskEngine{}, fillRepo).WithBrokerName("paper")
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	spread := &domain.OptionSpread{StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL", MaxRisk: 150, MaxReward: 350, Legs: []domain.SpreadLeg{
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, ExecutablePrice: 2.5},
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00155000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 155, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, ExecutablePrice: 1},
	}}
	if err := mgr.ProcessSpreadSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), spread, 1); err != nil {
		t.Fatalf("ProcessSpreadSignal() error = %v", err)
	}
	if len(fillRepo.batches) != 1 || len(fillRepo.batches[0]) != 2 {
		t.Fatalf("spread lifecycle was not one atomic batch: %+v", fillRepo.batches)
	}
	if fillRepo.batches[0][0].Order.LegGroupID == nil || fillRepo.batches[0][1].Order.LegGroupID == nil || *fillRepo.batches[0][0].Order.LegGroupID != *fillRepo.batches[0][1].Order.LegGroupID {
		t.Fatalf("spread legs not atomically grouped: %+v", fillRepo.batches[0])
	}
	groupID := fillRepo.batches[0][0].Order.LegGroupID.String()
	parentID := fillRepo.batches[0][0].Order.ClientOrderID
	if !strings.HasPrefix(parentID, "augr-option-spread-parent-") || strings.Contains(parentID, groupID) {
		t.Fatalf("spread parent client id %q must be persisted and distinct from opening leg group %s", parentID, groupID)
	}
}

func TestProcessSpreadSignalCompensatesPaperDebitWhenAtomicPersistenceFails(t *testing.T) {
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	broker := paper.NewPaperBroker(100000, 0, 0)
	fillRepo := &recordingOptionFillRepo{err: errors.New("database unavailable")}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, positionRepo, tradeRepo, &mockRiskEngine{}, fillRepo).WithBrokerName("paper")
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	spread := &domain.OptionSpread{StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL", MaxRisk: 150, MaxReward: 350, Legs: []domain.SpreadLeg{
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, ExecutablePrice: 2.5},
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00155000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 155, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, ExecutablePrice: 1},
	}}
	if err := mgr.ProcessSpreadSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), spread, 1); err == nil {
		t.Fatal("expected atomic spread persistence failure")
	}
	balance, err := broker.GetAccountBalance(context.Background())
	if err != nil || balance.Cash != 100000 {
		t.Fatalf("paper spread was not compensated: balance=%+v err=%v", balance, err)
	}
	if len(orderRepo.updates) != 2 || orderRepo.updates[0].Status != domain.OrderStatusRejected || orderRepo.updates[1].Status != domain.OrderStatusRejected {
		t.Fatalf("compensated spread orders were not rejected: %+v", orderRepo.updates)
	}
}

func TestProcessSpreadSignalAtomicallyClosesPersistedLegGroup(t *testing.T) {
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	strategyID, groupID := uuid.New(), uuid.New()
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	optionType, longStrike, shortStrike := domain.OptionTypeCall, 150.0, 155.0
	positions := []domain.Position{
		{ID: uuid.New(), StrategyID: &strategyID, Ticker: "AAPL271217C00150000", Side: domain.PositionSideLong, Quantity: 1, AvgEntry: 2.5, AssetClass: domain.AssetClassOption, UnderlyingTicker: "AAPL", OptionType: &optionType, Strike: &longStrike, Expiry: &expiry, ContractMultiplier: 100, LegGroupID: &groupID},
		{ID: uuid.New(), StrategyID: &strategyID, Ticker: "AAPL271217C00155000", Side: domain.PositionSideShort, Quantity: 1, AvgEntry: 1, AssetClass: domain.AssetClassOption, UnderlyingTicker: "AAPL", OptionType: &optionType, Strike: &shortStrike, Expiry: &expiry, ContractMultiplier: 100, LegGroupID: &groupID},
	}
	scope := optionExecutionScope(strategyID, uuid.New())
	originType, originID := scope.Origin()
	for i := range positions {
		positions[i].AccountID, positions[i].Environment = scope.AccountID(), scope.Environment()
		positions[i].OriginType, positions[i].OriginID = string(originType), originID
	}
	positionRepo.getByStrategyFn = func(context.Context, uuid.UUID, repository.PositionFilter, int, int) ([]domain.Position, error) {
		return positions, nil
	}
	positionRepo.executionScopeFn = func(context.Context, uuid.UUID, domain.AccountEnvironment, string, string, repository.PositionFilter, int, int) ([]domain.Position, error) {
		return positions, nil
	}
	positionRepo.getOpenFn = func(context.Context, repository.PositionFilter, int, int) ([]domain.Position, error) {
		return positions, nil
	}
	spread := &domain.OptionSpread{StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL", Legs: []domain.SpreadLeg{
		{Contract: domain.OptionContract{OCCSymbol: positions[0].Ticker, Underlying: "AAPL", OptionType: optionType, Strike: longStrike, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToClose, Ratio: 1, ExecutablePrice: 3},
		{Contract: domain.OptionContract{OCCSymbol: positions[1].Ticker, Underlying: "AAPL", OptionType: optionType, Strike: shortStrike, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToClose, Ratio: 1, ExecutablePrice: 1.2},
	}}
	fillRepo := &recordingOptionFillRepo{}
	riskEng := &mockRiskEngine{isKillSwitchActiveFn: func(context.Context) (bool, error) { return true, nil }}
	broker := paper.NewPaperBroker(100000, 0, 0)
	if err := broker.RestorePositions(positions); err != nil {
		t.Fatal(err)
	}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, positionRepo, tradeRepo, riskEng, fillRepo).WithBrokerName("paper")
	if err := mgr.ProcessSpreadSignal(context.Background(), scope, spread, 1); err != nil {
		t.Fatalf("ProcessSpreadSignal(close) error = %v", err)
	}
	if len(fillRepo.batches) != 1 || len(fillRepo.batches[0]) != 2 {
		t.Fatalf("spread close lifecycle was not one atomic batch: %+v", fillRepo.batches)
	}
	for index := range fillRepo.batches[0] {
		if fillRepo.batches[0][index].PositionID == nil || fillRepo.batches[0][index].ExitReason != "strategy spread close" {
			t.Fatalf("spread close leg %d incomplete: %+v", index, fillRepo.batches[0][index])
		}
	}
}

func TestProcessSpreadSignalRetainsMalformedAsyncResponseForReconciliation(t *testing.T) {
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	spread := &domain.OptionSpread{StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL", MaxRisk: 100, Legs: []domain.SpreadLeg{
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, ExecutablePrice: 2},
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00155000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 155, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, ExecutablePrice: 1},
	}}
	mgr := newTestOptionsManagerWithFillRepo(malformedAsyncSpreadBroker{}, orderRepo, positionRepo, tradeRepo, &mockRiskEngine{}, &recordingOptionFillRepo{})
	if err := mgr.ProcessSpreadSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), spread, 1); err == nil {
		t.Fatal("malformed async spread response accepted")
	}
	if len(orderRepo.updates) != 0 || len(orderRepo.orders) != 2 {
		t.Fatalf("malformed async orders were not retained pending: creates=%d updates=%+v", len(orderRepo.orders), orderRepo.updates)
	}
}

func TestProcessOptionSignalRetainsAmbiguousSubmitForRestartReconciliation(t *testing.T) {
	broker := &mockOptionsBroker{submitOptionOrderFn: func(context.Context, *domain.Order) (string, error) {
		return "", errors.New("timeout after send")
	}}
	orderRepo, positionRepo, tradeRepo := &mockOrderRepo{}, &mockPositionRepo{}, &mockTradeRepo{}
	mgr := newTestOptionsManager(broker, orderRepo, positionRepo, tradeRepo, &mockRiskEngine{})
	plan := execution.TradingPlan{Ticker: "AAPL271217C00150000", EntryPrice: 2, PositionSize: 1}
	err := mgr.ProcessOptionSignal(context.Background(), optionExecutionScope(uuid.New(), uuid.New()), execution.FinalSignal{Signal: domain.PipelineSignalBuy}, plan)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ProcessOptionSignal() error = %v, want ambiguous outcome", err)
	}
	if len(orderRepo.orders) != 1 || len(orderRepo.updates) != 0 {
		t.Fatalf("ambiguous submit did not retain pending order: creates=%d updates=%d", len(orderRepo.orders), len(orderRepo.updates))
	}
	order := orderRepo.orders[0]
	if order.Status != domain.OrderStatusPending || order.ClientOrderID == "" {
		t.Fatalf("retained order lacks durable recovery identity: %+v", order)
	}
}

func TestReconcilePendingOptionOrderUsesClientIDAndPersistsFill(t *testing.T) {
	price, filledAt := 2.5, time.Now().UTC()
	clientID := "augr-option-" + uuid.NewString()
	broker := &mockOptionsBroker{getOrderStatusFn: func(_ context.Context, got string) (execution.BrokerOrderStatus, error) {
		if got != clientID {
			t.Fatalf("lookup id = %q, want %q", got, clientID)
		}
		return execution.BrokerOrderStatus{Status: domain.OrderStatusFilled, FilledQuantity: 1, FilledAvgPrice: &price, FilledAt: &filledAt}, nil
	}}
	fillRepo := &recordingOptionFillRepo{}
	strategyID := uuid.New()
	order := domain.Order{ID: uuid.New(), AccountID: testExecutionAccountBinding.AccountID(), Environment: testExecutionAccountBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), StrategyID: &strategyID, ClientOrderID: clientID, Ticker: "AAPL271217C00150000", MarketType: domain.MarketTypeOptions, AssetClass: domain.AssetClassOption, Side: domain.OrderSideBuy, Quantity: 1, Status: domain.OrderStatusPending}
	orderRepo := &mockOrderRepo{getFn: func(context.Context, uuid.UUID) (*domain.Order, error) { copy := order; return &copy, nil }}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{}, fillRepo)
	if err := mgr.ReconcilePendingOptionOrders(context.Background(), testExecutionAccountBinding, []domain.Order{order}, nil); err != nil {
		t.Fatal(err)
	}
	if len(fillRepo.batches) != 1 || len(fillRepo.batches[0]) != 1 || fillRepo.batches[0][0].Order.ClientOrderID != clientID {
		t.Fatalf("recovered fills = %+v", fillRepo.batches)
	}
}

func TestReconcileOptionSpreadReloadsEveryNonterminalLegAndPersistsOneBatch(t *testing.T) {
	price, filledAt, groupID := 1.25, time.Now().UTC(), uuid.New()
	strategyID := uuid.New()
	orders := make([]domain.Order, 2)
	byID := make(map[uuid.UUID]domain.Order, 2)
	for i := range orders {
		orders[i] = domain.Order{ID: uuid.New(), AccountID: testExecutionAccountBinding.AccountID(), Environment: testExecutionAccountBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), StrategyID: &strategyID, ClientOrderID: "spread-client-" + uuid.NewString(), ExternalID: "spread-real-" + uuid.NewString(), Ticker: fmt.Sprintf("AAPL271217C00%d", 150000+i), MarketType: domain.MarketTypeOptions, AssetClass: domain.AssetClassOption, Side: domain.OrderSideBuy, Quantity: 1, Status: domain.OrderStatusSubmitted, LegGroupID: &groupID}
		byID[orders[i].ID] = orders[i]
	}
	broker := &mockOptionsBroker{getOrderStatusFn: func(context.Context, string) (execution.BrokerOrderStatus, error) {
		return execution.BrokerOrderStatus{Status: domain.OrderStatusFilled, FilledQuantity: 1, FilledAvgPrice: &price, FilledAt: &filledAt}, nil
	}}
	broker.spreadStatusFn = func(context.Context, string) (execution.BrokerSpreadOrderStatus, error) {
		status := execution.BrokerOrderStatus{Status: domain.OrderStatusFilled, FilledQuantity: 1, FilledAvgPrice: &price, FilledAt: &filledAt}
		return execution.BrokerSpreadOrderStatus{Legs: []execution.BrokerSpreadLegStatus{{Ticker: orders[1].Ticker, ExternalID: "leg-2", Status: status}, {Ticker: orders[0].Ticker, ExternalID: "leg-1", Status: status}}}, nil
	}
	reloads := 0
	orderRepo := &mockOrderRepo{getFn: func(_ context.Context, id uuid.UUID) (*domain.Order, error) {
		reloads++
		value := byID[id]
		return &value, nil
	}}
	fillRepo := &recordingOptionFillRepo{}
	mgr := newTestOptionsManagerWithFillRepo(broker, orderRepo, &mockPositionRepo{}, &mockTradeRepo{}, &mockRiskEngine{}, fillRepo)
	if err := mgr.ReconcilePendingOptionOrders(context.Background(), testExecutionAccountBinding, orders, nil); err != nil {
		t.Fatal(err)
	}
	if reloads != 2 || len(fillRepo.batches) != 1 || len(fillRepo.batches[0]) != 2 {
		t.Fatalf("reloads=%d batches=%v", reloads, fillRepo.batches)
	}
}

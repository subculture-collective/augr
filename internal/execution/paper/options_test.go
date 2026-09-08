package paper

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/google/uuid"
)

func optionOrder(price *float64) *domain.Order {
	optionType := domain.OptionTypeCall
	intent := domain.PositionIntentBuyToOpen
	return &domain.Order{Ticker: "AAPL271217C00150000", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 2, LimitPrice: price, AssetClass: domain.AssetClassOption, OptionType: &optionType, ContractMultiplier: 100, PositionIntent: &intent}
}

func TestOptionLotsRemainDistinctBehindAggregateExecutionView(t *testing.T) {
	price := 2.50
	broker := NewPaperBroker(10000, 0, 0)
	first, second := optionOrder(&price), optionOrder(&price)
	if _, err := broker.SubmitOptionOrder(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	firstID := uuid.New()
	if err := broker.BindDurableOptionPosition(context.Background(), first.Ticker, firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.SubmitOptionOrder(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	secondID := uuid.New()
	if err := broker.BindDurableOptionPosition(context.Background(), second.Ticker, secondID); err != nil {
		t.Fatal(err)
	}
	if len(broker.optionLots) != 2 {
		t.Fatalf("durable option lots = %d, want 2", len(broker.optionLots))
	}
	positions, err := broker.GetPositions(context.Background())
	if err != nil || len(positions) != 1 || positions[0].Quantity != 4 {
		t.Fatalf("aggregate execution view = %+v, err=%v", positions, err)
	}
	if err := broker.ApplyOptionSettlement(context.Background(), firstID, 5); err != nil {
		t.Fatal(err)
	}
	if broker.optionLots[firstID] != nil || broker.optionLots[secondID] == nil || broker.optionLots[secondID].Quantity != 2 {
		t.Fatalf("settlement did not delete exact lot: %+v", broker.optionLots)
	}
}

func TestOptionCloseTargetsExactDurableLot(t *testing.T) {
	price := 2.50
	broker := NewPaperBroker(10000, 0, 0)
	first, second := optionOrder(&price), optionOrder(&price)
	first.Quantity, second.Quantity = 1, 1
	if _, err := broker.SubmitOptionOrder(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	firstID := uuid.New()
	if err := broker.BindDurableOptionPosition(context.Background(), first.Ticker, firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.SubmitOptionOrder(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	secondID := uuid.New()
	if err := broker.BindDurableOptionPosition(context.Background(), second.Ticker, secondID); err != nil {
		t.Fatal(err)
	}
	intent := domain.PositionIntentSellToClose
	closeOrder := optionOrder(&price)
	closeOrder.Side, closeOrder.Quantity, closeOrder.PositionIntent = domain.OrderSideSell, 1, &intent
	closeOrder.ClosePositionIDs = []uuid.UUID{secondID}
	if _, err := broker.SubmitOptionOrder(context.Background(), closeOrder); err != nil {
		t.Fatal(err)
	}
	if broker.optionLots[firstID] == nil || broker.optionLots[secondID] != nil {
		t.Fatalf("exact durable close targeted wrong lot: %+v", broker.optionLots)
	}
}

func TestOptionInsufficientFundsIsDefinitiveBrokerRejection(t *testing.T) {
	price := 2.50
	broker := NewPaperBroker(1, 0, 0)
	_, err := broker.SubmitOptionOrder(context.Background(), optionOrder(&price))
	if !errors.Is(err, execution.ErrBrokerOrderRejected) {
		t.Fatalf("SubmitOptionOrder() error = %v, want ErrBrokerOrderRejected", err)
	}
}

func TestOptionSettlementRequiresCommittedDurablePositionIdentity(t *testing.T) {
	price := 2.50
	broker := NewPaperBroker(10000, 0, 0)
	order := optionOrder(&price)
	if _, err := broker.SubmitOptionOrder(context.Background(), order); err != nil {
		t.Fatal(err)
	}
	if err := broker.ApplyOptionSettlement(context.Background(), uuid.New(), 5); err == nil {
		t.Fatal("unmatched durable settlement must be rejected")
	}
	positionID := uuid.New()
	if err := broker.BindDurableOptionPosition(context.Background(), order.Ticker, positionID); err != nil {
		t.Fatal(err)
	}
	if err := broker.ApplyOptionSettlement(context.Background(), positionID, 5); err != nil {
		t.Fatal(err)
	}
	if err := broker.ApplyOptionSettlement(context.Background(), positionID, 5); err != nil {
		t.Fatalf("durable settlement replay must be idempotent: %v", err)
	}
}

func TestSubmitOptionOrderRequiresExecutablePrice(t *testing.T) {
	broker := NewPaperBroker(10000, 0, 0)
	if _, err := broker.SubmitOptionOrder(context.Background(), optionOrder(nil)); err == nil {
		t.Fatal("expected missing executable price to be rejected")
	}
}

func TestSubmitOptionOrderFillsWithoutExternalBroker(t *testing.T) {
	price := 2.50
	broker := NewPaperBroker(10000, 0, 0)
	order := optionOrder(&price)
	externalID, err := broker.SubmitOptionOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("SubmitOptionOrder() error = %v", err)
	}
	if externalID == "" || order.Status != domain.OrderStatusFilled || order.FilledQuantity != 2 || order.FilledAvgPrice == nil || *order.FilledAvgPrice != price {
		t.Fatalf("unexpected paper fill: id=%q order=%+v", externalID, order)
	}
	balance, err := broker.GetAccountBalance(context.Background())
	if err != nil {
		t.Fatalf("GetAccountBalance() error = %v", err)
	}
	wantCash := 10000 - (price * 2 * 100) - (2 * DefaultOptionFeePerContract)
	if balance.Cash != wantCash {
		t.Fatalf("cash = %.2f, want %.2f", balance.Cash, wantCash)
	}
	positions, err := broker.GetPositions(context.Background())
	if err != nil || len(positions) != 1 || positions[0].Quantity != 2 || positions[0].AssetClass != domain.AssetClassOption || positions[0].ContractMultiplier != 100 {
		t.Fatalf("option broker position = %+v, err=%v", positions, err)
	}
	if math.Abs(balance.Equity-(10000-2*DefaultOptionFeePerContract)) > 1e-9 {
		t.Fatalf("option equity = %.2f, want multiplier-aware %.2f", balance.Equity, 10000-2*DefaultOptionFeePerContract)
	}
	if err := broker.RollbackOptionOrder(context.Background(), externalID); err != nil {
		t.Fatalf("RollbackOptionOrder() error = %v", err)
	}
	balance, _ = broker.GetAccountBalance(context.Background())
	if balance.Cash != 10000 {
		t.Fatalf("rollback cash = %.2f, want 10000", balance.Cash)
	}
	positions, _ = broker.GetPositions(context.Background())
	if len(positions) != 0 || balance.Equity != 10000 {
		t.Fatalf("rollback accounting positions=%+v balance=%+v", positions, balance)
	}
	if err := broker.RollbackOptionOrder(context.Background(), externalID); err == nil {
		t.Fatal("duplicate option rollback must fail")
	}
}

func TestSubmitSpreadOrderFailsClosed(t *testing.T) {
	broker := NewPaperBroker(10000, 0, 0)
	if _, err := broker.SubmitSpreadOrder(context.Background(), &domain.OptionSpread{}, 1, "spread-test"); err == nil {
		t.Fatal("expected malformed spread to fail closed")
	}
}

func TestSubmitSpreadOrderAtomicallyDebitsVertical(t *testing.T) {
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	spread := &domain.OptionSpread{StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL", MaxRisk: 150, MaxReward: 350, Legs: []domain.SpreadLeg{
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, ExecutablePrice: 2.5},
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00155000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 155, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, ExecutablePrice: 1},
	}}
	broker := NewPaperBroker(10000, 0, 0)
	ids, err := broker.SubmitSpreadOrder(context.Background(), spread, 1, "spread-test")
	if err != nil || len(ids) != 2 {
		t.Fatalf("SubmitSpreadOrder() ids=%v err=%v", ids, err)
	}
	balance, _ := broker.GetAccountBalance(context.Background())
	if math.Abs(balance.Cash-9848.70) > 1e-9 {
		t.Fatalf("cash = %.2f, want 9848.70", balance.Cash)
	}
	positions, _ := broker.GetPositions(context.Background())
	if len(positions) != 2 || math.Abs(balance.Equity-9998.70) > 1e-9 {
		t.Fatalf("spread accounting positions=%+v balance=%+v", positions, balance)
	}
	if err := broker.RollbackOptionSpread(context.Background(), ids); err != nil {
		t.Fatalf("RollbackOptionSpread() error = %v", err)
	}
	balance, _ = broker.GetAccountBalance(context.Background())
	if balance.Cash != 10000 {
		t.Fatalf("spread rollback cash = %.2f, want 10000", balance.Cash)
	}
	positions, _ = broker.GetPositions(context.Background())
	if len(positions) != 0 || balance.Equity != 10000 {
		t.Fatalf("spread rollback accounting positions=%+v balance=%+v", positions, balance)
	}
	status, err := broker.GetSpreadOrderStatusByClientOrderIDResult(context.Background(), "spread-test")
	if err != nil || len(status.Legs) != 2 || status.Legs[0].Status.Status != domain.OrderStatusRejected || status.Legs[1].Status.Status != domain.OrderStatusRejected {
		t.Fatalf("spread rollback parent evidence=%+v err=%v", status, err)
	}
	if err := broker.RollbackOptionSpread(context.Background(), ids); err == nil {
		t.Fatal("duplicate spread rollback must fail")
	}
}

func TestFinalizeOptionSpreadRemovesCompensationRecord(t *testing.T) {
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	spread := &domain.OptionSpread{StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL", MaxRisk: 150, MaxReward: 350, Legs: []domain.SpreadLeg{
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00150000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 150, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideBuy, PositionIntent: domain.PositionIntentBuyToOpen, Ratio: 1, ExecutablePrice: 2.5},
		{Contract: domain.OptionContract{OCCSymbol: "AAPL271217C00155000", Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 155, Expiry: expiry, Multiplier: 100}, Side: domain.OrderSideSell, PositionIntent: domain.PositionIntentSellToOpen, Ratio: 1, ExecutablePrice: 1},
	}}
	broker := NewPaperBroker(10000, 0, 0)
	ids, err := broker.SubmitSpreadOrder(context.Background(), spread, 1, "spread-test")
	if err != nil {
		t.Fatalf("SubmitSpreadOrder() error = %v", err)
	}
	if err := broker.FinalizeOptionSpread(ids); err != nil {
		t.Fatalf("FinalizeOptionSpread() error = %v", err)
	}
	if err := broker.RollbackOptionSpread(context.Background(), ids); err == nil {
		t.Fatal("finalized spread must not retain a compensation record")
	}
}

package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type notPreparedPlannerStub struct{}

func (notPreparedPlannerStub) PlanAcceptedOrderFill(_ context.Context, _ ExecutionScope, mutation repository.OrderFillInput) (AcceptedFillInput, error) {
	return AcceptedFillInput{}, fmt.Errorf("canonical routed order %s is not prepared: %w", mutation.Order.ID, errors.Join(ErrAcceptedOrderNotPrepared, repository.ErrNotFound))
}

func (notPreparedPlannerStub) PlanAcceptedOptionFills(context.Context, ExecutionScope, []repository.OptionFillInput) ([]AcceptedFillInput, error) {
	return nil, fmt.Errorf("plan accepted option fill 0: %w", ErrAcceptedOrderNotPrepared)
}

func (notPreparedPlannerStub) PlanAcceptedPredictionSettlement(context.Context, ExecutionScope, repository.PredictionDecisionSettlementInput) (AcceptedPredictionSettlementInput, error) {
	return AcceptedPredictionSettlementInput{}, nil
}

type unpreparedStoreStub struct {
	rawEconomicStoreStub
	orderCalls  int
	optionCalls int
	lastOrder   repository.OrderFillInput
}

func (stub *unpreparedStoreStub) ApplyOrderFill(_ context.Context, input repository.OrderFillInput) (repository.OrderFillResult, error) {
	stub.orderCalls++
	stub.lastOrder = input
	tradeID := uuid.New()
	return repository.OrderFillResult{OrderID: input.Order.ID, TradeID: tradeID, Trade: &domain.Trade{ID: tradeID}}, nil
}

func (stub *unpreparedStoreStub) ApplyOptionFills(_ context.Context, inputs []repository.OptionFillInput) ([]repository.OptionFillResult, error) {
	stub.optionCalls++
	results := make([]repository.OptionFillResult, len(inputs))
	for index := range inputs {
		results[index] = repository.OptionFillResult{OrderID: inputs[index].Order.ID}
	}
	return results, nil
}

func unpreparedTestScope(t *testing.T) (ExecutionScope, domain.Order) {
	t.Helper()
	accountID := uuid.New()
	scope, err := NewNonRunExecutionScope(accountID, domain.AccountEnvironmentPaperScored, ledger.ExecutionOriginOperator, "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	order := domain.Order{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: string(ledger.ExecutionOriginOperator), OriginID: "operator-1", Ticker: "AAPL", MarketType: domain.MarketTypeStock, Side: domain.OrderSideBuy, Quantity: 1}
	return scope, order
}

func TestCoordinatedEconomicWriterFallsBackToUnpreparedFill(t *testing.T) {
	var log []string
	store := &unpreparedStoreStub{rawEconomicStoreStub: rawEconomicStoreStub{log: &log}}
	coordinator := &economicCoordinatorStub{log: &log}
	writer, err := NewCoordinatedEconomicWriter(notPreparedPlannerStub{}, store, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	scope, order := unpreparedTestScope(t)
	mutation := repository.OrderFillInput{IdempotencyKey: "k", Order: &order, FillIntent: repository.OrderFillIntent{Side: order.Side, Quantity: 1, ExecutionPrice: 100}, Now: time.Now(), Trade: &domain.Trade{}}
	result, err := writer.ApplyAcceptedOrderFill(context.Background(), scope, mutation)
	if err != nil {
		t.Fatalf("unprepared fill error = %v", err)
	}
	if result.OrderID != order.ID || store.orderCalls != 1 || coordinator.calls != 0 || len(log) != 0 {
		t.Fatalf("unprepared fill must bypass raw evidence and coordinator: result=%+v store=%d coordinator=%d log=%v", result, store.orderCalls, coordinator.calls, log)
	}

	// An order outside the dispatch scope is refused before any write.
	foreign := order
	foreign.AccountID = uuid.New()
	mutation.Order = &foreign
	if _, err := writer.ApplyAcceptedOrderFill(context.Background(), scope, mutation); err == nil || !errors.Is(err, ErrAcceptedEconomicRollbackConfirmed) || store.orderCalls != 1 {
		t.Fatalf("foreign-scope unprepared fill error = %v (calls=%d), want confirmed rollback without writes", err, store.orderCalls)
	}

	// Option fills use the same fallback.
	optionResults, err := writer.ApplyAcceptedOptionFills(context.Background(), scope, []repository.OptionFillInput{{Order: &order, FillQuantity: 1, FillPrice: 2}})
	if err != nil || len(optionResults) != 1 || optionResults[0].OrderID != order.ID || store.optionCalls != 1 {
		t.Fatalf("unprepared option fill = %+v, %v (calls=%d)", optionResults, err, store.optionCalls)
	}
}

func TestCoordinatedEconomicWriterWithoutUnpreparedApplierStillFailsClosed(t *testing.T) {
	var log []string
	store := &rawEconomicStoreStub{log: &log}
	writer, err := NewCoordinatedEconomicWriter(notPreparedPlannerStub{}, store, &economicCoordinatorStub{log: &log})
	if err != nil {
		t.Fatal(err)
	}
	scope, order := unpreparedTestScope(t)
	_, err = writer.ApplyAcceptedOrderFill(context.Background(), scope, repository.OrderFillInput{Order: &order, Trade: &domain.Trade{}})
	if err == nil || !errors.Is(err, ErrAcceptedOrderNotPrepared) || !errors.Is(err, ErrAcceptedEconomicRollbackConfirmed) {
		t.Fatalf("error = %v, want not-prepared marked as confirmed rollback", err)
	}
}

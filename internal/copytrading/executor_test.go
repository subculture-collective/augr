package copytrading

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestNewOrderManagerExecutorRetainsExecutionAccount(t *testing.T) {
	binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewOrderManagerExecutor(OrderManagerExecutorDeps{ExecutionAccount: binding})
	if executor.deps.ExecutionAccount != binding {
		t.Fatal("executor did not retain execution account")
	}
}

func TestMatchingCopyOrderResultRequiresOneExactStockCommand(t *testing.T) {
	accountID, subscriptionID, runID, orderID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	price := 100.0
	request := PaperOrderRequest{
		ClaimID:      uuid.New(),
		Subscription: domain.CopySubscription{ID: subscriptionID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored},
		Intent:       domain.CopyTradeIntent{Ticker: "AAPL", Side: domain.OrderSideBuy, RequestedNotional: 1000, ExecutablePrice: &price},
		OriginRunID:  runID,
	}
	limit := price
	order := domain.Order{ID: orderID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "copy_subscription", OriginID: subscriptionID.String(), CopyOriginRebalanceRunID: runID, Ticker: "AAPL", MarketType: domain.MarketTypeStock, Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: 10, LimitPrice: &limit, Status: domain.OrderStatusSubmitted}
	result, err := matchingCopyOrderResult([]domain.Order{order}, request)
	if err != nil || result.OrderID == nil || *result.OrderID != orderID {
		t.Fatalf("exact result = %+v, %v", result, err)
	}
	for name, orders := range map[string][]domain.Order{
		"duplicate":     {order, order},
		"wrong market":  {func() domain.Order { value := order; value.MarketType = domain.MarketTypePolymarket; return value }()},
		"wrong command": {func() domain.Order { value := order; value.Quantity = 9; return value }()},
	} {
		if _, err := matchingCopyOrderResult(orders, request); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	order.Status = domain.OrderStatusCancelled
	if _, err := matchingCopyOrderResult([]domain.Order{order}, request); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancelled result error = %v", err)
	}
}

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestAcceptedEconomicAsOfUsesLatestMicrosecondFrontier(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 28, 12, 0, 0, 123456789, time.UTC)
	got := acceptedEconomicAsOf(base, base.Add(-time.Second), base.Add(time.Second))
	want := base.Add(time.Second).Truncate(time.Microsecond)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("acceptedEconomicAsOf() = %s, want %s", got, want)
	}
}

func TestAcceptedFillIncrementConvertsCumulativeAverageToMarginalFill(t *testing.T) {
	t.Parallel()
	current := &lifecycle.Aggregate{Fills: []lifecycle.Fill{
		{Quantity: decimal.NewFromInt(2), Price: decimal.NewFromInt(10)},
		{Quantity: decimal.NewFromInt(1), Price: decimal.NewFromInt(13)},
	}}
	quantity, price, err := acceptedFillIncrement(current, 5, 11.4)
	if err != nil {
		t.Fatal(err)
	}
	if !quantity.Equal(decimal.NewFromInt(2)) || !price.Equal(decimal.NewFromInt(12)) {
		t.Fatalf("increment = %s @ %s, want 2 @ 12", quantity, price)
	}
	if _, _, err := acceptedFillIncrement(current, 3, 11); err == nil {
		t.Fatal("non-advancing cumulative fill unexpectedly accepted")
	}
}

func TestAcceptedEconomicPlannerBuildsGraphOnlyForExactRoutedOrder(t *testing.T) {
	fixture := newExecutionLifecycleFixture(t)
	originID := "operator-fixture"
	proposeInput := fixture.proposeInput("accepted-planner")
	proposeInput.OriginType = ledger.ExecutionOriginOperator
	proposeInput.OriginID = originID
	proposeInput.StrategyVersionID = ""
	proposed, err := lifecycle.Propose(proposeInput)
	if err != nil {
		t.Fatal(err)
	}
	current, err := fixture.repo.ProposeExecutionIntent(fixture.ctx, proposed)
	if err != nil {
		t.Fatal(err)
	}
	allocationInput := fixture.nextEvent(current, "accepted-allocation", "allocator", "allocation", "allocated", []byte(`{"quantity":"8"}`))
	allocation, err := lifecycle.Allocate(current, decimal.NewFromInt(8), allocationInput, allocationInput.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	current, err = fixture.repo.ApplyExecutionTransition(fixture.ctx, fixture.account.ID, allocation)
	if err != nil {
		t.Fatal(err)
	}
	riskInput := fixture.nextEvent(current, "accepted-risk", "risk", "risk-policy-v1", "approved", []byte(`{"approved":true}`))
	approval, err := lifecycle.ApproveRisk(current, riskInput, riskInput.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	current, err = fixture.repo.ApplyExecutionTransition(fixture.ctx, fixture.account.ID, approval)
	if err != nil {
		t.Fatal(err)
	}
	route := fixture.routeTransition(t, current, "accepted-planner")
	routed, err := fixture.repo.ApplyExecutionTransition(fixture.ctx, fixture.account.ID, route)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := execution.NewNonRunExecutionScope(fixture.account.ID, fixture.account.Environment, ledger.ExecutionOriginOperator, originID)
	if err != nil {
		t.Fatal(err)
	}
	filledAt := routed.Order.RoutedAt.Add(time.Second)
	price := 10.25
	order := &domain.Order{ID: routed.Order.ID, AccountID: fixture.account.ID, Environment: fixture.account.Environment, OriginType: string(ledger.ExecutionOriginOperator), OriginID: originID, ExternalID: "accepted-planner-order", Ticker: "FIXTURE", MarketType: domain.MarketTypeStock, Side: domain.OrderSideBuy, Status: domain.OrderStatusFilled, Quantity: 8, FilledQuantity: 8, FilledAvgPrice: &price, FilledAt: &filledAt}
	trade := &domain.Trade{ID: uuid.New(), AccountID: scope.AccountID(), Environment: scope.Environment(), OriginType: string(ledger.ExecutionOriginOperator), OriginID: originID, OrderID: &order.ID, Ticker: order.Ticker, Side: order.Side, Quantity: 8, Price: price, ExecutedAt: filledAt}
	mutation := repository.OrderFillInput{IdempotencyKey: "accepted-planner-fill", Order: order, FillIntent: repository.OrderFillIntent{Side: order.Side, Quantity: 8, ExecutionPrice: price}, Now: filledAt, Trade: trade}
	planner := NewAcceptedEconomicPlanner(fixture.pool)
	// Compatibility order identities must come from the prepared canonical
	// command, not the legacy order-effect UUID or its augr- client prefix.
	order.ClientOrderID = routed.Order.ClientOrderID
	order.OrderType = domain.OrderType(routed.Order.OrderType)
	if routed.Order.LimitPrice != nil {
		limitPrice := routed.Order.LimitPrice.InexactFloat64()
		order.LimitPrice = &limitPrice
	}
	if routed.Order.StopPrice != nil {
		stopPrice := routed.Order.StopPrice.InexactFloat64()
		order.StopPrice = &stopPrice
	}
	if err := planner.RequireAcceptedOrderPrepared(fixture.ctx, scope, order); err != nil {
		t.Fatalf("exact canonical command rejected: %v", err)
	}
	legacyClient := *order
	legacyClient.ClientOrderID = "augr-" + order.ID.String()
	if err := planner.RequireAcceptedOrderPrepared(fixture.ctx, scope, &legacyClient); err == nil {
		t.Fatal("legacy client identity accepted as the canonical routed command")
	}
	legacyOrder := *order
	legacyOrder.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("order-effect:v1:legacy-smoke"))
	if err := planner.RequireAcceptedOrderPrepared(fixture.ctx, scope, &legacyOrder); err == nil {
		t.Fatal("legacy order identity accepted as the canonical routed command")
	}
	for _, test := range []struct {
		name   string
		mutate func(*domain.Order)
	}{
		{"account", func(changed *domain.Order) { changed.AccountID = uuid.New() }},
		{"environment", func(changed *domain.Order) { changed.Environment = domain.AccountEnvironmentPaperStress }},
		{"origin", func(changed *domain.Order) { changed.OriginID = "foreign-origin" }},
		{"unexpected_run", func(changed *domain.Order) { id := uuid.New(); changed.PipelineRunID = &id }},
		{"quantity", func(changed *domain.Order) { changed.Quantity++ }},
		{"side", func(changed *domain.Order) { changed.Side = domain.OrderSideSell }},
		{"type", func(changed *domain.Order) { changed.OrderType = domain.OrderType("different") }},
		{"limit", func(changed *domain.Order) { value := 9876.0; changed.LimitPrice = &value }},
		{"stop", func(changed *domain.Order) { value := 9876.0; changed.StopPrice = &value }},
	} {
		t.Run("reject_changed_"+test.name, func(t *testing.T) {
			changed := *order
			test.mutate(&changed)
			if err := planner.RequireAcceptedOrderPrepared(fixture.ctx, scope, &changed); err == nil {
				t.Fatal("changed command accepted under prepared canonical identity")
			}
		})
	}
	planned, err := planner.PlanAcceptedOrderFill(context.Background(), scope, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if err := planned.Validate(); err != nil {
		t.Fatalf("planned graph is invalid: %v", err)
	}
	foreign := mutation
	foreignOrder := *order
	foreignOrder.ID = uuid.New()
	foreign.Order = &foreignOrder
	if _, err := planner.PlanAcceptedOrderFill(context.Background(), scope, foreign); err == nil {
		t.Fatal("unrouted compatibility order unexpectedly produced an accepted graph")
	}
}

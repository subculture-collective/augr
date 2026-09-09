package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

func TestSignalPreparationPersistsLotSizedScopedCommandAndRestarts(t *testing.T) {
	fixture := newExecutionLifecycleFixture(t)
	input := executionPreparationFixture(t, fixture, "signal-preparation")
	run := domain.PipelineRunRef{ID: uuid.New(), TradeDate: fixture.baseTime.Truncate(24 * time.Hour)}
	scope, err := execution.NewStrategyExecutionScope(fixture.account.ID, fixture.account.Environment, uuid.MustParse(input.Proposal.OriginID), run)
	if err != nil {
		t.Fatal(err)
	}
	plan := execution.TradingPlan{Ticker: "FIXTURE", EntryType: "limit", EntryPrice: 10.25}
	signal := execution.FinalSignal{Signal: domain.PipelineSignalBuy}
	newProvider := func() *SignalPreparation {
		p := NewSignalPreparation(fixture.repo, plan.Ticker, input.Proposal, input.Route)
		p.now = func() time.Time { return fixture.baseTime.Add(3 * time.Second) }
		return p
	}
	provider := newProvider()
	id, quantity, err := provider.Resolve(fixture.ctx, scope, signal, plan, 66.66666666666667)
	if err != nil {
		t.Fatal(err)
	}
	if quantity != 66 {
		t.Fatalf("quantity=%v want66 whole venue lots", quantity)
	}
	var count int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM execution_intents WHERE account_id=$1 AND idempotency_key=$2`, fixture.account.ID, input.Proposal.IdempotencyKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("resolution wrote an unapproved intent")
	}
	price := plan.EntryPrice
	order := domain.Order{
		ID: id, ClientOrderID: id.String(), AccountID: scope.AccountID(), Environment: scope.Environment(),
		OriginType: string(input.Proposal.OriginType), OriginID: input.Proposal.OriginID, Ticker: plan.Ticker,
		Side: domain.OrderSideBuy, OrderType: domain.OrderTypeLimit, Quantity: quantity, LimitPrice: &price,
		PipelineRunID: &run.ID, PipelineRunTradeDate: &run.TradeDate,
	}
	if err := provider.PersistApproved(fixture.ctx, scope, order); err != nil {
		t.Fatal(err)
	}
	// A fresh provider represents process restart, not merely cached input replay.
	restarted := newProvider()
	restarted.now = func() time.Time { return fixture.baseTime.Add(4 * time.Second) }
	replayedID, replayedQuantity, err := restarted.Resolve(fixture.ctx, scope, signal, plan, 66.66666666666667)
	if err != nil || replayedID != id || replayedQuantity != quantity {
		t.Fatalf("restart resolve: %s %v %v", replayedID, replayedQuantity, err)
	}
	if err := restarted.PersistApproved(fixture.ctx, scope, order); err != nil {
		t.Fatal(err)
	}
	foreignRun := run
	foreignRun.ID = uuid.New()
	foreignScope, err := execution.NewStrategyExecutionScope(fixture.account.ID, fixture.account.Environment, uuid.MustParse(input.Proposal.OriginID), foreignRun)
	if err != nil {
		t.Fatal(err)
	}
	foreignOrder := order
	foreignOrder.PipelineRunID = &foreignRun.ID
	if err := NewAcceptedEconomicPlanner(fixture.pool).RequireAcceptedOrderPrepared(fixture.ctx, foreignScope, &foreignOrder); err == nil {
		t.Fatal("canonical route accepted under a different pipeline run")
	}
	if _, _, err := newProvider().Resolve(fixture.ctx, foreignScope, signal, plan, 66.66666666666667); err == nil {
		t.Fatal("run rebinding accepted under existing intent key")
	}
}

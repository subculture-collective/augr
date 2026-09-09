package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
)

func TestSmokePreparationRejectsNonSmokeEnvironment(t *testing.T) {
	for _, environment := range []string{"production", "development", ""} {
		t.Run(environment, func(t *testing.T) {
			factory := newSmokePreparationFactory(environment, nil)
			if _, err := factory(context.Background(), execution.ExecutionScope{}, execution.TradingPlan{Ticker: "SMOKE", EntryPrice: 150}, time.Now()); err == nil {
				t.Fatal("synthetic preparation escaped smoke environment")
			}
		})
	}
}

func TestSmokePreparationPersistsCanonicalRuntimeRoute(t *testing.T) {
	if testing.Short() {
		t.Skip("requires PostgreSQL")
	}
	dsn := os.Getenv("DB_URL")
	if dsn == "" {
		t.Skip("requires DB_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("connect smoke qualification database:", err)
	}
	defer pool.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	run := domain.PipelineRunRef{ID: uuid.New(), TradeDate: now.Truncate(24 * time.Hour)}
	scope, err := execution.NewStrategyExecutionScope(uuid.MustParse("00000000-0000-4000-8000-000000000064"), domain.AccountEnvironmentPaperScored, uuid.MustParse("00000000-0000-4000-8000-000000000071"), run)
	if err != nil {
		t.Fatal(err)
	}
	plan := execution.TradingPlan{Ticker: "SMOKE", EntryType: "market", EntryPrice: 150}
	factory := newSmokePreparationFactory("smoke", pool)
	preparation, err := factory(ctx, scope, plan, now)
	if err != nil {
		t.Fatal(err)
	}
	id, quantity, err := preparation.Resolve(ctx, scope, execution.FinalSignal{Signal: domain.PipelineSignalBuy}, plan, 66.66666666666667)
	if err != nil {
		t.Fatal(err)
	}
	if quantity != 66 {
		t.Fatalf("resolved quantity=%v, want66", quantity)
	}
	origin, originID := scope.Origin()
	order := domain.Order{
		ID: id, ClientOrderID: id.String(), AccountID: scope.AccountID(), Environment: scope.Environment(),
		OriginType: string(origin), OriginID: originID, PipelineRunID: &run.ID, PipelineRunTradeDate: &run.TradeDate,
		Ticker: "SMOKE", Side: domain.OrderSideBuy, OrderType: domain.OrderTypeMarket, Quantity: quantity, ReferencePrice: &plan.EntryPrice,
	}
	if err := preparation.PersistApproved(ctx, scope, order); err != nil {
		t.Fatal(err)
	}
}

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestBuildAllocationDecisionQuery(t *testing.T) {
	strategyID := uuid.New()
	opportunityID := uuid.New()
	createdAfter := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	query, args := buildAllocationDecisionListQuery(canonicalRepositoryTestAccountID, repository.AllocationDecisionFilter{
		Mode:          domain.AllocationDecisionModeShadow,
		Action:        domain.AllocationDecisionActionShadowSelected,
		StrategyID:    &strategyID,
		OpportunityID: &opportunityID,
		CreatedAfter:  &createdAfter,
	}, 25, 50)

	if len(args) != 8 {
		t.Fatalf("expected 7 args, got %d: %#v", len(args), args)
	}
	assertContains(t, query, "mode = $2")
	assertContains(t, query, "action = $3")
	assertContains(t, query, "strategy_id = $4")
	assertContains(t, query, "opportunity_id = $5")
	assertContains(t, query, "created_at >= $6")
	assertContains(t, query, "LIMIT $7 OFFSET $8")
}

func TestAllocationDecisionRepoIntegration_CreateListAndCount(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOpportunityIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewAllocationDecisionRepo(pool, canonicalRepositoryTestAccountID)
	strategyID := createTestStrategy(t, ctx, pool)
	runID, versionID := uuid.New(), uuid.New()
	opportunityRepo := NewOpportunityRepo(pool, canonicalRepositoryTestAccountID)
	opportunity := &domain.Opportunity{
		AccountID: canonicalRepositoryTestAccountID, Environment: domain.AccountEnvironmentPaperScored,
		OriginType: "strategy_version", OriginID: versionID.String(), PipelineRunID: &runID, PipelineRunTradeDate: timePtr(canonicalRepositoryTestTradeDate),
		StrategyID:        strategyID,
		MarketType:        domain.MarketTypeStock,
		Ticker:            "AAPL",
		Side:              domain.OrderSideBuy,
		Signal:            domain.PipelineSignalBuy,
		Status:            domain.OpportunityStatusSelected,
		Confidence:        0.9,
		EdgePct:           3.1,
		ExpectedReturnPct: 5.0,
		MaxLossPct:        1.2,
		LiquidityUSD:      1500000,
		SpreadPct:         0.11,
		ProposedNotional:  5000,
		SelectedNotional:  5000,
		Reason:            "allocator selected",
		ExpiresAt:         time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC),
		DedupeKey:         "alloc-AAPL-1",
	}
	if err := opportunityRepo.Create(ctx, opportunity); err != nil {
		t.Fatalf("Create(opportunity) error = %v", err)
	}

	decision := &domain.AllocationDecision{
		AccountID: canonicalRepositoryTestAccountID, Environment: opportunity.Environment,
		OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, PipelineRunID: &runID, PipelineRunTradeDate: timePtr(canonicalRepositoryTestTradeDate),
		OpportunityID: &opportunity.ID,
		StrategyID:    &strategyID,
		Mode:          domain.AllocationDecisionModeShadow,
		Action:        domain.AllocationDecisionActionShadowSelected,
		Score:         91.5,
		NotionalUSD:   5000,
		Quantity:      25,
		Reasons:       []string{"edge", "liquidity"},
	}
	if err := repo.Create(ctx, decision); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if decision.ID == uuid.Nil || decision.CreatedAt.IsZero() {
		t.Fatal("expected Create() to populate id/timestamp")
	}

	listed, err := repo.List(ctx, repository.AllocationDecisionFilter{Mode: domain.AllocationDecisionModeShadow}, 10, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != decision.ID {
		t.Fatalf("unexpected list result: %+v", listed)
	}
	if listed[0].CreatedOrderID != nil {
		t.Fatalf("unexpected created order id: %+v", listed[0])
	}

	count, err := repo.Count(ctx, repository.AllocationDecisionFilter{Action: domain.AllocationDecisionActionShadowSelected})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
}

func TestAllocationDecisionRepoIntegration_ConflictNeverReturnsForeignLineage(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOpportunityIntegrationPool(t, ctx)
	defer cleanup()
	strategyID, runID, versionID := createTestStrategy(t, ctx, pool), uuid.New(), uuid.New()
	opportunity := &domain.Opportunity{AccountID: canonicalRepositoryTestAccountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: timePtr(canonicalRepositoryTestTradeDate), MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Status: domain.OpportunityStatusSelected, ExpiresAt: time.Now().Add(time.Hour), DedupeKey: uuid.NewString()}
	if err := NewOpportunityRepo(pool, canonicalRepositoryTestAccountID).Create(ctx, opportunity); err != nil {
		t.Fatal(err)
	}
	decision := &domain.AllocationDecision{AccountID: canonicalRepositoryTestAccountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, PipelineRunID: &runID, PipelineRunTradeDate: timePtr(canonicalRepositoryTestTradeDate), OpportunityID: &opportunity.ID, StrategyID: &strategyID, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionPaperOrderIntent}
	if err := NewAllocationDecisionRepo(pool, canonicalRepositoryTestAccountID).Create(ctx, decision); err != nil {
		t.Fatal(err)
	}
	foreign := *decision
	foreign.ID, foreign.CreatedAt, foreign.Environment = uuid.Nil, time.Time{}, domain.AccountEnvironmentLive
	if err := NewAllocationDecisionRepo(pool, canonicalRepositoryTestAccountID).Create(ctx, &foreign); err == nil {
		t.Fatal("foreign conflict returned existing allocation decision")
	}
	if foreign.ID != uuid.Nil {
		t.Fatalf("foreign decision received ID %s", foreign.ID)
	}
}

func TestAllocationDecisionRepoIntegration_RecordPaperOrderResultFencesFullLineage(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOpportunityIntegrationPool(t, ctx)
	defer cleanup()
	strategyID, runID, versionID := createTestStrategy(t, ctx, pool), uuid.New(), uuid.New()
	opportunity := &domain.Opportunity{AccountID: canonicalRepositoryTestAccountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: timePtr(canonicalRepositoryTestTradeDate), MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Status: domain.OpportunityStatusSelected, ExpiresAt: time.Now().Add(time.Hour), DedupeKey: uuid.NewString()}
	if err := NewOpportunityRepo(pool, canonicalRepositoryTestAccountID).Create(ctx, opportunity); err != nil {
		t.Fatal(err)
	}
	decision := &domain.AllocationDecision{AccountID: canonicalRepositoryTestAccountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, PipelineRunID: &runID, PipelineRunTradeDate: timePtr(canonicalRepositoryTestTradeDate), OpportunityID: &opportunity.ID, StrategyID: &strategyID, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionPaperOrderIntent}
	repo := NewAllocationDecisionRepo(pool, canonicalRepositoryTestAccountID)
	if err := repo.Create(ctx, decision); err != nil {
		t.Fatal(err)
	}
	orderID, wrongStrategyID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orders(id,account_id,environment,origin_type,origin_id,pipeline_run_id,pipeline_run_trade_date,strategy_id,allocation_opportunity_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, orderID, canonicalRepositoryTestAccountID, opportunity.Environment, opportunity.OriginType, opportunity.OriginID, runID, canonicalRepositoryTestTradeDate, wrongStrategyID, opportunity.ID); err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.RecordPaperOrderResult(ctx, decision.ID, &orderID, domain.AllocationDecisionActionExecuted, nil); err != nil || applied {
		t.Fatalf("mismatched strategy attachment = %t, %v", applied, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET strategy_id=$1 WHERE id=$2`, strategyID, orderID); err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.RecordPaperOrderResult(ctx, decision.ID, &orderID, domain.AllocationDecisionActionExecuted, nil); err != nil || !applied {
		t.Fatalf("matching attachment = %t, %v", applied, err)
	}
}

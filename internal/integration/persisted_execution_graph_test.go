package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

func TestPersistedExecutionGraphIntegration_SurvivesCancellationAndRestart(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	accountID := uuid.MustParse("00000000-0000-4000-8000-000000000064")
	strategyID, versionID := uuid.New(), uuid.New()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO strategies(id,name,ticker,market_type,is_paper,is_active,status) VALUES($1,$2,'AUGR','stock',true,true,'active')`, strategyID, "persisted-graph-"+strategyID.String()); err != nil {
		t.Fatal(err)
	}

	tradeDate := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	startedAt := tradeDate.Add(14 * time.Hour)
	originID := versionID.String()
	run := &domain.PipelineRun{
		ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored,
		OriginType: "strategy_version", OriginID: originID, StrategyID: strategyID, Ticker: "AUGR",
		TradeDate: tradeDate, Status: domain.PipelineStatusRunning, StartedAt: startedAt,
	}
	runRepo := postgres.NewPipelineRunRepo(db.Pool, accountID)
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatal(err)
	}

	snapshot := &domain.PipelineRunSnapshot{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, PipelineRunID: run.ID, PipelineRunTradeDate: tradeDate, DataType: "market", Payload: json.RawMessage(`{"ticker":"AUGR","price":10}`)}
	if err := postgres.NewPipelineRunSnapshotRepo(db.Pool, accountID).Create(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	decision := &domain.AgentDecision{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, PipelineRunID: run.ID, PipelineRunTradeDate: tradeDate, AgentRole: domain.AgentRoleTrader, Phase: domain.PhaseTrading, OutputText: "buy"}
	if err := postgres.NewAgentDecisionRepo(db.Pool, accountID).Create(ctx, decision); err != nil {
		t.Fatal(err)
	}
	runID := run.ID
	event := &domain.AgentEvent{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, StrategyID: &strategyID, AgentRole: domain.AgentRoleTrader, EventKind: "decision.persisted", Title: "Decision persisted"}
	if err := postgres.NewAgentEventRepo(db.Pool, accountID).Create(ctx, event); err != nil {
		t.Fatal(err)
	}

	journal := postgres.NewTradeDecisionJournalRepo(db.Pool, accountID)
	tradeDecision := &domain.TradeDecision{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, StrategyID: &strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, MarketType: domain.MarketTypeStock, InstrumentKey: "AUGR", Side: domain.OrderSideBuy, FairValue: 10.2, ExecutablePrice: 10, GrossEV: 0.2, NetEV: 0.15, ProposedSize: 10, ApprovedSize: 10, RiskStatus: domain.RiskDecisionApproved, Status: domain.TradeDecisionStatusCandidate}
	if err := journal.CreateWithInitialReplay(ctx, tradeDecision); err != nil {
		t.Fatal(err)
	}

	opportunity := &domain.Opportunity{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, MarketType: domain.MarketTypeStock, Ticker: "AUGR", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Status: domain.OpportunityStatusQueued, Confidence: 0.8, EntryPrice: 10, ProposedNotional: 100, Reason: "approved trade decision", Evidence: json.RawMessage(`{"trade_decision":"` + tradeDecision.ID.String() + `"}`), ExpiresAt: startedAt.Add(time.Hour), DedupeKey: "persisted-graph-" + run.ID.String()}
	opportunityRepo := postgres.NewOpportunityRepo(db.Pool, accountID)
	if err := opportunityRepo.Create(ctx, opportunity); err != nil {
		t.Fatal(err)
	}
	claimID := uuid.New()
	if claimed, err := opportunityRepo.ClaimQueuedForAllocation(ctx, opportunity.ID, claimID, startedAt, startedAt.Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("claim opportunity = %t, %v", claimed, err)
	}
	allocation := &domain.AllocationDecision{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, OpportunityID: &opportunity.ID, StrategyID: &strategyID, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionPaperOrderIntent, Score: 1, NotionalUSD: 100, Quantity: 10, Reasons: []string{"within budget"}}
	allocationRepo := postgres.NewAllocationDecisionRepo(db.Pool, accountID)
	if err := allocationRepo.Create(ctx, allocation); err != nil {
		t.Fatal(err)
	}
	order := &domain.Order{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, StrategyID: &strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, AllocationOpportunityID: &opportunity.ID, AllocationClaimID: &claimID, Ticker: "AUGR", MarketType: domain.MarketTypeStock, Side: domain.OrderSideBuy, OrderType: domain.OrderTypeMarket, Quantity: 10, Status: domain.OrderStatusPending, AssetClass: domain.AssetClassEquity}
	if err := postgres.NewOrderRepo(db.Pool, accountID).Create(ctx, order); err != nil {
		t.Fatal(err)
	}
	if recorded, err := allocationRepo.RecordPaperOrderResult(ctx, allocation.ID, &order.ID, domain.AllocationDecisionActionExecuted, []string{"paper order persisted"}); err != nil || !recorded {
		t.Fatalf("record order result = %t, %v", recorded, err)
	}
	if err := journal.AttachOrderWithReplay(ctx, tradeDecision.ID, order.ID, false, "allocator", startedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if transitioned, err := opportunityRepo.TransitionClaimedStatus(ctx, opportunity.ID, claimID, domain.OpportunityStatusSelected, domain.OpportunityStatusExecuted, ""); err != nil || !transitioned {
		t.Fatalf("complete opportunity = %t, %v", transitioned, err)
	}

	completedAt := startedAt.Add(2 * time.Minute)
	terminalEvent := &domain.AgentEvent{AccountID: accountID, Environment: run.Environment, OriginType: run.OriginType, OriginID: originID, PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, StrategyID: &strategyID, EventKind: "pipeline.cancelled", Title: "Pipeline cancelled"}
	receipt, err := runRepo.Finalize(ctx, domain.PipelineRunRef{ID: run.ID, TradeDate: tradeDate}, repository.PipelineRunFinalization{Status: domain.PipelineStatusCancelled, CompletedAt: completedAt, ErrorMessage: "operator cancellation", Event: terminalEvent})
	if err != nil || !receipt.Applied {
		t.Fatalf("cancel run = %+v, %v", receipt, err)
	}

	assertPersistedExecutionGraph(t, ctx, db, accountID, run, tradeDecision, opportunity, allocation, order)
	assertPersistedExecutionGraph(t, ctx, db, accountID, run, tradeDecision, opportunity, allocation, order)

	foreignAccount := uuid.New()
	if rows, err := postgres.NewPipelineRunRepo(db.Pool, foreignAccount).List(ctx, repository.PipelineRunFilter{}, 10, 0); err != nil || len(rows) != 0 {
		t.Fatalf("cross-account runs = %d, %v", len(rows), err)
	}
	if rows, err := postgres.NewReplayEventRepo(db.Pool, foreignAccount).ListReplayEvents(ctx, tradeDecision.ID); err != nil || len(rows) != 0 {
		t.Fatalf("cross-account replay = %d, %v", len(rows), err)
	}
}

func assertPersistedExecutionGraph(t *testing.T, ctx context.Context, db *testDB, accountID uuid.UUID, run *domain.PipelineRun, tradeDecision *domain.TradeDecision, opportunity *domain.Opportunity, allocation *domain.AllocationDecision, order *domain.Order) {
	t.Helper()
	restartedRun, err := postgres.NewPipelineRunRepo(db.Pool, accountID).Get(ctx, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})
	if err != nil || restartedRun.Status != domain.PipelineStatusCancelled {
		t.Fatalf("restart run = %+v, %v", restartedRun, err)
	}
	replay, err := postgres.NewReplayEventRepo(db.Pool, accountID).ListReplayEvents(ctx, tradeDecision.ID)
	if err != nil || len(replay) != 3 {
		t.Fatalf("restart replay = %+v, %v", replay, err)
	}
	replayTypes := make(map[domain.ReplayEventType]bool, len(replay))
	for _, event := range replay {
		replayTypes[event.EventType] = true
	}
	for _, eventType := range []domain.ReplayEventType{domain.ReplayEventTypeDecisionCreated, domain.ReplayEventTypeRiskReviewed, domain.ReplayEventTypePaperOrdered} {
		if !replayTypes[eventType] {
			t.Fatalf("restart replay missing %s: %+v", eventType, replay)
		}
	}
	var replayCount, agentEventCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(DISTINCT re.id),count(DISTINCT e.id) FROM pipeline_run_snapshots s JOIN agent_decisions d ON d.pipeline_run_id=s.pipeline_run_id AND d.pipeline_run_trade_date=s.pipeline_run_trade_date JOIN agent_events e ON e.pipeline_run_id=s.pipeline_run_id AND e.pipeline_run_trade_date=s.pipeline_run_trade_date JOIN trade_decisions td ON td.pipeline_run_id=s.pipeline_run_id AND td.pipeline_run_trade_date=s.pipeline_run_trade_date JOIN portfolio_opportunities po ON po.pipeline_run_id=s.pipeline_run_id AND po.pipeline_run_trade_date=s.pipeline_run_trade_date JOIN allocation_decisions ad ON ad.opportunity_id=po.id JOIN orders o ON o.allocation_opportunity_id=po.id AND o.id=ad.created_order_id JOIN replay_events re ON re.trade_decision_id=td.id WHERE s.pipeline_run_id=$1 AND s.account_id=$2 AND td.id=$3 AND po.id=$4 AND ad.id=$5 AND o.id=$6`, run.ID, accountID, tradeDecision.ID, opportunity.ID, allocation.ID, order.ID).Scan(&replayCount, &agentEventCount); err != nil || replayCount != 3 || agentEventCount != 2 {
		t.Fatalf("persisted graph counts replay=%d events=%d, %v, want 3/2", replayCount, agentEventCount, err)
	}
}

package portfolio

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

type paperProcessorStub struct {
	called int
	signal execution.FinalSignal
	plan   execution.TradingPlan
	scope  execution.ExecutionScope
	err    error
	result PaperOrderResult
}

func (p *paperProcessorStub) ProcessPaperOrder(_ context.Context, req PaperOrderRequest) (PaperOrderResult, error) {
	p.called++
	p.signal = req.Signal
	p.plan = req.Plan
	p.scope = req.Scope
	if p.result.OrderID == nil && !p.result.Skipped && p.err == nil {
		id := uuid.New()
		p.result.OrderID = &id
		p.result.Status = domain.OrderStatusFilled
	}
	return p.result, p.err
}

func TestPaperExecutorRejectsInvalidPreconditions(t *testing.T) {
	t.Parallel()

	runID, tradeDate, accountID, versionID := uuid.New(), time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC), uuid.New(), uuid.New()
	baseOpportunity := domain.Opportunity{
		ID:        uuid.New(),
		AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored,
		OriginType: "strategy_version", OriginID: versionID.String(),
		PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate,
		StrategyID:       uuid.New(),
		MarketType:       domain.MarketTypeStock,
		Ticker:           "AAPL",
		Side:             domain.OrderSideBuy,
		PredictionSide:   "YES",
		Signal:           domain.PipelineSignalBuy,
		Confidence:       0.8,
		EntryPrice:       100,
		MaxLossPct:       0.05,
		SelectedNotional: 2500,
	}
	baseDecision := paperDecisionForOpportunity(baseOpportunity)
	baseDecision.NotionalUSD = 2500
	baseDecision.Reasons = []string{"score=91.0"}
	baseStrategy := domain.Strategy{
		ID:                         baseOpportunity.StrategyID,
		Ticker:                     "AAPL",
		MarketType:                 domain.MarketTypeStock,
		Status:                     domain.StrategyStatusActive,
		IsPaper:                    true,
		ExecutionStrategyVersionID: &versionID,
	}

	tests := []struct {
		name        string
		opportunity domain.Opportunity
		decision    domain.AllocationDecision
		strategy    domain.Strategy
		wantReason  string
	}{
		{name: "invalid action", opportunity: baseOpportunity, decision: domain.AllocationDecision{Action: domain.AllocationDecisionActionShadowRejected}, strategy: baseStrategy, wantReason: "invalid_decision_action"},
		{name: "non paper strategy", opportunity: baseOpportunity, decision: baseDecision, strategy: func() domain.Strategy { s := baseStrategy; s.IsPaper = false; return s }(), wantReason: "strategy_not_paper"},
		{name: "inactive strategy", opportunity: baseOpportunity, decision: baseDecision, strategy: func() domain.Strategy { s := baseStrategy; s.Status = domain.StrategyStatusInactive; return s }(), wantReason: "strategy_inactive"},
		{name: "strategy mismatch", opportunity: baseOpportunity, decision: baseDecision, strategy: func() domain.Strategy { s := baseStrategy; s.ID = uuid.New(); return s }(), wantReason: "strategy_mismatch"},
		{name: "market mismatch", opportunity: baseOpportunity, decision: baseDecision, strategy: func() domain.Strategy { s := baseStrategy; s.MarketType = domain.MarketTypeCrypto; return s }(), wantReason: "market_type_mismatch"},
		{name: "missing notional", opportunity: baseOpportunity, decision: func() domain.AllocationDecision { d := baseDecision; d.NotionalUSD = 0; return d }(), strategy: baseStrategy, wantReason: "missing_notional_usd"},
		{name: "missing entry price", opportunity: func() domain.Opportunity { o := baseOpportunity; o.EntryPrice = 0; return o }(), decision: baseDecision, strategy: baseStrategy, wantReason: "missing_entry_price"},
		{name: "missing stop loss", opportunity: func() domain.Opportunity { o := baseOpportunity; o.MaxLossPct = 0; return o }(), decision: baseDecision, strategy: baseStrategy, wantReason: "missing_stop_loss"},
		{name: "source version mismatch", opportunity: func() domain.Opportunity { o := baseOpportunity; o.OriginID = uuid.NewString(); return o }(), decision: baseDecision, strategy: baseStrategy, wantReason: "decision_scope_mismatch"},
		{name: "account mismatch", opportunity: func() domain.Opportunity { o := baseOpportunity; o.AccountID = uuid.New(); return o }(), decision: baseDecision, strategy: baseStrategy, wantReason: "decision_scope_mismatch"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			processor := &paperProcessorStub{}
			binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
			exec := NewPaperExecutor(PaperExecutorDeps{Processor: processor, ExecutionAccount: binding})
			result, err := exec.ExecutePaperDecision(context.Background(), tt.opportunity, tt.decision, tt.strategy)
			if err != nil {
				t.Fatalf("ExecutePaperDecision() error = %v", err)
			}
			if result.Action != domain.AllocationDecisionActionExecutionRejected {
				t.Fatalf("action = %s, want execution_rejected", result.Action)
			}
			if result.Reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", result.Reason, tt.wantReason)
			}
			if processor.called != 0 {
				t.Fatalf("processor called %d times, want 0", processor.called)
			}
		})
	}
}

func TestPaperExecutorExecutesValidPaperDecision(t *testing.T) {
	t.Parallel()

	strategyID := uuid.New()
	versionID, accountID, runID := uuid.New(), uuid.New(), uuid.New()
	tradeDate := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	processor := &paperProcessorStub{}
	binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
	exec := NewPaperExecutor(PaperExecutorDeps{Processor: processor, ExecutionAccount: binding})
	opportunity := domain.Opportunity{
		ID:        uuid.New(),
		AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored,
		OriginType: "strategy_version", OriginID: versionID.String(),
		PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate,
		StrategyID:       strategyID,
		MarketType:       domain.MarketTypeStock,
		Ticker:           "AAPL",
		Side:             domain.OrderSideBuy,
		PredictionSide:   "YES",
		Signal:           domain.PipelineSignalBuy,
		Confidence:       0.91,
		EntryPrice:       100,
		MaxLossPct:       0.05,
		SelectedNotional: 2500,
	}
	decision := paperDecisionForOpportunity(opportunity)
	decision.NotionalUSD = 2500
	decision.Reasons = []string{"score=91.0", "multiplier=1.00"}
	strategy := domain.Strategy{
		ID:                         strategyID,
		Ticker:                     "AAPL",
		MarketType:                 domain.MarketTypeStock,
		Status:                     domain.StrategyStatusActive,
		IsPaper:                    true,
		ExecutionStrategyVersionID: &versionID,
	}

	result, err := exec.ExecutePaperDecision(context.Background(), opportunity, decision, strategy)
	if err != nil {
		t.Fatalf("ExecutePaperDecision() error = %v", err)
	}
	if result.Action != domain.AllocationDecisionActionExecuted {
		t.Fatalf("action = %s, want executed", result.Action)
	}
	if result.Reason != "" {
		t.Fatalf("reason = %q, want empty", result.Reason)
	}
	if result.OrderID == nil {
		t.Fatal("OrderID = nil, want created paper order id")
	}
	if processor.called != 1 {
		t.Fatalf("processor called %d times, want 1", processor.called)
	}
	if processor.signal.Signal != domain.PipelineSignalBuy || processor.plan.Action != domain.PipelineSignalBuy {
		t.Fatalf("unexpected signal/plan action: %+v %+v", processor.signal, processor.plan)
	}
	if processor.plan.EntryType != "market" || processor.plan.EntryPrice != 100 {
		t.Fatalf("unexpected entry plan: %+v", processor.plan)
	}
	if processor.plan.Side != "YES" {
		t.Fatalf("plan side = %q, want YES", processor.plan.Side)
	}
	if math.Abs(processor.plan.StopLoss-95) > 1e-9 {
		t.Fatalf("stop loss = %v, want 95", processor.plan.StopLoss)
	}
	if processor.plan.PositionSize != 25 {
		t.Fatalf("position size = %v, want 25", processor.plan.PositionSize)
	}
	_, originID := processor.scope.Origin()
	if originID != versionID.String() {
		t.Fatalf("scope origin = %s, want strategy version %s", originID, versionID)
	}
}

func TestPaperExecutorRejectsIncompleteDecisionOwnership(t *testing.T) {
	accountID, versionID, runID, strategyID, opportunityID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tradeDate := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	opportunity := domain.Opportunity{ID: opportunityID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, StrategyID: strategyID, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, EntryPrice: 100, MaxLossPct: .05}
	strategy := domain.Strategy{ID: strategyID, MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true, ExecutionStrategyVersionID: &versionID}
	binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
	base := paperDecisionForOpportunity(opportunity)
	base.NotionalUSD = 1000
	otherDate := tradeDate.AddDate(0, 0, 1)
	tests := map[string]func(*domain.AllocationDecision){
		"account":     func(d *domain.AllocationDecision) { d.AccountID = uuid.New() },
		"environment": func(d *domain.AllocationDecision) { d.Environment = domain.AccountEnvironmentLive },
		"origin":      func(d *domain.AllocationDecision) { d.OriginID = uuid.NewString() },
		"run":         func(d *domain.AllocationDecision) { id := uuid.New(); d.PipelineRunID = &id },
		"trade date":  func(d *domain.AllocationDecision) { d.PipelineRunTradeDate = &otherDate },
		"opportunity": func(d *domain.AllocationDecision) { id := uuid.New(); d.OpportunityID = &id },
		"strategy":    func(d *domain.AllocationDecision) { id := uuid.New(); d.StrategyID = &id },
		"claim":       func(d *domain.AllocationDecision) { d.ExecutionClaimID = uuid.Nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			decision := base
			mutate(&decision)
			processor := &paperProcessorStub{}
			result, err := NewPaperExecutor(PaperExecutorDeps{Processor: processor, ExecutionAccount: binding}).ExecutePaperDecision(context.Background(), opportunity, decision, strategy)
			if err != nil || result.Reason != "decision_scope_mismatch" || processor.called != 0 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, processor.called)
			}
		})
	}
}

func TestPaperExecutorMapsProcessorOrderStatus(t *testing.T) {
	strategyID, versionID, accountID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tradeDate := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	opportunity := domain.Opportunity{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, StrategyID: strategyID, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: .9, EntryPrice: 100, MaxLossPct: .05}
	decision := paperDecisionForOpportunity(opportunity)
	decision.NotionalUSD = 1000
	strategy := domain.Strategy{ID: strategyID, MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true, ExecutionStrategyVersionID: &versionID}
	binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)

	tests := []struct {
		status domain.OrderStatus
		want   domain.AllocationDecisionAction
	}{
		{domain.OrderStatusPending, domain.AllocationDecisionActionPaperOrderIntent},
		{domain.OrderStatusSubmitted, domain.AllocationDecisionActionPaperOrderIntent},
		{domain.OrderStatusPartial, domain.AllocationDecisionActionPaperOrderIntent},
		{domain.OrderStatusCancelled, domain.AllocationDecisionActionExecutionRejected},
		{domain.OrderStatusRejected, domain.AllocationDecisionActionExecutionRejected},
		{domain.OrderStatusFilled, domain.AllocationDecisionActionExecuted},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			orderID := uuid.New()
			processor := &paperProcessorStub{result: PaperOrderResult{OrderID: &orderID, Status: tt.status}}
			result, err := NewPaperExecutor(PaperExecutorDeps{Processor: processor, ExecutionAccount: binding}).ExecutePaperDecision(context.Background(), opportunity, decision, strategy)
			if err != nil || result.Action != tt.want {
				t.Fatalf("status %s: action=%s err=%v, want %s", tt.status, result.Action, err, tt.want)
			}
		})
	}
}

func TestPaperExecutorKeepsProcessorErrorPending(t *testing.T) {
	t.Parallel()

	strategyID := uuid.New()
	versionID, accountID, runID := uuid.New(), uuid.New(), uuid.New()
	tradeDate := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	processor := &paperProcessorStub{err: errors.New("boom")}
	binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
	exec := NewPaperExecutor(PaperExecutorDeps{Processor: processor, ExecutionAccount: binding})
	opportunity := domain.Opportunity{
		ID:        uuid.New(),
		AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored,
		OriginType: "strategy_version", OriginID: versionID.String(),
		PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate,
		StrategyID: strategyID,
		MarketType: domain.MarketTypeStock,
		Ticker:     "AAPL",
		Side:       domain.OrderSideBuy,
		Signal:     domain.PipelineSignalBuy,
		Confidence: 0.91,
		EntryPrice: 100,
		MaxLossPct: 0.05,
	}
	decision := paperDecisionForOpportunity(opportunity)
	decision.NotionalUSD = 2500
	result, err := exec.ExecutePaperDecision(context.Background(), opportunity, decision, domain.Strategy{
		ID:                         strategyID,
		Ticker:                     "AAPL",
		MarketType:                 domain.MarketTypeStock,
		Status:                     domain.StrategyStatusActive,
		IsPaper:                    true,
		ExecutionStrategyVersionID: &versionID,
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("ExecutePaperDecision() error = %v, want boom", err)
	}
	if result.Action != domain.AllocationDecisionActionPaperOrderIntent {
		t.Fatalf("action = %s, want paper_order_intent", result.Action)
	}
	if result.Reason != "processor_error:boom" {
		t.Fatalf("reason = %q, want processor_error:boom", result.Reason)
	}
}

func paperDecisionForOpportunity(opportunity domain.Opportunity) domain.AllocationDecision {
	opportunityID, strategyID := opportunity.ID, opportunity.StrategyID
	return domain.AllocationDecision{
		AccountID: opportunity.AccountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID,
		PipelineRunID: opportunity.PipelineRunID, PipelineRunTradeDate: opportunity.PipelineRunTradeDate,
		OpportunityID: &opportunityID, StrategyID: &strategyID, ExecutionClaimID: uuid.New(),
		Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionPaperOrderIntent,
	}
}

package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type portfolioAllocatorBalanceStub struct {
	balance execution.Balance
	err     error
}

func (s portfolioAllocatorBalanceStub) GetAccountBalance(context.Context) (execution.Balance, error) {
	return s.balance, s.err
}

func paperAllocatorStateDeps() (repository.PositionRepository, PortfolioAccountBalanceSource) {
	return newRecordingPositionRepo(), portfolioAllocatorBalanceStub{balance: execution.Balance{Currency: "USD", Cash: 100000, BuyingPower: 100000, Equity: 100000}}
}

type portfolioAllocatorOpportunityRepo struct {
	items             []domain.Opportunity
	lastFilter        repository.OpportunityFilter
	lastLimit         int
	lastOffset        int
	lastAsOf          time.Time
	expireCalls       int
	updateStatusCalls int
	lastStatus        domain.OpportunityStatus
	lastRejectReason  string
	statusHistory     []domain.OpportunityStatus
	claims            map[uuid.UUID]uuid.UUID
	claimExpires      map[uuid.UUID]time.Time
}

func (r *portfolioAllocatorOpportunityRepo) Create(context.Context, *domain.Opportunity) error {
	return nil
}

func (r *portfolioAllocatorOpportunityRepo) UpsertQueuedByDedupeKey(context.Context, *domain.Opportunity) error {
	return nil
}

func (r *portfolioAllocatorOpportunityRepo) Get(context.Context, uuid.UUID) (*domain.Opportunity, error) {
	return nil, repository.ErrNotFound
}

func (r *portfolioAllocatorOpportunityRepo) List(_ context.Context, filter repository.OpportunityFilter, limit, offset int) ([]domain.Opportunity, error) {
	r.lastFilter = filter
	r.lastLimit = limit
	r.lastOffset = offset
	if filter.Status == "" {
		return append([]domain.Opportunity(nil), r.items...), nil
	}
	out := make([]domain.Opportunity, 0, len(r.items))
	for _, item := range r.items {
		if item.Status == filter.Status {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *portfolioAllocatorOpportunityRepo) ExpireQueuedBefore(_ context.Context, before time.Time) (int64, error) {
	r.expireCalls++
	r.lastAsOf = before
	count := int64(0)
	for i := range r.items {
		if r.items[i].Status == domain.OpportunityStatusQueued && !r.items[i].ExpiresAt.After(before) {
			r.items[i].Status = domain.OpportunityStatusExpired
			r.items[i].RejectReason = "expired_before_allocation"
			count++
		}
	}
	return count, nil
}

func (r *portfolioAllocatorOpportunityRepo) ListQueuedForAllocation(_ context.Context, asOf time.Time) ([]domain.Opportunity, error) {
	r.lastAsOf = asOf
	out := make([]domain.Opportunity, 0, len(r.items))
	for _, item := range r.items {
		if item.Status == domain.OpportunityStatusQueued && item.ExpiresAt.After(asOf) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *portfolioAllocatorOpportunityRepo) ListSelectedForAllocation(_ context.Context, claimID uuid.UUID, asOf time.Time) ([]domain.Opportunity, error) {
	r.lastAsOf = asOf
	out := make([]domain.Opportunity, 0)
	for _, item := range r.items {
		claimExpiry := r.claimExpires[item.ID]
		if item.Status == domain.OpportunityStatusSelected && (r.claims[item.ID] == claimID || claimExpiry.IsZero() || !claimExpiry.After(asOf)) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *portfolioAllocatorOpportunityRepo) ClaimQueuedForAllocation(_ context.Context, id, claimID uuid.UUID, _, expires time.Time) (bool, error) {
	for i := range r.items {
		if r.items[i].ID != id || r.items[i].Status != domain.OpportunityStatusQueued {
			continue
		}
		r.items[i].Status = domain.OpportunityStatusSelected
		r.statusHistory = append(r.statusHistory, domain.OpportunityStatusSelected)
		r.updateStatusCalls++
		if r.claims == nil {
			r.claims = map[uuid.UUID]uuid.UUID{}
		}
		if r.claimExpires == nil {
			r.claimExpires = map[uuid.UUID]time.Time{}
		}
		r.claims[id], r.claimExpires[id] = claimID, expires
		return true, nil
	}
	return false, nil
}

func (r *portfolioAllocatorOpportunityRepo) TakeOverExpiredAllocationClaim(_ context.Context, id, claimID uuid.UUID, asOf, expires time.Time) (bool, error) {
	for i := range r.items {
		if r.items[i].ID != id || r.items[i].Status != domain.OpportunityStatusSelected || (r.claims[id] != claimID && r.claimExpires[id].After(asOf)) {
			continue
		}
		if r.claims == nil {
			r.claims = map[uuid.UUID]uuid.UUID{}
		}
		if r.claimExpires == nil {
			r.claimExpires = map[uuid.UUID]time.Time{}
		}
		r.claims[id], r.claimExpires[id] = claimID, expires
		return true, nil
	}
	return false, nil
}

func (r *portfolioAllocatorOpportunityRepo) RenewAllocationClaim(_ context.Context, id, claimID uuid.UUID, lease time.Duration) (bool, error) {
	if r.claims[id] != claimID || !r.claimExpires[id].After(time.Now()) {
		return false, nil
	}
	r.claimExpires[id] = time.Now().Add(lease)
	return true, nil
}

func (r *portfolioAllocatorOpportunityRepo) TransitionClaimedStatus(ctx context.Context, id, claimID uuid.UUID, from, to domain.OpportunityStatus, reason string) (bool, error) {
	if r.claims[id] != claimID {
		return false, nil
	}
	for i := range r.items {
		if r.items[i].ID == id && r.items[i].Status == from {
			delete(r.claims, id)
			delete(r.claimExpires, id)
			return true, r.UpdateStatus(ctx, id, to, reason)
		}
	}
	return false, nil
}

func (r *portfolioAllocatorOpportunityRepo) Count(_ context.Context, filter repository.OpportunityFilter) (int, error) {
	items, _ := r.List(context.Background(), filter, 0, 0)
	return len(items), nil
}

func (r *portfolioAllocatorOpportunityRepo) UpdateStatus(_ context.Context, id uuid.UUID, status domain.OpportunityStatus, rejectReason string) error {
	r.updateStatusCalls++
	r.lastStatus = status
	r.lastRejectReason = rejectReason
	r.statusHistory = append(r.statusHistory, status)
	for i := range r.items {
		if r.items[i].ID == id {
			r.items[i].Status = status
			r.items[i].RejectReason = rejectReason
		}
	}
	return nil
}

func (r *portfolioAllocatorOpportunityRepo) TransitionStatus(ctx context.Context, id uuid.UUID, from, to domain.OpportunityStatus, rejectReason string) (bool, error) {
	for i := range r.items {
		if r.items[i].ID != id || r.items[i].Status != from {
			continue
		}
		return true, r.UpdateStatus(ctx, id, to, rejectReason)
	}
	return false, nil
}

type portfolioAllocatorDecisionRepo struct {
	created []*domain.AllocationDecision
}

type portfolioAllocatorRunRepo struct {
	runs map[uuid.UUID]domain.PipelineRun
	err  error
}

var allocatorRunTradeDate = time.Date(2026, time.August, 27, 0, 0, 0, 0, time.UTC)

func persistedRunsForOpportunities(items []domain.Opportunity) *portfolioAllocatorRunRepo {
	runs := make(map[uuid.UUID]domain.PipelineRun, len(items))
	for i := range items {
		runID, accountID, versionID := uuid.New(), uuid.New(), uuid.New()
		items[i].PipelineRunID = &runID
		items[i].PipelineRunTradeDate = &allocatorRunTradeDate
		items[i].AccountID = accountID
		items[i].Environment = domain.AccountEnvironmentPaperScored
		items[i].OriginType = "strategy_version"
		items[i].OriginID = versionID.String()
		runs[runID] = domain.PipelineRun{ID: runID, AccountID: accountID, Environment: items[i].Environment, OriginType: items[i].OriginType, OriginID: items[i].OriginID, StrategyID: items[i].StrategyID, TradeDate: allocatorRunTradeDate, Status: domain.PipelineStatusCompleted, Signal: items[i].Signal}
	}
	return &portfolioAllocatorRunRepo{runs: runs}
}

func (*portfolioAllocatorRunRepo) Create(context.Context, *domain.PipelineRun) error { return nil }

func (r *portfolioAllocatorRunRepo) Get(_ context.Context, ref domain.PipelineRunRef) (*domain.PipelineRun, error) {
	if r.err != nil {
		return nil, r.err
	}
	run, ok := r.runs[ref.ID]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &run, nil
}

func (*portfolioAllocatorRunRepo) List(context.Context, repository.PipelineRunFilter, int, int) ([]domain.PipelineRun, error) {
	return nil, nil
}

func (*portfolioAllocatorRunRepo) Count(context.Context, repository.PipelineRunFilter) (int, error) {
	return 0, nil
}

func (*portfolioAllocatorRunRepo) Finalize(_ context.Context, ref domain.PipelineRunRef, value repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	id, tradeDate := ref.ID, ref.TradeDate
	return repository.PipelineRunFinalizationReceipt{Applied: true, Run: domain.PipelineRun{ID: id, TradeDate: tradeDate, Status: value.Status, CompletedAt: &value.CompletedAt}}, nil
}

func (*portfolioAllocatorRunRepo) RefineCompletedSignal(context.Context, domain.PipelineRunRef, domain.PipelineSignal, domain.PipelineSignal) (repository.PipelineRunFinalizationReceipt, error) {
	return repository.PipelineRunFinalizationReceipt{}, nil
}

func (r *portfolioAllocatorDecisionRepo) Create(_ context.Context, decision *domain.AllocationDecision) error {
	cloned := *decision
	r.created = append(r.created, &cloned)
	return nil
}

func (r *portfolioAllocatorDecisionRepo) List(_ context.Context, filter repository.AllocationDecisionFilter, limit, offset int) ([]domain.AllocationDecision, error) {
	_ = limit
	_ = offset
	out := make([]domain.AllocationDecision, 0, len(r.created))
	for _, decision := range r.created {
		if filter.Mode != "" && decision.Mode != filter.Mode {
			continue
		}
		if filter.OpportunityID != nil && (decision.OpportunityID == nil || *decision.OpportunityID != *filter.OpportunityID) {
			continue
		}
		out = append(out, *decision)
	}
	return out, nil
}

func (r *portfolioAllocatorDecisionRepo) RecordPaperOrderResult(_ context.Context, id, _ uuid.UUID, orderID *uuid.UUID, action domain.AllocationDecisionAction, reasons []string) (bool, error) {
	for _, decision := range r.created {
		if decision.ID == id && decision.Action == domain.AllocationDecisionActionPaperOrderIntent {
			decision.Action, decision.Reasons, decision.CreatedOrderID = action, append([]string(nil), reasons...), orderID
			return true, nil
		}
	}
	return false, nil
}

func (r *portfolioAllocatorDecisionRepo) Count(_ context.Context, filter repository.AllocationDecisionFilter) (int, error) {
	decisions, _ := r.List(context.Background(), filter, 0, 0)
	return len(decisions), nil
}

type portfolioAllocatorStrategyRepo struct {
	strategy *domain.Strategy
	getCalls int
}

func (r *portfolioAllocatorStrategyRepo) Create(context.Context, *domain.Strategy) error { return nil }
func (r *portfolioAllocatorStrategyRepo) CreateWithExecutionVersion(ctx context.Context, strategy *domain.Strategy) (uuid.UUID, error) {
	if err := r.Create(ctx, strategy); err != nil {
		return uuid.Nil, err
	}
	return uuid.New(), nil
}
func (*portfolioAllocatorStrategyRepo) ResolveExecutionVersionID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (r *portfolioAllocatorStrategyRepo) Get(_ context.Context, id uuid.UUID) (*domain.Strategy, error) {
	r.getCalls++
	if r.strategy == nil || r.strategy.ID != id {
		return nil, repository.ErrNotFound
	}
	cloned := *r.strategy
	return &cloned, nil
}

func (r *portfolioAllocatorStrategyRepo) List(context.Context, repository.StrategyFilter, int, int) ([]domain.Strategy, error) {
	if r.strategy == nil {
		return nil, nil
	}
	return []domain.Strategy{*r.strategy}, nil
}

func (r *portfolioAllocatorStrategyRepo) Count(context.Context, repository.StrategyFilter) (int, error) {
	return 0, nil
}

func (r *portfolioAllocatorStrategyRepo) Update(context.Context, *domain.Strategy) error { return nil }

func (r *portfolioAllocatorStrategyRepo) Delete(context.Context, uuid.UUID) error { return nil }

func (r *portfolioAllocatorStrategyRepo) UpdateThesis(context.Context, uuid.UUID, json.RawMessage) error {
	return nil
}

func (r *portfolioAllocatorStrategyRepo) GetThesisRaw(context.Context, uuid.UUID) (json.RawMessage, error) {
	return nil, nil
}

type portfolioPaperProcessorStub struct {
	called int
	signal execution.FinalSignal
	plan   execution.TradingPlan
	scope  execution.ExecutionScope
}

type restartPaperProcessor struct {
	orderID          uuid.UUID
	calls            int
	completedEffects int
	orders           *portfolioRecoveryOrderRepo
	errAfterEffect   bool
	reconcileCalls   int
}

func (p *restartPaperProcessor) ReconcilePaperOrder(_ context.Context, _ domain.Opportunity, order *domain.Order, _ uuid.UUID) (portfolio.PaperOrderResult, error) {
	p.reconcileCalls++
	return portfolio.PaperOrderResult{OrderID: &order.ID, Status: order.Status}, nil
}

func (p *restartPaperProcessor) ProcessPaperOrder(ctx context.Context, req portfolio.PaperOrderRequest) (portfolio.PaperOrderResult, error) {
	p.calls++
	if p.orderID == uuid.Nil {
		p.orderID = uuid.New()
		p.completedEffects++
		run, _ := req.Scope.PipelineRun()
		originType, originID := req.Scope.Origin()
		strategyID := req.Opportunity.StrategyID
		_ = p.orders.Create(ctx, &domain.Order{ID: p.orderID, AccountID: req.Scope.AccountID(), Environment: req.Scope.Environment(), OriginType: string(originType), OriginID: originID, StrategyID: &strategyID, PipelineRunID: &run.ID, PipelineRunTradeDate: &run.TradeDate, AllocationOpportunityID: &req.Opportunity.ID, Status: domain.OrderStatusFilled})
	}
	if p.errAfterEffect {
		p.errAfterEffect = false
		return portfolio.PaperOrderResult{OrderID: &p.orderID, Status: domain.OrderStatusSubmitted}, errors.New("post-submit transport error")
	}
	return portfolio.PaperOrderResult{OrderID: &p.orderID, Status: domain.OrderStatusFilled}, nil
}

type portfolioRecoveryOrderRepo struct{ *recordingOrderRepo }

type terminalRecoveryProcessor struct {
	status domain.OrderStatus
	calls  int
}

func (p *terminalRecoveryProcessor) ProcessPaperOrder(context.Context, portfolio.PaperOrderRequest) (portfolio.PaperOrderResult, error) {
	return portfolio.PaperOrderResult{}, errors.New("unexpected submit during recovery")
}

func (p *terminalRecoveryProcessor) ReconcilePaperOrder(_ context.Context, _ domain.Opportunity, order *domain.Order, _ uuid.UUID) (portfolio.PaperOrderResult, error) {
	p.calls++
	order.Status = p.status
	return portfolio.PaperOrderResult{OrderID: &order.ID, Status: p.status}, nil
}

func (r *portfolioRecoveryOrderRepo) GetByAllocationOpportunity(_ context.Context, opportunity domain.Opportunity) (*domain.Order, error) {
	for _, order := range r.list {
		if order.AllocationOpportunityID != nil && *order.AllocationOpportunityID == opportunity.ID {
			return order, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (r *portfolioRecoveryOrderRepo) GetByRun(_ context.Context, ref domain.PipelineRunRef, _ repository.OrderFilter, _, _ int) ([]domain.Order, error) {
	out := make([]domain.Order, 0)
	for _, order := range r.list {
		if order.PipelineRunID != nil && *order.PipelineRunID == ref.ID && order.PipelineRunTradeDate != nil && order.PipelineRunTradeDate.Equal(ref.TradeDate) {
			out = append(out, *order)
		}
	}
	return out, nil
}

type crashAfterEffectDecisionRepo struct {
	portfolioAllocatorDecisionRepo
	failCreate bool
}

func (r *crashAfterEffectDecisionRepo) Create(ctx context.Context, decision *domain.AllocationDecision) error {
	if r.failCreate {
		r.failCreate = false
		return errors.New("simulated crash after completed effect")
	}
	return r.portfolioAllocatorDecisionRepo.Create(ctx, decision)
}

func (p *portfolioPaperProcessorStub) ProcessPaperOrder(_ context.Context, req portfolio.PaperOrderRequest) (portfolio.PaperOrderResult, error) {
	p.called++
	p.signal = req.Signal
	p.plan = req.Plan
	p.scope = req.Scope
	id := uuid.New()
	return portfolio.PaperOrderResult{OrderID: &id, Status: domain.OrderStatusFilled}, nil
}

func (p *portfolioPaperProcessorStub) ReconcilePaperOrder(_ context.Context, _ domain.Opportunity, order *domain.Order, _ uuid.UUID) (portfolio.PaperOrderResult, error) {
	return portfolio.PaperOrderResult{OrderID: &order.ID, Status: order.Status}, nil
}

type preclaimFailOpportunityRepo struct {
	portfolioAllocatorOpportunityRepo
}

type recoverySnapshotProbeRepo struct {
	portfolioAllocatorOpportunityRepo
	sawReleased bool
}

func (r *recoverySnapshotProbeRepo) ListQueuedForAllocation(_ context.Context, _ time.Time) ([]domain.Opportunity, error) {
	r.sawReleased = len(r.items) == 1 && r.items[0].Status == domain.OpportunityStatusQueued
	return nil, errors.New("snapshot probe")
}

type concurrentClaimOpportunityRepo struct {
	portfolioAllocatorOpportunityRepo
	mu sync.Mutex
}

func (r *concurrentClaimOpportunityRepo) TransitionStatus(_ context.Context, id uuid.UUID, from, to domain.OpportunityStatus, _ string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) != 1 || r.items[0].ID != id || r.items[0].Status != from {
		return false, nil
	}
	r.items[0].Status = to
	return true, nil
}

func (r *concurrentClaimOpportunityRepo) ClaimQueuedForAllocation(ctx context.Context, id, claimID uuid.UUID, claimedAt, expires time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.portfolioAllocatorOpportunityRepo.ClaimQueuedForAllocation(ctx, id, claimID, claimedAt, expires)
}

func (r *concurrentClaimOpportunityRepo) TakeOverExpiredAllocationClaim(ctx context.Context, id, claimID uuid.UUID, asOf, expires time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.portfolioAllocatorOpportunityRepo.TakeOverExpiredAllocationClaim(ctx, id, claimID, asOf, expires)
}

func (r *concurrentClaimOpportunityRepo) RenewAllocationClaim(ctx context.Context, id, claimID uuid.UUID, lease time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.portfolioAllocatorOpportunityRepo.RenewAllocationClaim(ctx, id, claimID, lease)
}

func (r *concurrentClaimOpportunityRepo) ListSelectedForAllocation(ctx context.Context, claimID uuid.UUID, asOf time.Time) ([]domain.Opportunity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.portfolioAllocatorOpportunityRepo.ListSelectedForAllocation(ctx, claimID, asOf)
}

func (r *concurrentClaimOpportunityRepo) TransitionClaimedStatus(ctx context.Context, id, claimID uuid.UUID, from, to domain.OpportunityStatus, reason string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.portfolioAllocatorOpportunityRepo.TransitionClaimedStatus(ctx, id, claimID, from, to, reason)
}

func (r *preclaimFailOpportunityRepo) TransitionStatus(_ context.Context, _ uuid.UUID, _, status domain.OpportunityStatus, rejectReason string) (bool, error) {
	r.updateStatusCalls++
	r.lastStatus = status
	r.lastRejectReason = rejectReason
	r.statusHistory = append(r.statusHistory, status)
	if r.updateStatusCalls == 1 {
		return false, fmt.Errorf("preclaim failed")
	}
	return true, nil
}

func (r *preclaimFailOpportunityRepo) ClaimQueuedForAllocation(_ context.Context, _ uuid.UUID, _ uuid.UUID, _, _ time.Time) (bool, error) {
	return false, fmt.Errorf("preclaim failed")
}

func TestPortfolioAllocatorJobRegistrationWithNilDeps(t *testing.T) {
	t.Parallel()

	orch := NewJobOrchestrator(OrchestratorDeps{})
	orch.registerPortfolioAllocatorJobs()
	if _, ok := orch.jobs["portfolio_allocator"]; ok {
		t.Fatal("portfolio_allocator registered unexpectedly")
	}
}

func TestValidatePortfolioOpportunitySourcesRejectsFailedAndMismatchedRuns(t *testing.T) {
	t.Parallel()

	strategyID := uuid.New()
	accountID, versionID := uuid.New(), uuid.New()
	scopeFields := func(runID uuid.UUID, status domain.PipelineStatus, signal domain.PipelineSignal) domain.PipelineRun {
		return domain.PipelineRun{ID: runID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, TradeDate: allocatorRunTradeDate, Status: status, Signal: signal}
	}
	validRunID := uuid.New()
	failedRunID := uuid.New()
	mismatchedRunID := uuid.New()
	missingRunID := uuid.New()
	runRepo := &portfolioAllocatorRunRepo{runs: map[uuid.UUID]domain.PipelineRun{
		validRunID:      scopeFields(validRunID, domain.PipelineStatusCompleted, domain.PipelineSignalBuy),
		failedRunID:     scopeFields(failedRunID, domain.PipelineStatusFailed, domain.PipelineSignalHold),
		mismatchedRunID: scopeFields(mismatchedRunID, domain.PipelineStatusCompleted, domain.PipelineSignalSell),
	}}
	opportunityRepo := &portfolioAllocatorOpportunityRepo{}
	orch := NewJobOrchestrator(OrchestratorDeps{RunRepo: runRepo, OpportunityRepo: opportunityRepo})
	opportunities := []domain.Opportunity{
		{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &validRunID, PipelineRunTradeDate: &allocatorRunTradeDate, Signal: domain.PipelineSignalBuy},
		{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &failedRunID, PipelineRunTradeDate: &allocatorRunTradeDate, Signal: domain.PipelineSignalBuy},
		{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &mismatchedRunID, PipelineRunTradeDate: &allocatorRunTradeDate, Signal: domain.PipelineSignalBuy},
		{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &missingRunID, PipelineRunTradeDate: &allocatorRunTradeDate, Signal: domain.PipelineSignalBuy, Status: domain.OpportunityStatusQueued},
		{ID: uuid.New(), StrategyID: strategyID, Signal: domain.PipelineSignalBuy, Status: domain.OpportunityStatusQueued},
	}
	opportunityRepo.items = append([]domain.Opportunity(nil), opportunities...)

	valid, rejected, err := orch.validatePortfolioOpportunitySources(context.Background(), opportunities, portfolio.AllocatorModeShadow)
	if err != nil {
		t.Fatalf("validatePortfolioOpportunitySources() error = %v", err)
	}
	if len(valid) != 1 || valid[0].ID != opportunities[0].ID {
		t.Fatalf("valid opportunities = %#v, want only completed matching source", valid)
	}
	if len(rejected) != 2 {
		t.Fatalf("rejected decisions = %#v, want two lineage-valid rejections", rejected)
	}
	wantReasons := []string{"source_run_not_completed", "source_signal_mismatch"}
	for i, want := range wantReasons {
		if len(rejected[i].Reasons) != 1 || rejected[i].Reasons[0] != want {
			t.Fatalf("rejected[%d] reasons = %v, want %q", i, rejected[i].Reasons, want)
		}
	}
	if opportunityRepo.lastStatus != domain.OpportunityStatusRejected || opportunityRepo.lastRejectReason != "source_run_missing" {
		t.Fatalf("malformed opportunity status=%q reason=%q", opportunityRepo.lastStatus, opportunityRepo.lastRejectReason)
	}
	if len(opportunityRepo.statusHistory) != 2 {
		t.Fatalf("quarantined status history=%v, want missing row and malformed row", opportunityRepo.statusHistory)
	}
}

func TestValidatePortfolioOpportunitySourcesFailsClosedOnRunRepositoryError(t *testing.T) {
	t.Parallel()

	runID := uuid.New()
	orch := NewJobOrchestrator(OrchestratorDeps{RunRepo: &portfolioAllocatorRunRepo{err: errors.New("run store unavailable")}})
	_, _, err := orch.validatePortfolioOpportunitySources(context.Background(), []domain.Opportunity{{ID: uuid.New(), PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate}}, portfolio.AllocatorModeShadow)
	if err == nil || !strings.Contains(err.Error(), "load source run") {
		t.Fatalf("validatePortfolioOpportunitySources() error = %v, want repository failure", err)
	}
}

func TestValidatePortfolioOpportunitySourcesRequiresRunRepositoryInShadowMode(t *testing.T) {
	orch := NewJobOrchestrator(OrchestratorDeps{})
	_, _, err := orch.validatePortfolioOpportunitySources(context.Background(), []domain.Opportunity{{ID: uuid.New()}}, portfolio.AllocatorModeShadow)
	if err == nil || !strings.Contains(err.Error(), "shadow mode requires pipeline run repository") {
		t.Fatalf("validatePortfolioOpportunitySources() error = %v", err)
	}
}

func TestValidatePortfolioOpportunitySourcesRejectsConflictingAccountRetry(t *testing.T) {
	runID, strategyID, accountID, versionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	run := domain.PipelineRun{ID: runID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, TradeDate: allocatorRunTradeDate, Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy}
	opportunity := domain.Opportunity{ID: uuid.New(), AccountID: uuid.New(), Environment: run.Environment, OriginType: run.OriginType, OriginID: run.OriginID, StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, Signal: run.Signal}
	orch := NewJobOrchestrator(OrchestratorDeps{RunRepo: &portfolioAllocatorRunRepo{runs: map[uuid.UUID]domain.PipelineRun{runID: run}}})
	valid, rejected, err := orch.validatePortfolioOpportunitySources(context.Background(), []domain.Opportunity{opportunity}, portfolio.AllocatorModePaper)
	if err != nil {
		t.Fatal(err)
	}
	if len(valid) != 0 || len(rejected) != 1 || len(rejected[0].Reasons) != 1 || rejected[0].Reasons[0] != "source_scope_mismatch" {
		t.Fatalf("valid=%+v rejected=%+v", valid, rejected)
	}
}

func TestPortfolioAllocatorJobPersistsShadowDecisions(t *testing.T) {
	t.Parallel()

	now := time.Now()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{
		{
			ID:                uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			StrategyID:        uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			Status:            domain.OpportunityStatusQueued,
			MarketType:        domain.MarketTypeStock,
			Ticker:            "AAPL",
			Side:              domain.OrderSideBuy,
			Signal:            domain.PipelineSignalBuy,
			Confidence:        1,
			EdgePct:           0.05,
			ExpectedReturnPct: 0.1,
			MaxLossPct:        0.01,
			LiquidityUSD:      5_000_000,
			MarketCapUSD:      10_000_000_000,
			SpreadPct:         0.001,
			ProposedNotional:  2_000,
			Reason:            "strong shadow opportunity",
			ExpiresAt:         now.Add(24 * time.Hour),
			CreatedAt:         now.Add(-time.Hour),
			DedupeKey:         "aapl-shadow-1",
		},
	}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	runRepo := persistedRunsForOpportunities(opportunityRepo.items)
	orch := NewJobOrchestrator(OrchestratorDeps{
		OpportunityRepo:        opportunityRepo,
		AllocationDecisionRepo: decisionRepo,
		RunRepo:                runRepo,
	})
	orch.registerPortfolioAllocatorJobs()
	job, ok := orch.jobs["portfolio_allocator"]
	if !ok {
		t.Fatal("portfolio_allocator job not registered")
	}

	if err := job.Fn(context.Background()); err != nil {
		t.Fatalf("job run error = %v", err)
	}
	if opportunityRepo.updateStatusCalls != 1 || opportunityRepo.lastStatus != domain.OpportunityStatusSelected {
		t.Fatalf("UpdateStatus calls/status = %d/%s, want selected once", opportunityRepo.updateStatusCalls, opportunityRepo.lastStatus)
	}
	if len(decisionRepo.created) == 0 {
		t.Fatal("expected shadow decisions to be persisted")
	}
	if decisionRepo.created[0].Mode != domain.AllocationDecisionModeShadow {
		t.Fatalf("decision mode = %s, want shadow", decisionRepo.created[0].Mode)
	}
	if decisionRepo.created[0].Action != domain.AllocationDecisionActionShadowSelected {
		t.Fatalf("decision action = %s, want shadow_selected", decisionRepo.created[0].Action)
	}
	status := orch.Status()
	if len(status) == 0 || status[0].LastSummary == nil {
		t.Fatal("expected orchestrator to record last summary")
	}
}

func TestPortfolioAllocatorJobSecondShadowRunDoesNotRepeatSelectedOpportunity(t *testing.T) {
	t.Parallel()

	now := time.Now()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), StrategyID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: 0.05, ExpectedReturnPct: 0.1, MaxLossPct: 0.01, LiquidityUSD: 5_000_000, MarketCapUSD: 10_000_000_000, SpreadPct: 0.001, ProposedNotional: 2_000, Reason: "strong shadow opportunity", ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now.Add(-time.Hour), DedupeKey: "aapl-shadow-1"}}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	runRepo := persistedRunsForOpportunities(opportunityRepo.items)
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo, RunRepo: runRepo})
	orch.registerPortfolioAllocatorJobs()
	job := orch.jobs["portfolio_allocator"]
	if job == nil {
		t.Fatal("portfolio_allocator job not registered")
	}
	if err := job.Fn(context.Background()); err != nil {
		t.Fatalf("first run error = %v", err)
	}
	if len(decisionRepo.created) != 1 || decisionRepo.created[0].Action != domain.AllocationDecisionActionShadowSelected {
		t.Fatalf("unexpected first run decisions: %+v", decisionRepo.created)
	}
	opportunityRepo.items[0].Status = domain.OpportunityStatusSelected
	decisionRepo.created = nil
	if err := job.Fn(context.Background()); err != nil {
		t.Fatalf("second run error = %v", err)
	}
	if len(decisionRepo.created) != 0 {
		t.Fatalf("expected no repeat decisions, got %+v", decisionRepo.created)
	}
}

func TestPortfolioAllocatorJobUpdatesShadowStatuses(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{ID: uuid.New(), StrategyID: uuid.New(), Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: 0.05, ExpectedReturnPct: 0.1, MaxLossPct: 0.01, LiquidityUSD: 5_000_000, MarketCapUSD: 10_000_000_000, SpreadPct: 0.001, ProposedNotional: 2_000, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour), DedupeKey: "selected"}, {ID: uuid.New(), StrategyID: uuid.New(), Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "MSFT", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 0.1, EdgePct: 0.001, ExpectedReturnPct: 0.1, MaxLossPct: 0.01, LiquidityUSD: 1, MarketCapUSD: 1, SpreadPct: 0.2, ProposedNotional: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour), DedupeKey: "rejected"}}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	runRepo := persistedRunsForOpportunities(opportunityRepo.items)
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo, RunRepo: runRepo})
	orch.registerPortfolioAllocatorJobs()
	if err := orch.jobs["portfolio_allocator"].Fn(context.Background()); err != nil {
		t.Fatalf("job run error = %v", err)
	}
	if opportunityRepo.updateStatusCalls != 2 {
		t.Fatalf("update calls = %d, want 2", opportunityRepo.updateStatusCalls)
	}
	if opportunityRepo.statusHistory[0] != domain.OpportunityStatusSelected || opportunityRepo.statusHistory[1] != domain.OpportunityStatusRejected {
		t.Fatalf("status history = %+v", opportunityRepo.statusHistory)
	}
}

func TestPortfolioAllocatorJobExpiresDueBeforeAllocation(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	opp := domain.Opportunity{ID: uuid.New(), StrategyID: uuid.New(), Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: 0.05, ExpectedReturnPct: 0.1, MaxLossPct: 0.01, LiquidityUSD: 1, MarketCapUSD: 1, SpreadPct: 0.001, ProposedNotional: 1, ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour), DedupeKey: "expired-queued"}
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{opp}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo})
	orch.registerPortfolioAllocatorJobs()
	if err := orch.jobs["portfolio_allocator"].Fn(context.Background()); err != nil {
		t.Fatalf("job run error = %v", err)
	}
	if opportunityRepo.expireCalls != 1 || opportunityRepo.items[0].Status != domain.OpportunityStatusExpired {
		t.Fatalf("expire behavior unexpected: %+v", opportunityRepo)
	}
	if len(decisionRepo.created) != 0 {
		t.Fatalf("expected no decisions, got %d", len(decisionRepo.created))
	}
	if got := orch.Status()[0].LastSummary; got == nil || got["expired"] != 1 || got["queued_loaded"] != 0 {
		t.Fatalf("summary = %+v", got)
	}
}

func TestPortfolioAllocatorJobLoadsCompleteSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	items := make([]domain.Opportunity, 205)
	for i := range items {
		items[i] = domain.Opportunity{ID: uuid.New(), StrategyID: uuid.New(), Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: 0.05, ExpectedReturnPct: 0.1, MaxLossPct: 0.01, LiquidityUSD: 1, MarketCapUSD: 1, SpreadPct: 0.001, ProposedNotional: 1, ExpiresAt: now.Add(time.Duration(i+1) * time.Minute), CreatedAt: now.Add(-time.Duration(i) * time.Minute), DedupeKey: uuid.NewString()}
	}
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: items}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	runRepo := persistedRunsForOpportunities(opportunityRepo.items)
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo, RunRepo: runRepo})
	orch.registerPortfolioAllocatorJobs()
	if err := orch.jobs["portfolio_allocator"].Fn(context.Background()); err != nil {
		t.Fatalf("job run error = %v", err)
	}
	if opportunityRepo.lastLimit != 0 || len(decisionRepo.created) != 205 {
		t.Fatalf("snapshot/decisions unexpected: limit=%d decisions=%d", opportunityRepo.lastLimit, len(decisionRepo.created))
	}
}

func TestPortfolioAllocatorJobReportsLifecycleCounts(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{ID: uuid.New(), StrategyID: uuid.New(), Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: 0.05, ExpectedReturnPct: 0.1, MaxLossPct: 0.01, LiquidityUSD: 1, MarketCapUSD: 1, SpreadPct: 0.001, ProposedNotional: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour), DedupeKey: "queued"}}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	runRepo := persistedRunsForOpportunities(opportunityRepo.items)
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo, RunRepo: runRepo})
	orch.registerPortfolioAllocatorJobs()
	if err := orch.jobs["portfolio_allocator"].Fn(context.Background()); err != nil {
		t.Fatalf("job run error = %v", err)
	}
	summary := orch.Status()[0].LastSummary
	for _, key := range []string{"expired", "queued_loaded", "evaluated", "persisted_decisions"} {
		if _, ok := summary[key]; !ok {
			t.Fatalf("missing summary key %q in %#v", key, summary)
		}
	}
}

func TestPortfolioAllocatorJobPaperModeExecutesPaperIntent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	runID := uuid.New()
	tradeDate := now.UTC().Truncate(24 * time.Hour)
	accountID, versionID := uuid.New(), uuid.New()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{
		{
			ID:                   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			StrategyID:           uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			PipelineRunID:        &runID,
			PipelineRunTradeDate: &tradeDate,
			AccountID:            accountID,
			Environment:          domain.AccountEnvironmentPaperScored,
			OriginType:           "strategy_version",
			OriginID:             versionID.String(),
			Status:               domain.OpportunityStatusQueued,
			MarketType:           domain.MarketTypeStock,
			Ticker:               "AAPL",
			Side:                 domain.OrderSideBuy,
			Signal:               domain.PipelineSignalBuy,
			Confidence:           1,
			EdgePct:              0.05,
			ExpectedReturnPct:    0.1,
			MaxLossPct:           0.05,
			EntryPrice:           100,
			LiquidityUSD:         5_000_000,
			MarketCapUSD:         10_000_000_000,
			SpreadPct:            0.001,
			ProposedNotional:     2_000,
			Reason:               "strong paper opportunity",
			ExpiresAt:            now.Add(24 * time.Hour),
			CreatedAt:            now.Add(-time.Hour),
			DedupeKey:            "aapl-paper-1",
		},
	}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	strategyRepo := &portfolioAllocatorStrategyRepo{strategy: &domain.Strategy{
		ID:                         opportunityRepo.items[0].StrategyID,
		Name:                       "paper-aapl",
		Ticker:                     "AAPL",
		MarketType:                 domain.MarketTypeStock,
		Status:                     domain.StrategyStatusActive,
		IsPaper:                    true,
		ExecutionStrategyVersionID: &versionID,
	}}
	processor := &portfolioPaperProcessorStub{}
	positionRepo, accountBalance := paperAllocatorStateDeps()
	orch := NewJobOrchestrator(OrchestratorDeps{
		ExecutionAccount: func() domain.ExecutionAccountBinding {
			binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
			return binding
		}(),
		OpportunityRepo:         opportunityRepo,
		AllocationDecisionRepo:  decisionRepo,
		StrategyRepo:            strategyRepo,
		PortfolioAllocatorMode:  portfolio.AllocatorModePaper,
		PortfolioPaperProcessor: processor,
		PositionRepo:            positionRepo,
		PortfolioAccountBalance: accountBalance,
		RunRepo: &portfolioAllocatorRunRepo{runs: map[uuid.UUID]domain.PipelineRun{
			runID: {ID: runID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: opportunityRepo.items[0].StrategyID, TradeDate: tradeDate, Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy},
		}},
	})
	orch.registerPortfolioAllocatorJobs()
	job := orch.jobs["portfolio_allocator"]
	if job == nil {
		t.Fatal("portfolio_allocator job not registered")
	}

	if err := job.Fn(context.Background()); err != nil {
		t.Fatalf("job run error = %v", err)
	}
	if processor.called != 1 {
		t.Fatalf("processor called %d times, want 1", processor.called)
	}
	if len(opportunityRepo.statusHistory) != 2 || opportunityRepo.statusHistory[0] != domain.OpportunityStatusSelected || opportunityRepo.statusHistory[1] != domain.OpportunityStatusExecuted {
		t.Fatalf("status history = %+v, want preclaim then final executed", opportunityRepo.statusHistory)
	}
	if len(decisionRepo.created) == 0 {
		t.Fatal("expected paper decision to be persisted")
	}
	decision := decisionRepo.created[0]
	if decision.Mode != domain.AllocationDecisionModePaper {
		t.Fatalf("decision mode = %s, want paper", decision.Mode)
	}
	if decision.Action != domain.AllocationDecisionActionExecuted {
		t.Fatalf("decision action = %s, want executed", decision.Action)
	}
	if decision.CreatedOrderID == nil {
		t.Fatal("CreatedOrderID = nil, want created paper order id")
	}
	if decision.AccountID != accountID || decision.Environment != domain.AccountEnvironmentPaperScored || decision.OriginID != versionID.String() || processor.scope.AccountID() != accountID {
		t.Fatalf("paper graph lineage decision=%+v scope=%s/%s", decision, processor.scope.AccountID(), processor.scope.Environment())
	}
	if decisionRepo.created[0].OpportunityID == nil || *decisionRepo.created[0].OpportunityID != opportunityRepo.items[0].ID {
		t.Fatalf("unexpected opportunity id on decision: %+v", decisionRepo.created[0])
	}
	if status := orch.Status()[0].LastSummary; status["executed"] != 1 || status["execution_rejected"] != 0 {
		t.Fatalf("summary counts = %#v, want executed=1 execution_rejected=0", status)
	}
	if opportunityRepo.updateStatusCalls != 2 || opportunityRepo.lastStatus != domain.OpportunityStatusExecuted {
		t.Fatalf("UpdateStatus calls/status = %d/%s, want preclaim+executed", opportunityRepo.updateStatusCalls, opportunityRepo.lastStatus)
	}
	if processor.plan.EntryType != "market" || processor.plan.EntryPrice != 100 || math.Abs(processor.plan.StopLoss-95) > 1e-9 {
		t.Fatalf("unexpected paper plan: %+v", processor.plan)
	}
}

func TestPortfolioAllocatorJobRestartRetriesCompletedEffectFromDurableClaim(t *testing.T) {
	now := time.Now().UTC()
	runID, accountID, versionID, strategyID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: .05, ExpectedReturnPct: .1, MaxLossPct: .05, EntryPrice: 100, LiquidityUSD: 5_000_000, MarketCapUSD: 10_000_000_000, SpreadPct: .001, ProposedNotional: 2_000, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour), DedupeKey: "restart-completed-effect"}}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	orders := &portfolioRecoveryOrderRepo{newRecordingOrderRepo()}
	processor := &restartPaperProcessor{orders: orders, errAfterEffect: true}
	positionRepo, balance := paperAllocatorStateDeps()
	binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
	deps := OrchestratorDeps{ExecutionAccount: binding, OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo, StrategyRepo: &portfolioAllocatorStrategyRepo{strategy: &domain.Strategy{ID: strategyID, MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true, ExecutionStrategyVersionID: &versionID}}, PortfolioAllocatorMode: portfolio.AllocatorModePaper, PortfolioPaperProcessor: processor, PositionRepo: positionRepo, OrderRepo: orders, PortfolioAccountBalance: balance, RunRepo: &portfolioAllocatorRunRepo{runs: map[uuid.UUID]domain.PipelineRun{runID: {ID: runID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, TradeDate: allocatorRunTradeDate, Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy}}}}

	first := NewJobOrchestrator(deps)
	first.registerPortfolioAllocatorJobs()
	if err := first.jobs["portfolio_allocator"].Fn(context.Background()); err == nil || !strings.Contains(err.Error(), "post-submit transport error") {
		t.Fatalf("first run error = %v, want post-submit transport error", err)
	}
	if opportunityRepo.items[0].Status != domain.OpportunityStatusSelected || processor.completedEffects != 1 || decisionRepo.created[0].Action != domain.AllocationDecisionActionPaperOrderIntent {
		t.Fatalf("durable claim/effect = %s/%d, want selected/1", opportunityRepo.items[0].Status, processor.completedEffects)
	}
	opportunityRepo.claimExpires[opportunityRepo.items[0].ID] = time.Now().Add(-time.Second)

	restarted := NewJobOrchestrator(deps)
	restarted.registerPortfolioAllocatorJobs()
	if err := restarted.jobs["portfolio_allocator"].Fn(context.Background()); err != nil {
		t.Fatalf("restart run error = %v", err)
	}
	if processor.calls != 1 || processor.completedEffects != 1 || processor.reconcileCalls != 1 {
		t.Fatalf("processor calls/effects/reconciliations = %d/%d/%d, want fill repair without duplicate execution", processor.calls, processor.completedEffects, processor.reconcileCalls)
	}
	if opportunityRepo.items[0].Status != domain.OpportunityStatusExecuted || len(decisionRepo.created) != 1 || decisionRepo.created[0].CreatedOrderID == nil || *decisionRepo.created[0].CreatedOrderID != processor.orderID {
		t.Fatalf("restart did not reconcile completed effect: opportunity=%+v decisions=%+v", opportunityRepo.items[0], decisionRepo.created)
	}
}

func TestPortfolioAllocatorRestartProgressesNonterminalIntentToCancelled(t *testing.T) {
	now := time.Now().UTC()
	runID, accountID, versionID, strategyID, opportunityID, orderID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	opportunity := domain.Opportunity{ID: opportunityID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, Status: domain.OpportunityStatusSelected, ExpiresAt: now.Add(time.Hour)}
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{opportunity}}
	decisionRepo := &portfolioAllocatorDecisionRepo{created: []*domain.AllocationDecision{{ID: uuid.New(), AccountID: accountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, OpportunityID: &opportunityID, StrategyID: &strategyID, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionPaperOrderIntent, CreatedOrderID: &orderID}}}
	orders := &portfolioRecoveryOrderRepo{newRecordingOrderRepo(&domain.Order{ID: orderID, AccountID: accountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, StrategyID: &strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, AllocationOpportunityID: &opportunityID, Status: domain.OrderStatusSubmitted})}
	processor := &terminalRecoveryProcessor{status: domain.OrderStatusCancelled}
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo, OrderRepo: orders, PortfolioPaperProcessor: processor})
	if err := orch.recoverSelectedPaperOpportunities(context.Background(), uuid.New(), now); err != nil {
		t.Fatal(err)
	}
	if opportunityRepo.items[0].Status != domain.OpportunityStatusRejected {
		t.Fatalf("opportunity status = %s, want rejected", opportunityRepo.items[0].Status)
	}
	if decisionRepo.created[0].Action != domain.AllocationDecisionActionExecutionRejected || processor.calls != 1 {
		t.Fatalf("pending decision did not terminally reconcile: %+v calls=%d", decisionRepo.created[0], processor.calls)
	}
}

func TestPortfolioAllocatorRestartRetriesMissingOrderEffect(t *testing.T) {
	now := time.Now().UTC()
	opportunityID, oldOwner, accountID, versionID, strategyID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	opportunity := domain.Opportunity{ID: opportunityID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, Status: domain.OpportunityStatusSelected, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, EntryPrice: 100, MaxLossPct: .05, ExpiresAt: now.Add(time.Hour)}
	repo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{opportunity}, claims: map[uuid.UUID]uuid.UUID{opportunityID: oldOwner}, claimExpires: map[uuid.UUID]time.Time{opportunityID: now.Add(-time.Minute)}}
	decisionRepo := &portfolioAllocatorDecisionRepo{created: []*domain.AllocationDecision{{ID: uuid.New(), AccountID: accountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, OpportunityID: &opportunityID, StrategyID: &strategyID, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionPaperOrderIntent, NotionalUSD: 1000}}}
	processor := &portfolioPaperProcessorStub{}
	binding, _ := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
	orch := NewJobOrchestrator(OrchestratorDeps{ExecutionAccount: binding, OpportunityRepo: repo, AllocationDecisionRepo: decisionRepo, StrategyRepo: &portfolioAllocatorStrategyRepo{strategy: &domain.Strategy{ID: strategyID, MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true, ExecutionStrategyVersionID: &versionID}}, PortfolioPaperProcessor: processor})
	if err := orch.recoverSelectedPaperOpportunities(context.Background(), uuid.New(), now); err != nil {
		t.Fatal(err)
	}
	if repo.items[0].Status != domain.OpportunityStatusExecuted || decisionRepo.created[0].Action != domain.AllocationDecisionActionExecuted || processor.called != 1 {
		t.Fatalf("missing effect was not retried: opportunity=%+v decision=%+v calls=%d", repo.items[0], decisionRepo.created[0], processor.called)
	}
}

func TestPortfolioAllocatorRecoveryLeavesActiveClaimUntouched(t *testing.T) {
	now := time.Now().UTC()
	opportunityID, activeOwner := uuid.New(), uuid.New()
	repo := &portfolioAllocatorOpportunityRepo{
		items:        []domain.Opportunity{{ID: opportunityID, Status: domain.OpportunityStatusSelected, ExpiresAt: now.Add(time.Hour)}},
		claims:       map[uuid.UUID]uuid.UUID{opportunityID: activeOwner},
		claimExpires: map[uuid.UUID]time.Time{opportunityID: now.Add(time.Minute)},
	}
	decisions := &portfolioAllocatorDecisionRepo{}
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: repo, AllocationDecisionRepo: decisions})
	if err := orch.recoverSelectedPaperOpportunities(context.Background(), uuid.New(), now); err != nil {
		t.Fatal(err)
	}
	if repo.items[0].Status != domain.OpportunityStatusSelected || repo.claims[opportunityID] != activeOwner || len(decisions.created) != 0 {
		t.Fatalf("active claim changed: opportunity=%+v owner=%s decisions=%d", repo.items[0], repo.claims[opportunityID], len(decisions.created))
	}
}

func TestPortfolioAllocatorRecoveryReleasesBeforeRestartSnapshot(t *testing.T) {
	now := time.Now().UTC()
	opportunityID, oldOwner := uuid.New(), uuid.New()
	repo := &recoverySnapshotProbeRepo{portfolioAllocatorOpportunityRepo: portfolioAllocatorOpportunityRepo{
		items:        []domain.Opportunity{{ID: opportunityID, Status: domain.OpportunityStatusSelected, ExpiresAt: now.Add(time.Hour)}},
		claims:       map[uuid.UUID]uuid.UUID{opportunityID: oldOwner},
		claimExpires: map[uuid.UUID]time.Time{opportunityID: now.Add(-time.Minute)},
	}}
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: repo, AllocationDecisionRepo: &portfolioAllocatorDecisionRepo{}, PortfolioAllocatorMode: portfolio.AllocatorModePaper})
	err := orch.runPortfolioAllocator(context.Background())
	if err == nil || !strings.Contains(err.Error(), "snapshot probe") {
		t.Fatalf("run error = %v, want snapshot probe", err)
	}
	if !repo.sawReleased {
		t.Fatal("queued snapshot ran before expired claim recovery")
	}
}

func TestPortfolioAllocatorConcurrentRecoverersCreateOneDecision(t *testing.T) {
	now := time.Now().UTC()
	runID, accountID, versionID, strategyID, opportunityID, orderID, oldOwner := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	opportunity := domain.Opportunity{ID: opportunityID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, Status: domain.OpportunityStatusSelected, ExpiresAt: now.Add(time.Hour)}
	repo := &concurrentClaimOpportunityRepo{portfolioAllocatorOpportunityRepo: portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{opportunity}, claims: map[uuid.UUID]uuid.UUID{opportunityID: oldOwner}, claimExpires: map[uuid.UUID]time.Time{opportunityID: now.Add(-time.Minute)}}}
	decisions := &portfolioAllocatorDecisionRepo{}
	orders := &portfolioRecoveryOrderRepo{newRecordingOrderRepo(&domain.Order{ID: orderID, AccountID: accountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, StrategyID: &strategyID, PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, AllocationOpportunityID: &opportunityID, Status: domain.OrderStatusFilled})}
	processor := &terminalRecoveryProcessor{status: domain.OrderStatusFilled}
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: repo, AllocationDecisionRepo: decisions, OrderRepo: orders, PortfolioPaperProcessor: processor})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := orch.recoverSelectedPaperOpportunities(context.Background(), uuid.New(), now); err != nil {
				t.Errorf("recover: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(decisions.created) != 1 || repo.items[0].Status != domain.OpportunityStatusExecuted || processor.calls != 1 {
		t.Fatalf("recovery decisions/status/repairs = %d/%s/%d, want 1/executed/1", len(decisions.created), repo.items[0].Status, processor.calls)
	}
}

func TestPortfolioAllocatorJobPaperModeRejectsWithoutStrategyRepo(t *testing.T) {
	t.Parallel()

	now := time.Now()
	runID := uuid.New()
	accountID, versionID := uuid.New(), uuid.New()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{
		ID:            uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		StrategyID:    uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate,
		AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(),
		Status:            domain.OpportunityStatusQueued,
		MarketType:        domain.MarketTypeStock,
		Ticker:            "MSFT",
		Side:              domain.OrderSideBuy,
		Signal:            domain.PipelineSignalBuy,
		Confidence:        1,
		EdgePct:           0.05,
		ExpectedReturnPct: 0.1,
		MaxLossPct:        0.05,
		EntryPrice:        100,
		LiquidityUSD:      5_000_000,
		MarketCapUSD:      10_000_000_000,
		SpreadPct:         0.001,
		ProposedNotional:  2_000,
		Reason:            "paper opportunity without strategy repo",
		ExpiresAt:         now.Add(24 * time.Hour),
		CreatedAt:         now.Add(-time.Hour),
		DedupeKey:         "msft-paper-1",
	}}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	positionRepo, accountBalance := paperAllocatorStateDeps()
	orch := NewJobOrchestrator(OrchestratorDeps{
		OpportunityRepo:         opportunityRepo,
		AllocationDecisionRepo:  decisionRepo,
		PortfolioAllocatorMode:  portfolio.AllocatorModePaper,
		PositionRepo:            positionRepo,
		PortfolioAccountBalance: accountBalance,
		RunRepo: &portfolioAllocatorRunRepo{runs: map[uuid.UUID]domain.PipelineRun{
			runID: {ID: runID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: opportunityRepo.items[0].StrategyID, TradeDate: allocatorRunTradeDate, Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy},
		}},
	})
	orch.registerPortfolioAllocatorJobs()
	job := orch.jobs["portfolio_allocator"]
	if job == nil {
		t.Fatal("portfolio_allocator job not registered")
	}

	if err := job.Fn(context.Background()); err != nil {
		t.Fatalf("job run error = %v", err)
	}
	if len(decisionRepo.created) == 0 {
		t.Fatal("expected rejection decision to be persisted")
	}
	if decisionRepo.created[0].Action != domain.AllocationDecisionActionExecutionRejected {
		t.Fatalf("decision action = %s, want execution_rejected", decisionRepo.created[0].Action)
	}
	if got := strings.Join(decisionRepo.created[0].Reasons, ";"); !strings.Contains(got, "missing_strategy_repo") {
		t.Fatalf("expected missing_strategy_repo reason, got %q", got)
	}
}

func TestPortfolioAllocatorJobPaperModeStopsWhenPreclaimFails(t *testing.T) {
	t.Parallel()

	now := time.Now()
	runID := uuid.New()
	accountID, versionID := uuid.New(), uuid.New()
	opportunityRepo := &preclaimFailOpportunityRepo{portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: uuid.New(), PipelineRunID: &runID, PipelineRunTradeDate: &allocatorRunTradeDate, Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock, Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: 0.05, ExpectedReturnPct: 0.1, MaxLossPct: 0.05, EntryPrice: 100, LiquidityUSD: 5_000_000, MarketCapUSD: 10_000_000_000, SpreadPct: 0.001, ProposedNotional: 2_000, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now.Add(-time.Hour), DedupeKey: "aapl-paper-1"}}}}
	decisionRepo := &portfolioAllocatorDecisionRepo{}
	strategyRepo := &portfolioAllocatorStrategyRepo{strategy: &domain.Strategy{ID: opportunityRepo.items[0].StrategyID, Name: "paper-aapl", Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true}}
	processor := &portfolioPaperProcessorStub{}
	positionRepo, accountBalance := paperAllocatorStateDeps()
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: opportunityRepo, AllocationDecisionRepo: decisionRepo, StrategyRepo: strategyRepo, PortfolioAllocatorMode: portfolio.AllocatorModePaper, PortfolioPaperProcessor: processor, PositionRepo: positionRepo, PortfolioAccountBalance: accountBalance, RunRepo: &portfolioAllocatorRunRepo{runs: map[uuid.UUID]domain.PipelineRun{runID: {ID: runID, AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: opportunityRepo.items[0].StrategyID, TradeDate: allocatorRunTradeDate, Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy}}}})
	orch.registerPortfolioAllocatorJobs()
	err := orch.jobs["portfolio_allocator"].Fn(context.Background())
	if err == nil || !strings.Contains(err.Error(), "preclaim opportunity selected") {
		t.Fatalf("expected preclaim failure, got %v", err)
	}
	if processor.called != 0 {
		t.Fatalf("processor called %d times, want 0", processor.called)
	}
	if len(decisionRepo.created) != 0 {
		t.Fatalf("expected no persisted decision, got %+v", decisionRepo.created)
	}
}

func TestPortfolioAllocatorPaperPreclaimHasOneConcurrentWinner(t *testing.T) {
	opportunityID := uuid.New()
	repo := &concurrentClaimOpportunityRepo{portfolioAllocatorOpportunityRepo: portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{ID: opportunityID, Status: domain.OpportunityStatusQueued}}}}
	orch := NewJobOrchestrator(OrchestratorDeps{OpportunityRepo: repo})
	decision := domain.AllocationDecision{OpportunityID: &opportunityID}
	const workers = 32
	results := make(chan bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			now := time.Now().UTC()
			claimed, err := orch.preclaimPaperOpportunity(context.Background(), decision, uuid.New(), now)
			if err != nil {
				t.Errorf("preclaim: %v", err)
			}
			results <- claimed
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("preclaim winners = %d, want 1", winners)
	}
}

func TestPortfolioAllocatorPaperModeRequiresCompleteFinancialState(t *testing.T) {
	t.Parallel()

	base := func() OrchestratorDeps {
		return OrchestratorDeps{
			OpportunityRepo:        &portfolioAllocatorOpportunityRepo{},
			AllocationDecisionRepo: &portfolioAllocatorDecisionRepo{},
			PortfolioAllocatorMode: portfolio.AllocatorModePaper,
		}
	}
	tests := map[string]func() OrchestratorDeps{
		"missing positions": base,
		"missing balance": func() OrchestratorDeps {
			deps := base()
			deps.PositionRepo = newRecordingPositionRepo()
			return deps
		},
		"balance error": func() OrchestratorDeps {
			deps := base()
			deps.PositionRepo = newRecordingPositionRepo()
			deps.PortfolioAccountBalance = portfolioAllocatorBalanceStub{err: errors.New("balance unavailable")}
			return deps
		},
		"invalid balance": func() OrchestratorDeps {
			deps := base()
			deps.PositionRepo = newRecordingPositionRepo()
			deps.PortfolioAccountBalance = portfolioAllocatorBalanceStub{balance: execution.Balance{Equity: 0, BuyingPower: 100}}
			return deps
		},
	}
	for name, makeDeps := range tests {
		name, makeDeps := name, makeDeps
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deps := makeDeps()
			orch := NewJobOrchestrator(deps)
			orch.registerPortfolioAllocatorJobs()
			if err := orch.jobs["portfolio_allocator"].Fn(context.Background()); err == nil {
				t.Fatalf("paper allocator error = nil, want fail-closed financial state error")
			}
		})
	}
}

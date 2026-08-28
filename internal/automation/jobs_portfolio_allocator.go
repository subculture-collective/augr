package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/google/uuid"
)

var portfolioAllocatorSpec = scheduler.ScheduleSpec{
	Type:         scheduler.ScheduleTypeAfterHours,
	Cron:         "15,45 * * * *",
	SkipWeekends: true,
	SkipHolidays: true,
}

const portfolioAllocationClaimLease = portfolio.AllocationClaimLease

// PortfolioAccountBalanceSource supplies the restored paper account state used
// to size paper allocator decisions.
type PortfolioAccountBalanceSource interface {
	GetAccountBalance(context.Context) (execution.Balance, error)
}

func (o *JobOrchestrator) registerPortfolioAllocatorJobs() {
	if o.deps.OpportunityRepo == nil || o.deps.AllocationDecisionRepo == nil {
		o.logger.Info("portfolio_allocator: skipped — repositories not configured")
		return
	}

	o.Register("portfolio_allocator", "Shadow portfolio allocator", portfolioAllocatorSpec, o.runPortfolioAllocator)
}

func (o *JobOrchestrator) runPortfolioAllocator(ctx context.Context) error {
	if o.deps.OpportunityRepo == nil || o.deps.AllocationDecisionRepo == nil {
		return fmt.Errorf("portfolio_allocator: repositories not configured")
	}
	mode := o.portfolioAllocatorMode()
	asOf := time.Now().UTC()
	claimID := uuid.New()
	expired, err := o.deps.OpportunityRepo.ExpireQueuedBefore(ctx, asOf)
	if err != nil {
		return fmt.Errorf("portfolio_allocator: expire opportunities: %w", err)
	}
	if mode == portfolio.AllocatorModePaper {
		if err := o.recoverSelectedPaperOpportunities(ctx, claimID, asOf); err != nil {
			return err
		}
	}
	opportunities, err := o.deps.OpportunityRepo.ListQueuedForAllocation(ctx, asOf)
	if err != nil {
		return fmt.Errorf("portfolio_allocator: snapshot opportunities: %w", err)
	}

	state, warnings, err := o.buildPortfolioAllocatorState(ctx, mode)
	if err != nil {
		return err
	}

	validOpportunities, sourceRejected, err := o.validatePortfolioOpportunitySources(ctx, opportunities, mode)
	if err != nil {
		return err
	}

	allocCfg := portfolio.DefaultAllocatorConfig()
	allocCfg.Mode = mode
	result := portfolio.AllocateShadow(validOpportunities, state, allocCfg)
	result.Decisions = append(sourceRejected, result.Decisions...)
	result.Summary.Evaluated += len(sourceRejected)
	result.Summary.Rejected += len(sourceRejected)
	opportunityByID := make(map[uuid.UUID]domain.Opportunity, len(opportunities))
	for _, opportunity := range opportunities {
		opportunityByID[opportunity.ID] = opportunity
	}

	for i := range result.Decisions {
		decision := result.Decisions[i]
		persisted := false
		decision.Mode = domain.AllocationDecisionMode(mode)
		if opportunity, ok := opportunityByIDValue(decision, opportunityByID); ok {
			decision.AccountID = opportunity.AccountID
			decision.Environment = opportunity.Environment
			decision.OriginType = opportunity.OriginType
			decision.OriginID = opportunity.OriginID
			decision.PipelineRunID = opportunity.PipelineRunID
			decision.PipelineRunTradeDate = opportunity.PipelineRunTradeDate
		}

		if mode == portfolio.AllocatorModePaper && decision.Action == domain.AllocationDecisionActionShadowSelected {
			claimed, err := o.preclaimPaperOpportunity(ctx, decision, claimID, asOf)
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			decision.Mode = domain.AllocationDecisionModePaper
			decision.Action = domain.AllocationDecisionActionPaperOrderIntent
			decision.ExecutionClaimID = claimID
			if err := o.deps.AllocationDecisionRepo.Create(ctx, &decision); err != nil {
				return fmt.Errorf("portfolio_allocator: persist paper intent: %w", err)
			}
			persisted = true
			decision, err = withAllocationClaim(ctx, o.deps.OpportunityRepo, *decision.OpportunityID, claimID, func(effectCtx context.Context) (domain.AllocationDecision, error) {
				return o.executePaperAllocatorDecision(effectCtx, decision, opportunityByID)
			})
			applied, recordErr := o.deps.AllocationDecisionRepo.RecordPaperOrderResult(ctx, decision.ID, claimID, decision.CreatedOrderID, decision.Action, decision.Reasons)
			if recordErr != nil {
				return fmt.Errorf("portfolio_allocator: record paper result: %w", recordErr)
			}
			if !applied {
				return fmt.Errorf("portfolio_allocator: record paper result: compare-and-swap failed")
			}
			if err != nil {
				return fmt.Errorf("portfolio_allocator: paper execution ambiguous: %w", err)
			}
		}
		result.Decisions[i] = decision

		if !persisted {
			if err := o.deps.AllocationDecisionRepo.Create(ctx, &decision); err != nil {
				return fmt.Errorf("portfolio_allocator: persist decision: %w", err)
			}
		}
		var owner *uuid.UUID
		if mode == portfolio.AllocatorModePaper && decision.Action != domain.AllocationDecisionActionShadowRejected {
			owner = &claimID
		}
		if err := o.updateOpportunityStatus(ctx, decision, owner); err != nil {
			return err
		}
	}

	summary := map[string]int{
		"expired":             int(expired),
		"queued_loaded":       len(opportunities),
		"evaluated":           result.Summary.Evaluated,
		"eligible":            result.Summary.Eligible,
		"selected":            result.Summary.Selected,
		"rejected":            result.Summary.Rejected,
		"executed":            countDecisionActions(result.Decisions, domain.AllocationDecisionActionExecuted),
		"execution_rejected":  countDecisionActions(result.Decisions, domain.AllocationDecisionActionExecutionRejected),
		"source_rejected":     len(sourceRejected),
		"persisted_decisions": len(result.Decisions),
		"warnings":            len(warnings),
	}
	o.SetLastSummary("portfolio_allocator", summary)

	fields := []any{
		slog.Int("expired", int(expired)),
		slog.Int("queued_loaded", len(opportunities)),
		slog.Int("queued_opportunities", len(opportunities)),
		slog.Int("evaluated", result.Summary.Evaluated),
		slog.Int("eligible", result.Summary.Eligible),
		slog.Int("selected", result.Summary.Selected),
		slog.Int("rejected", result.Summary.Rejected),
		slog.Int("executed", countDecisionActions(result.Decisions, domain.AllocationDecisionActionExecuted)),
		slog.Int("execution_rejected", countDecisionActions(result.Decisions, domain.AllocationDecisionActionExecutionRejected)),
		slog.Int("source_rejected", len(sourceRejected)),
		slog.Int("persisted_decisions", len(result.Decisions)),
	}
	for _, warning := range warnings {
		fields = append(fields, slog.String("warning", warning))
	}
	o.logger.Info("portfolio_allocator: completed", fields...)
	return nil
}

func (o *JobOrchestrator) validatePortfolioOpportunitySources(ctx context.Context, opportunities []domain.Opportunity, mode portfolio.AllocatorMode) ([]domain.Opportunity, []domain.AllocationDecision, error) {
	if len(opportunities) == 0 {
		return nil, nil, nil
	}
	if o.deps.RunRepo == nil {
		return nil, nil, fmt.Errorf("portfolio_allocator: %s mode requires pipeline run repository", mode)
	}

	valid := make([]domain.Opportunity, 0, len(opportunities))
	rejected := make([]domain.AllocationDecision, 0)
	for i := range opportunities {
		opportunity := opportunities[i]
		reason := ""
		malformedSource := opportunity.PipelineRunID == nil || opportunity.PipelineRunID != nil && *opportunity.PipelineRunID == uuid.Nil || opportunity.PipelineRunTradeDate == nil
		if malformedSource {
			if o.deps.OpportunityRepo == nil {
				return nil, nil, fmt.Errorf("portfolio_allocator: opportunity repository is required to quarantine malformed source")
			}
			applied, err := o.deps.OpportunityRepo.TransitionStatus(ctx, opportunity.ID, domain.OpportunityStatusQueued, domain.OpportunityStatusRejected, "source_run_missing")
			if err != nil {
				return nil, nil, fmt.Errorf("portfolio_allocator: quarantine malformed opportunity: %w", err)
			}
			if applied {
				o.logger.Warn("portfolio_allocator: quarantined malformed opportunity", "opportunity_id", opportunity.ID, "reason", "source_run_missing")
			}
			continue
		} else {
			run, err := o.deps.RunRepo.Get(ctx, domain.PipelineRunRef{ID: *opportunity.PipelineRunID, TradeDate: *opportunity.PipelineRunTradeDate})
			switch {
			case err == nil && run == nil:
				reason = "source_run_missing"
			case errors.Is(err, repository.ErrNotFound):
				reason = "source_run_missing"
			case err != nil:
				return nil, nil, fmt.Errorf("portfolio_allocator: load source run: %w", err)
			case run.Status != domain.PipelineStatusCompleted:
				reason = "source_run_not_completed"
			case run.AccountID != opportunity.AccountID || run.Environment != opportunity.Environment || run.OriginType != opportunity.OriginType || run.OriginID != opportunity.OriginID || !run.TradeDate.Equal(*opportunity.PipelineRunTradeDate):
				reason = "source_scope_mismatch"
			case run.StrategyID != opportunity.StrategyID:
				reason = "source_strategy_mismatch"
			case run.Signal != opportunity.Signal || (run.Signal != domain.PipelineSignalBuy && run.Signal != domain.PipelineSignalSell):
				reason = "source_signal_mismatch"
			}
		}
		if reason == "source_run_missing" {
			if o.deps.OpportunityRepo == nil {
				return nil, nil, fmt.Errorf("portfolio_allocator: opportunity repository is required to quarantine missing source")
			}
			applied, err := o.deps.OpportunityRepo.TransitionStatus(ctx, opportunity.ID, domain.OpportunityStatusQueued, domain.OpportunityStatusRejected, reason)
			if err != nil {
				return nil, nil, fmt.Errorf("portfolio_allocator: quarantine missing-source opportunity: %w", err)
			}
			if applied {
				o.logger.Warn("portfolio_allocator: quarantined missing-source opportunity", "opportunity_id", opportunity.ID, "reason", reason)
			}
			continue
		}
		if reason == "" {
			valid = append(valid, opportunity)
			continue
		}
		opportunityID := opportunity.ID
		strategyID := opportunity.StrategyID
		rejected = append(rejected, domain.AllocationDecision{
			AccountID:            opportunity.AccountID,
			Environment:          opportunity.Environment,
			OriginType:           opportunity.OriginType,
			OriginID:             opportunity.OriginID,
			PipelineRunID:        opportunity.PipelineRunID,
			PipelineRunTradeDate: opportunity.PipelineRunTradeDate,
			OpportunityID:        &opportunityID,
			StrategyID:           &strategyID,
			Mode:                 domain.AllocationDecisionMode(mode),
			Action:               domain.AllocationDecisionActionShadowRejected,
			Score:                -1,
			Reasons:              []string{reason},
		})
	}
	return valid, rejected, nil
}

func (o *JobOrchestrator) recoverSelectedPaperOpportunities(ctx context.Context, claimID uuid.UUID, asOf time.Time) error {
	selected, err := o.deps.OpportunityRepo.ListSelectedForAllocation(ctx, claimID, asOf)
	if err != nil {
		return fmt.Errorf("portfolio_allocator: load selected opportunity claims: %w", err)
	}
	for i := range selected {
		opportunity := selected[i]
		claimed, err := o.deps.OpportunityRepo.TakeOverExpiredAllocationClaim(ctx, opportunity.ID, claimID, asOf, asOf.Add(portfolioAllocationClaimLease))
		if err != nil {
			return fmt.Errorf("portfolio_allocator: take over expired claim: %w", err)
		}
		if !claimed {
			continue
		}
		decisions, err := o.deps.AllocationDecisionRepo.List(ctx, repository.AllocationDecisionFilter{OpportunityID: &opportunity.ID}, 1, 0)
		if err != nil {
			return fmt.Errorf("portfolio_allocator: reconcile selected opportunity: %w", err)
		}
		if len(decisions) != 0 {
			decision := decisions[0]
			if decision.Action == domain.AllocationDecisionActionPaperOrderIntent {
				order, err := o.findOpportunityOrder(ctx, opportunity)
				if err != nil {
					return err
				}
				if order == nil {
					decision.ExecutionClaimID = claimID
					decision, err = withAllocationClaim(ctx, o.deps.OpportunityRepo, opportunity.ID, claimID, func(effectCtx context.Context) (domain.AllocationDecision, error) {
						return o.executePaperAllocatorDecision(effectCtx, decision, map[uuid.UUID]domain.Opportunity{opportunity.ID: opportunity})
					})
					applied, recordErr := o.deps.AllocationDecisionRepo.RecordPaperOrderResult(ctx, decision.ID, claimID, decision.CreatedOrderID, decision.Action, decision.Reasons)
					if recordErr != nil {
						return fmt.Errorf("portfolio_allocator: record recovered paper result: %w", recordErr)
					}
					if !applied {
						return fmt.Errorf("portfolio_allocator: record recovered paper result: compare-and-swap failed")
					}
					if err != nil {
						return fmt.Errorf("portfolio_allocator: recovered paper execution ambiguous: %w", err)
					}
				} else if err := o.reconcilePendingPaperDecision(ctx, opportunity, &decision, order, claimID); err != nil {
					return err
				}
			}
			if err := o.updateOpportunityStatus(ctx, decision, &claimID); err != nil {
				return err
			}
			continue
		}

		order, err := o.findOpportunityOrder(ctx, opportunity)
		if err != nil {
			return err
		}
		if order == nil {
			applied, err := o.deps.OpportunityRepo.TransitionClaimedStatus(ctx, opportunity.ID, claimID, domain.OpportunityStatusSelected, domain.OpportunityStatusQueued, "")
			if err != nil {
				return fmt.Errorf("portfolio_allocator: release empty selected claim: %w", err)
			}
			if !applied {
				return fmt.Errorf("portfolio_allocator: release empty selected claim: compare-and-swap failed")
			}
			continue
		}
		if _, err := withAllocationClaim(ctx, o.deps.OpportunityRepo, opportunity.ID, claimID, func(effectCtx context.Context) (struct{}, error) {
			return struct{}{}, o.reconcileNonterminalPaperOrder(effectCtx, opportunity, order, claimID)
		}); err != nil {
			return err
		}
		decision := recoveredAllocationDecision(opportunity, *order)
		decision.ExecutionClaimID = claimID
		if err := o.deps.AllocationDecisionRepo.Create(ctx, &decision); err != nil {
			return fmt.Errorf("portfolio_allocator: persist recovered decision: %w", err)
		}
		if err := o.updateOpportunityStatus(ctx, decision, &claimID); err != nil {
			return err
		}
	}
	return nil
}

func (o *JobOrchestrator) reconcilePendingPaperDecision(ctx context.Context, opportunity domain.Opportunity, decision *domain.AllocationDecision, order *domain.Order, claimID uuid.UUID) error {
	action, reason := domain.AllocationDecisionActionPaperOrderIntent, "recovery_order_pending"
	if !orderMatchesOpportunity(*order, opportunity) {
		return fmt.Errorf("portfolio_allocator: recovered order lineage mismatch")
	}
	if _, err := withAllocationClaim(ctx, o.deps.OpportunityRepo, opportunity.ID, claimID, func(effectCtx context.Context) (struct{}, error) {
		return struct{}{}, o.reconcileNonterminalPaperOrder(effectCtx, opportunity, order, claimID)
	}); err != nil {
		return err
	}
	if order.Status == domain.OrderStatusFilled {
		decision.CreatedOrderID = &order.ID
		action, reason = domain.AllocationDecisionActionExecuted, "recovered_filled_order"
	} else if order.Status == domain.OrderStatusRejected || order.Status == domain.OrderStatusCancelled {
		decision.CreatedOrderID = &order.ID
		action, reason = domain.AllocationDecisionActionExecutionRejected, "recovered_rejected_order:"+order.Status.String()
	} else {
		return fmt.Errorf("portfolio_allocator: recovered paper order remained nonterminal: %s", order.Status)
	}
	decision.Action = action
	decision.Reasons = append(decision.Reasons, reason)
	applied, err := o.deps.AllocationDecisionRepo.RecordPaperOrderResult(ctx, decision.ID, claimID, decision.CreatedOrderID, action, decision.Reasons)
	if err != nil {
		return fmt.Errorf("portfolio_allocator: reconcile pending paper intent: %w", err)
	}
	if !applied {
		return fmt.Errorf("portfolio_allocator: reconcile pending paper intent: compare-and-swap failed")
	}
	return nil
}

func (o *JobOrchestrator) reconcileNonterminalPaperOrder(ctx context.Context, opportunity domain.Opportunity, order *domain.Order, claimID uuid.UUID) error {
	if order.Status == domain.OrderStatusFilled || order.Status == domain.OrderStatusRejected || order.Status == domain.OrderStatusCancelled {
		return nil
	}
	reconciler, ok := o.deps.PortfolioPaperProcessor.(portfolio.PaperOrderReconciler)
	if !ok {
		return fmt.Errorf("portfolio_allocator: paper order reconciler is required for nonterminal recovery")
	}
	result, err := reconciler.ReconcilePaperOrder(ctx, opportunity, order, claimID)
	if err != nil {
		return fmt.Errorf("portfolio_allocator: reconcile broker paper order: %w", err)
	}
	if result.OrderID == nil || *result.OrderID != order.ID {
		return fmt.Errorf("portfolio_allocator: reconciled paper order identity mismatch")
	}
	order.Status = result.Status
	if order.Status != domain.OrderStatusFilled && order.Status != domain.OrderStatusRejected && order.Status != domain.OrderStatusCancelled {
		return fmt.Errorf("portfolio_allocator: recovered paper order remained nonterminal: %s", order.Status)
	}
	return nil
}

func (o *JobOrchestrator) findOpportunityOrder(ctx context.Context, opportunity domain.Opportunity) (*domain.Order, error) {
	if o.deps.OrderRepo == nil {
		return nil, nil
	}
	allocationRepo, ok := o.deps.OrderRepo.(repository.AllocationOrderRepository)
	if !ok {
		return nil, fmt.Errorf("portfolio_allocator: allocation order repository is required for recovery")
	}
	order, err := allocationRepo.GetByAllocationOpportunity(ctx, opportunity)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("portfolio_allocator: get scoped recovery order: %w", err)
	}
	if !orderMatchesOpportunity(*order, opportunity) {
		return nil, fmt.Errorf("portfolio_allocator: recovered order lineage mismatch")
	}
	return order, nil
}

func orderMatchesOpportunity(order domain.Order, opportunity domain.Opportunity) bool {
	return order.AccountID == opportunity.AccountID && order.Environment == opportunity.Environment && order.OriginType == opportunity.OriginType && order.OriginID == opportunity.OriginID && order.AllocationOpportunityID != nil && *order.AllocationOpportunityID == opportunity.ID && order.StrategyID != nil && *order.StrategyID == opportunity.StrategyID && opportunity.PipelineRunID != nil && order.PipelineRunID != nil && *order.PipelineRunID == *opportunity.PipelineRunID && opportunity.PipelineRunTradeDate != nil && order.PipelineRunTradeDate != nil && order.PipelineRunTradeDate.Equal(*opportunity.PipelineRunTradeDate)
}

func recoveredAllocationDecision(opportunity domain.Opportunity, order domain.Order) domain.AllocationDecision {
	opportunityID, strategyID, orderID := opportunity.ID, opportunity.StrategyID, order.ID
	action, reason := domain.AllocationDecisionActionPaperOrderIntent, "recovery_nonterminal_order:"+order.Status.String()
	if order.Status == domain.OrderStatusFilled {
		action, reason = domain.AllocationDecisionActionExecuted, "recovered_filled_order"
	} else if order.Status == domain.OrderStatusRejected || order.Status == domain.OrderStatusCancelled {
		action, reason = domain.AllocationDecisionActionExecutionRejected, "recovered_rejected_order:"+order.Status.String()
	}
	return domain.AllocationDecision{AccountID: opportunity.AccountID, Environment: opportunity.Environment, OriginType: opportunity.OriginType, OriginID: opportunity.OriginID, PipelineRunID: opportunity.PipelineRunID, PipelineRunTradeDate: opportunity.PipelineRunTradeDate, OpportunityID: &opportunityID, StrategyID: &strategyID, Mode: domain.AllocationDecisionModePaper, Action: action, Reasons: []string{reason}, CreatedOrderID: &orderID}
}

func (o *JobOrchestrator) updateOpportunityStatus(ctx context.Context, decision domain.AllocationDecision, claimID *uuid.UUID) error {
	if o.deps.OpportunityRepo == nil || decision.OpportunityID == nil {
		return nil
	}
	switch decision.Action {
	case domain.AllocationDecisionActionShadowSelected:
		if err := o.deps.OpportunityRepo.UpdateStatus(ctx, *decision.OpportunityID, domain.OpportunityStatusSelected, ""); err != nil {
			return fmt.Errorf("portfolio_allocator: mark opportunity selected: %w", err)
		}
	case domain.AllocationDecisionActionExecuted:
		if claimID != nil {
			return o.transitionClaimedOpportunity(ctx, *decision.OpportunityID, *claimID, domain.OpportunityStatusExecuted, "")
		}
		if err := o.deps.OpportunityRepo.UpdateStatus(ctx, *decision.OpportunityID, domain.OpportunityStatusExecuted, ""); err != nil {
			return fmt.Errorf("portfolio_allocator: mark opportunity executed: %w", err)
		}
	case domain.AllocationDecisionActionShadowRejected, domain.AllocationDecisionActionExecutionRejected:
		if claimID != nil {
			return o.transitionClaimedOpportunity(ctx, *decision.OpportunityID, *claimID, domain.OpportunityStatusRejected, strings.Join(decision.Reasons, "; "))
		}
		if err := o.deps.OpportunityRepo.UpdateStatus(ctx, *decision.OpportunityID, domain.OpportunityStatusRejected, strings.Join(decision.Reasons, "; ")); err != nil {
			return fmt.Errorf("portfolio_allocator: mark opportunity rejected: %w", err)
		}
	}
	return nil
}

func (o *JobOrchestrator) transitionClaimedOpportunity(ctx context.Context, id, claimID uuid.UUID, status domain.OpportunityStatus, reason string) error {
	applied, err := o.deps.OpportunityRepo.TransitionClaimedStatus(ctx, id, claimID, domain.OpportunityStatusSelected, status, reason)
	if err != nil {
		return fmt.Errorf("portfolio_allocator: complete claimed opportunity: %w", err)
	}
	if !applied {
		return fmt.Errorf("portfolio_allocator: complete claimed opportunity: claim ownership lost")
	}
	return nil
}

func (o *JobOrchestrator) preclaimPaperOpportunity(ctx context.Context, decision domain.AllocationDecision, claimID uuid.UUID, asOf time.Time) (bool, error) {
	if o.deps.OpportunityRepo == nil || decision.OpportunityID == nil {
		return false, fmt.Errorf("portfolio_allocator: preclaim opportunity selected: missing opportunity repo or opportunity id")
	}
	claimed, err := o.deps.OpportunityRepo.ClaimQueuedForAllocation(ctx, *decision.OpportunityID, claimID, asOf, asOf.Add(portfolioAllocationClaimLease))
	if err != nil {
		return false, fmt.Errorf("portfolio_allocator: preclaim opportunity selected: %w", err)
	}
	return claimed, nil
}

func withAllocationClaim[T any](ctx context.Context, repo repository.OpportunityRepository, opportunityID, claimID uuid.UUID, effect func(context.Context) (T, error)) (T, error) {
	var zero T
	renew := func() error {
		owned, err := repo.RenewAllocationClaim(ctx, opportunityID, claimID, portfolioAllocationClaimLease)
		if err != nil {
			return fmt.Errorf("portfolio_allocator: renew allocation claim: %w", err)
		}
		if !owned {
			return fmt.Errorf("portfolio_allocator: allocation claim ownership lost")
		}
		return nil
	}
	if err := renew(); err != nil {
		return zero, err
	}
	effectCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(portfolioAllocationClaimLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-effectCtx.Done():
				done <- effectCtx.Err()
				return
			case <-ticker.C:
				if err := renew(); err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	result, effectErr := effect(effectCtx)
	close(stop)
	renewErr := <-done
	if renewErr != nil {
		return zero, renewErr
	}
	if effectErr != nil {
		return result, effectErr
	}
	if err := renew(); err != nil {
		return zero, err
	}
	return result, nil
}

func countDecisionActions(decisions []domain.AllocationDecision, action domain.AllocationDecisionAction) int {
	count := 0
	for _, decision := range decisions {
		if decision.Action == action {
			count++
		}
	}
	return count
}

func (o *JobOrchestrator) portfolioAllocatorMode() portfolio.AllocatorMode {
	mode := o.deps.PortfolioAllocatorMode
	if mode == "" {
		return portfolio.AllocatorModeShadow
	}
	return mode
}

func (o *JobOrchestrator) executePaperAllocatorDecision(ctx context.Context, decision domain.AllocationDecision, opportunities map[uuid.UUID]domain.Opportunity) (domain.AllocationDecision, error) {
	decision.Mode = domain.AllocationDecisionModePaper
	decision.Action = domain.AllocationDecisionActionPaperOrderIntent

	if decision.OpportunityID == nil {
		return paperAllocatorRejected(decision, "missing_opportunity_id"), nil
	}
	opportunity, ok := opportunities[*decision.OpportunityID]
	if !ok {
		return paperAllocatorRejected(decision, "missing_opportunity"), nil
	}
	if o.deps.StrategyRepo == nil {
		return paperAllocatorRejected(decision, "missing_strategy_repo"), nil
	}
	strategy, err := o.deps.StrategyRepo.Get(ctx, opportunity.StrategyID)
	if err != nil || strategy == nil {
		return paperAllocatorRejected(decision, "missing_strategy"), nil
	}

	scope, err := opportunityExecutionScope(opportunity, *strategy)
	if err != nil {
		return paperAllocatorRejected(decision, "execution_scope_mismatch"), nil
	}
	executor := portfolio.NewPaperExecutor(portfolio.PaperExecutorDeps{Processor: o.deps.PortfolioPaperProcessor, ExecutionAccount: o.deps.ExecutionAccount})
	result, err := executor.ExecutePaperDecisionScoped(ctx, scope, opportunity, decision, *strategy)
	if err != nil {
		decision.Action = domain.AllocationDecisionActionPaperOrderIntent
		decision.CreatedOrderID = result.OrderID
		decision.Reasons = append(decision.Reasons, result.Reason)
		return decision, err
	}
	if result.Action == domain.AllocationDecisionActionExecutionRejected {
		return paperAllocatorRejected(decision, result.Reason), nil
	}
	decision.Action = result.Action
	decision.CreatedOrderID = result.OrderID
	decision.Reasons = append([]string(nil), decision.Reasons...)
	if result.Reason != "" {
		decision.Reasons = append(decision.Reasons, result.Reason)
	}
	return decision, nil
}

func opportunityByIDValue(decision domain.AllocationDecision, opportunities map[uuid.UUID]domain.Opportunity) (domain.Opportunity, bool) {
	if decision.OpportunityID == nil {
		return domain.Opportunity{}, false
	}
	opportunity, ok := opportunities[*decision.OpportunityID]
	return opportunity, ok
}

func opportunityExecutionScope(opportunity domain.Opportunity, strategy domain.Strategy) (execution.ExecutionScope, error) {
	if opportunity.PipelineRunID == nil || opportunity.PipelineRunTradeDate == nil || strategy.ExecutionStrategyVersionID == nil {
		return execution.ExecutionScope{}, errors.New("complete opportunity ownership is required")
	}
	if opportunity.OriginID != strategy.ExecutionStrategyVersionID.String() || opportunity.StrategyID != strategy.ID {
		return execution.ExecutionScope{}, errors.New("opportunity ownership does not match strategy")
	}
	return execution.NewStrategyExecutionScope(opportunity.AccountID, opportunity.Environment, *strategy.ExecutionStrategyVersionID, domain.PipelineRunRef{ID: *opportunity.PipelineRunID, TradeDate: *opportunity.PipelineRunTradeDate}, strategy.ID)
}

func paperAllocatorRejected(decision domain.AllocationDecision, reason string) domain.AllocationDecision {
	decision.Action = domain.AllocationDecisionActionExecutionRejected
	decision.Reasons = append([]string(nil), decision.Reasons...)
	if reason != "" {
		decision.Reasons = append(decision.Reasons, reason)
	}
	return decision
}

func (o *JobOrchestrator) buildPortfolioAllocatorState(ctx context.Context, mode portfolio.AllocatorMode) (portfolio.PortfolioState, []string, error) {
	state := portfolio.PortfolioState{
		MarketExposure: map[domain.MarketType]float64{},
		OpenTickers:    map[string]bool{},
	}
	warnings := make([]string, 0, 2)

	positions := make([]domain.Position, 0)
	switch {
	case o.deps.PositionRepo != nil:
		items, err := listAllOpenPositions(ctx, o.deps.PositionRepo)
		if err != nil {
			return state, warnings, fmt.Errorf("portfolio_allocator: load open positions: %w", err)
		}
		positions = items
	case mode == portfolio.AllocatorModePaper:
		return state, warnings, fmt.Errorf("portfolio_allocator: paper mode requires position repository")
	default:
		warnings = append(warnings, "positions_unavailable")
	}

	var grossExposure float64
	for _, position := range positions {
		exposure := portfolioPositionExposure(position)
		grossExposure += exposure
		state.MarketExposure[position.MarketType] += exposure
		if ticker := strings.TrimSpace(position.Ticker); ticker != "" {
			state.OpenTickers[ticker] = true
			state.OpenTickers[strings.ToUpper(ticker)] = true
			state.OpenTickers[strings.ToLower(ticker)] = true
		}
	}

	if mode == portfolio.AllocatorModePaper {
		if o.deps.PortfolioAccountBalance == nil {
			return state, warnings, fmt.Errorf("portfolio_allocator: paper mode requires account balance source")
		}
		balance, err := o.deps.PortfolioAccountBalance.GetAccountBalance(ctx)
		if err != nil {
			return state, warnings, fmt.Errorf("portfolio_allocator: load paper account balance: %w", err)
		}
		if balance.Equity <= 0 || balance.BuyingPower < 0 {
			return state, warnings, fmt.Errorf("portfolio_allocator: invalid paper account balance: equity=%g buying_power=%g", balance.Equity, balance.BuyingPower)
		}
		state.Equity = balance.Equity
		state.BuyingPower = balance.BuyingPower
	} else {
		state.Equity = 100000
		state.BuyingPower = maxFloat(100000-grossExposure, 0)
		warnings = append(warnings, "paper_account_balance_fallback")
	}
	state.GrossExposure = grossExposure
	return state, warnings, nil
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func portfolioPositionExposure(position domain.Position) float64 {
	price := position.CurrentPrice
	if price == nil || *price <= 0 {
		if position.AvgEntry > 0 {
			p := position.AvgEntry
			price = &p
		}
	}
	if price == nil || *price <= 0 || position.Quantity <= 0 {
		return 0
	}
	return position.Quantity * *price
}

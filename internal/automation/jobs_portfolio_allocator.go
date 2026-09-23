package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/regime"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
	"github.com/google/uuid"
)

// portfolioAllocatorSpec runs twice an hour from 09:15 through 20:45 ET on
// trading days: the 09:15 run captures the day-open equity snapshot before the
// 09:30 bell, regular-session runs keep option quotes inside the
// MaxQuoteAgeSeconds window, and the evening runs cover the after-hours
// selections the job previously handled exclusively.
var portfolioAllocatorSpec = scheduler.ScheduleSpec{
	Type:         scheduler.ScheduleTypeCron,
	Cron:         "15,45 9-20 * * 1-5",
	SkipWeekends: true,
	SkipHolidays: true,
}

const portfolioAllocationClaimLease = portfolio.AllocationClaimLease

// portfolioBreakerResultWindow bounds the closed-position history replayed
// into the consecutive-loss breaker on each run.
const portfolioBreakerResultWindow = 7 * 24 * time.Hour

// portfolioLadderMetricsWindow bounds the closed-position history used for
// capital-ladder win-rate metrics.
const portfolioLadderMetricsWindow = 30 * 24 * time.Hour

// portfolioBreakerSource is implemented by the canonical risk-state repository
// when it can also read and trip risk breakers. Absent it, breaker wiring is
// skipped and the allocator relies on the risk-state flags alone.
type portfolioBreakerSource interface {
	repository.RiskBreakerRepository
	RecentStrategyTradeResults(context.Context, time.Time, int) ([]pgrepo.StrategyTradeResult, error)
}

// portfolioLadderSource is implemented by the canonical risk-state repository
// when capital-ladder rows are readable and updatable.
type portfolioLadderSource interface {
	CapitalLadderSteps(context.Context, []uuid.UUID) (map[uuid.UUID]float64, error)
	UpdateMetrics(context.Context, string, float64, float64, float64) error
}

// ladderMetricsAdapter exposes only the metrics write of the ladder repository
// to risk.CapitalLadder; promotion stays on the CLI path.
type ladderMetricsAdapter struct{ source portfolioLadderSource }

func (a ladderMetricsAdapter) Upsert(context.Context, domain.CapitalLadderEntry) error {
	return errors.New("portfolio_allocator: capital ladder upsert is not available from the allocator")
}

func (a ladderMetricsAdapter) Get(context.Context, string) (*domain.CapitalLadderEntry, error) {
	return nil, repository.ErrNotFound
}

func (a ladderMetricsAdapter) List(context.Context) ([]domain.CapitalLadderEntry, error) {
	return nil, errors.New("portfolio_allocator: capital ladder list is not available from the allocator")
}

func (a ladderMetricsAdapter) UpdateMetrics(ctx context.Context, strategyID string, fillRate, winRate, drawdownPct float64) error {
	return a.source.UpdateMetrics(ctx, strategyID, fillRate, winRate, drawdownPct)
}

func (a ladderMetricsAdapter) AdvanceStep(context.Context, string, float64, time.Time) error {
	return errors.New("portfolio_allocator: capital ladder promotion stays on the CLI path")
}

// guardedBreaker trips a scope only when it is not already open and was not
// reset by an operator inside the replay window, so replaying closed-position
// history each run never reopens a breaker someone deliberately reset.
type guardedBreaker struct {
	repo       repository.RiskBreakerRepository
	resetAfter time.Time
	now        time.Time
	tripped    map[string]string
}

func (b *guardedBreaker) Allow(ctx context.Context, scope string) error {
	state, err := b.repo.Get(ctx, scope)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.ResetAt == nil {
		return fmt.Errorf("%w: %s (%s)", risk.ErrBreakerTripped, scope, state.Reason)
	}
	return nil
}

func (b *guardedBreaker) Trip(ctx context.Context, scope, reason string) error {
	state, err := b.repo.Get(ctx, scope)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if state != nil {
		if state.ResetAt == nil {
			b.tripped[scope] = state.Reason
			return nil
		}
		if state.ResetAt.After(b.resetAfter) {
			return nil
		}
	}
	if err := b.repo.Trip(ctx, scope, reason, b.now); err != nil {
		return err
	}
	b.tripped[scope] = reason
	return nil
}

func (b *guardedBreaker) Reset(context.Context, string) error {
	return errors.New("portfolio_allocator: breaker reset is an operator action")
}

// PortfolioAccountBalanceSource supplies the restored paper account state used
// to size paper allocator decisions.
type PortfolioAccountBalanceSource interface {
	GetAccountBalance(context.Context) (execution.Balance, error)
}

type PortfolioAccountSnapshotSource interface {
	CaptureAccountSnapshot(context.Context) (portfolio.AccountSnapshot, error)
}

type PortfolioRiskStateSource interface {
	LoadPortfolioRiskState(context.Context, uuid.UUID, time.Time) (portfolio.RuntimeRiskState, error)
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

	state, warnings, err := o.buildPortfolioAllocatorState(ctx, mode, opportunities)
	if err != nil {
		return err
	}

	validOpportunities, sourceRejected, err := o.validatePortfolioOpportunitySources(ctx, opportunities, mode)
	if err != nil {
		return err
	}

	allocCfg := portfolio.DefaultAllocatorConfig()
	allocCfg.Mode = mode
	breakerWarnings, breakerStats, err := o.evaluatePortfolioBreakers(ctx, &state, allocCfg, asOf)
	if err != nil {
		return err
	}
	warnings = append(warnings, breakerWarnings...)
	warnings = append(warnings, o.applyPortfolioRegimeControls(ctx, &state, breakerStats)...)
	result := portfolio.AllocateShadow(validOpportunities, state, allocCfg)
	if !mode.OwnsExecution() && result.Summary.Selected > 0 {
		o.logger.Warn(fmt.Sprintf("portfolio_allocator: %s mode: %d opportunities selected, none submitted", mode, result.Summary.Selected),
			slog.String("mode", string(mode)), slog.Int("selected", result.Summary.Selected))
	}
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
			decision.RiskPolicyVersion = opportunity.RiskPolicyVersion
		}
		decision.AccountSnapshotID = state.AccountSnapshotID
		decision.RiskStateSHA256 = state.RiskStateSHA256
		decision.RiskStateBytes = append([]byte(nil), state.RiskStateBytes...)

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
				return o.executePaperAllocatorDecision(effectCtx, decision, opportunityByID, state.Equity)
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

	if mode.OwnsExecution() {
		if err := o.recordPortfolioLadderMetrics(ctx, result.Decisions, state, asOf); err != nil {
			warnings = append(warnings, "capital_ladder_metrics_failed")
			o.logger.Warn("portfolio_allocator: record capital ladder metrics", slog.String("error", err.Error()))
		}
	}

	summary := map[string]int{
		"positions_missing_mark": state.PositionsMissingMark,
		"expired":                int(expired),
		"queued_loaded":          len(opportunities),
		"evaluated":              result.Summary.Evaluated,
		"eligible":               result.Summary.Eligible,
		"selected":               result.Summary.Selected,
		"rejected":               result.Summary.Rejected,
		"executed":               countDecisionActions(result.Decisions, domain.AllocationDecisionActionExecuted),
		"execution_rejected":     countDecisionActions(result.Decisions, domain.AllocationDecisionActionExecutionRejected),
		"source_rejected":        len(sourceRejected),
		"persisted_decisions":    len(result.Decisions),
		"warnings":               len(warnings),
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

	prefetched := o.prefetchOpportunityRuns(ctx, opportunities)
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
		}
		run, err := o.lookupOpportunityRun(ctx, prefetched, domain.PipelineRunRef{ID: *opportunity.PipelineRunID, TradeDate: *opportunity.PipelineRunTradeDate})
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
		case run.ExecutionVersionID != opportunity.ExecutionVersionID || run.EvaluationScopeID != opportunity.EvaluationScopeID ||
			run.ManifestID != opportunity.ManifestID || run.QualityResultID != opportunity.QualityResultID ||
			run.DeploymentID != opportunity.DeploymentID || run.PromotionDecisionID != opportunity.PromotionDecisionID ||
			run.CapitalBindingID != opportunity.CapitalBindingID || run.RiskPolicyVersion != opportunity.RiskPolicyVersion:
			reason = "source_promotion_lineage_mismatch"
		case run.StrategyID != opportunity.StrategyID:
			reason = "source_strategy_mismatch"
		case run.Signal != opportunity.Signal || (run.Signal != domain.PipelineSignalBuy && run.Signal != domain.PipelineSignalSell):
			reason = "source_signal_mismatch"
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
						// Recovery runs before the canonical snapshot is captured; zero
						// equity lets the processor fall back to its configured balance.
						return o.executePaperAllocatorDecision(effectCtx, decision, map[uuid.UUID]domain.Opportunity{opportunity.ID: opportunity}, 0)
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
	var action domain.AllocationDecisionAction
	var reason string
	if !orderMatchesOpportunity(*order, opportunity) {
		return fmt.Errorf("portfolio_allocator: recovered order lineage mismatch")
	}
	if _, err := withAllocationClaim(ctx, o.deps.OpportunityRepo, opportunity.ID, claimID, func(effectCtx context.Context) (struct{}, error) {
		return struct{}{}, o.reconcileNonterminalPaperOrder(effectCtx, opportunity, order, claimID)
	}); err != nil {
		return err
	}
	switch order.Status {
	case domain.OrderStatusFilled:
		decision.CreatedOrderID = &order.ID
		action, reason = domain.AllocationDecisionActionExecuted, "recovered_filled_order"
	case domain.OrderStatusRejected, domain.OrderStatusCancelled:
		decision.CreatedOrderID = &order.ID
		action, reason = domain.AllocationDecisionActionExecutionRejected, "recovered_rejected_order:"+order.Status.String()
	default:
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
	if order.Status == domain.OrderStatusRejected || order.Status == domain.OrderStatusCancelled {
		return nil
	}
	processor := any(o.deps.PortfolioPaperProcessor)
	if opportunity.MarketType == domain.MarketTypeOptions {
		processor = o.deps.PortfolioOptionsProcessor
	}
	reconciler, ok := processor.(portfolio.PaperOrderReconciler)
	if !ok {
		return fmt.Errorf("portfolio_allocator: paper order reconciler is required for fill-safe recovery")
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
	switch order.Status {
	case domain.OrderStatusFilled:
		action, reason = domain.AllocationDecisionActionExecuted, "recovered_filled_order"
	case domain.OrderStatusRejected, domain.OrderStatusCancelled:
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

func (o *JobOrchestrator) executePaperAllocatorDecision(ctx context.Context, decision domain.AllocationDecision, opportunities map[uuid.UUID]domain.Opportunity, accountEquity float64) (domain.AllocationDecision, error) {
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
	if opportunity.MarketType == domain.MarketTypeOptions {
		if o.deps.PortfolioOptionsProcessor == nil {
			return paperAllocatorRejected(decision, "missing_options_paper_processor"), nil
		}
		result, optionErr := o.deps.PortfolioOptionsProcessor.ProcessPaperOptionsOrder(ctx, scope, opportunity, decision)
		if optionErr != nil {
			decision.Action = domain.AllocationDecisionActionPaperOrderIntent
			decision.CreatedOrderID = result.OrderID
			decision.Reasons = append(decision.Reasons, result.Reason)
			return decision, optionErr
		}
		if result.Skipped || result.OrderID == nil || result.Status == domain.OrderStatusRejected || result.Status == domain.OrderStatusCancelled {
			return paperAllocatorRejected(decision, result.Reason), nil
		}
		decision.CreatedOrderID = result.OrderID
		if result.Status == domain.OrderStatusFilled {
			decision.Action = domain.AllocationDecisionActionExecuted
		}
		if result.Reason != "" {
			decision.Reasons = append(decision.Reasons, result.Reason)
		}
		return decision, nil
	}
	executor := portfolio.NewPaperExecutor(portfolio.PaperExecutorDeps{Processor: o.deps.PortfolioPaperProcessor, ExecutionAccount: o.deps.ExecutionAccount, AccountEquityUSD: accountEquity})
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

func (o *JobOrchestrator) buildPortfolioAllocatorState(ctx context.Context, mode portfolio.AllocatorMode, opportunities []domain.Opportunity) (portfolio.PortfolioState, []string, error) {
	state := portfolio.PortfolioState{
		MarketExposure:      map[domain.MarketType]float64{},
		OpenTickers:         map[string]bool{},
		StrategyBreakerOpen: map[uuid.UUID]bool{},
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
	countedOptionGroups := make(map[uuid.UUID]struct{})
	for _, position := range positions {
		if position.AssetClass == domain.AssetClassOption {
			if position.LegGroupID == nil || *position.LegGroupID == uuid.Nil {
				state.OpenPositionCount++
			} else if _, counted := countedOptionGroups[*position.LegGroupID]; !counted {
				countedOptionGroups[*position.LegGroupID] = struct{}{}
				state.OpenPositionCount++
			}
			multiplier := position.ContractMultiplier
			if multiplier <= 0 {
				multiplier = 100
			}
			sign := 1.0
			if position.Side == domain.PositionSideShort {
				sign = -1
			}
			if position.Delta != nil {
				state.Delta += sign * *position.Delta * position.Quantity * multiplier
			}
			if position.Gamma != nil {
				state.Gamma += sign * *position.Gamma * position.Quantity * multiplier
			}
			if position.Theta != nil {
				state.Theta += sign * *position.Theta * position.Quantity * multiplier
			}
			if position.Vega != nil {
				state.Vega += sign * *position.Vega * position.Quantity * multiplier
			}
			continue
		}
		state.OpenPositionCount++
		exposure, marked := portfolioPositionExposure(position)
		if !marked {
			state.PositionsMissingMark++
		}
		grossExposure += exposure
		state.MarketExposure[position.MarketType] += exposure
		if ticker := strings.TrimSpace(position.Ticker); ticker != "" {
			state.OpenTickers[ticker] = true
			state.OpenTickers[strings.ToUpper(ticker)] = true
			state.OpenTickers[strings.ToLower(ticker)] = true
		}
	}

	if o.deps.PortfolioAccountSnapshot == nil {
		return state, warnings, fmt.Errorf("portfolio_allocator: %s mode requires canonical account snapshot source", mode)
	}
	snapshot, err := o.deps.PortfolioAccountSnapshot.CaptureAccountSnapshot(ctx)
	if err != nil {
		return state, warnings, fmt.Errorf("portfolio_allocator: capture canonical account balance: %w", err)
	}
	if snapshot.ID == uuid.Nil || snapshot.FallbackUsed || snapshot.Equity <= 0 || snapshot.BuyingPower < 0 || snapshot.OptionsBuyingPower < 0 {
		return state, warnings, fmt.Errorf("portfolio_allocator: invalid canonical account snapshot")
	}
	state.AccountSnapshotID = snapshot.ID
	state.Equity = snapshot.Equity
	state.BuyingPower = snapshot.BuyingPower
	state.OptionsBuyingPower = snapshot.OptionsBuyingPower
	state.InternalAccount = snapshot.InternalAccount
	if o.deps.PortfolioRiskState == nil {
		return state, warnings, fmt.Errorf("portfolio_allocator: %s mode requires canonical risk-state source", mode)
	}
	riskState, err := o.deps.PortfolioRiskState.LoadPortfolioRiskState(ctx, snapshot.ID, snapshot.ObservedAt)
	if err != nil {
		return state, warnings, fmt.Errorf("portfolio_allocator: load canonical risk state: %w", err)
	}
	state.DailyLossPct = riskState.DailyLossPct
	state.DrawdownPct = riskState.DrawdownPct
	state.NewOrdersToday = riskState.NewOrdersToday
	state.CircuitBreakerOpen = riskState.CircuitBreakerOpen
	state.ReconciliationID = riskState.ReconciliationID
	state.UnderlyingRisk = riskState.UnderlyingRisk
	for _, scope := range riskState.OpenBreakerScopes {
		if strategyID, ok := strategyScopeID(scope); ok {
			state.StrategyBreakerOpen[strategyID] = true
		}
	}
	for _, reservedRisk := range riskState.UnderlyingRisk {
		grossExposure += reservedRisk
		state.MarketExposure[domain.MarketTypeOptions] += reservedRisk
	}
	state.GrossExposure = grossExposure
	if state.PositionsMissingMark > 0 {
		warnings = append(warnings, fmt.Sprintf("%s=%d", portfolio.WarningPositionsMissingMark, state.PositionsMissingMark))
	}
	if ladder, ok := o.deps.PortfolioRiskState.(portfolioLadderSource); ok && len(opportunities) > 0 {
		steps, err := ladder.CapitalLadderSteps(ctx, opportunityStrategyIDs(opportunities))
		if err != nil {
			return state, warnings, fmt.Errorf("portfolio_allocator: load capital ladder steps: %w", err)
		}
		if len(steps) > 0 {
			state.StrategyStepPct = steps
		}
	}
	if err := portfolio.BindRiskStateEvidence(&state, snapshot.ObservedAt); err != nil {
		return state, warnings, fmt.Errorf("portfolio_allocator: bind risk-state evidence: %w", err)
	}
	return state, warnings, nil
}

// evaluatePortfolioBreakers runs the drawdown and consecutive-loss controls
// against the ledger-derived risk state and recent closed positions, tripping
// the durable breaker scopes when the reviewed thresholds are exceeded. The
// updated open scopes are reflected in the state used for this run.
func (o *JobOrchestrator) evaluatePortfolioBreakers(ctx context.Context, state *portfolio.PortfolioState, cfg portfolio.AllocatorConfig, now time.Time) ([]string, portfolioBreakerStats, error) {
	var stats portfolioBreakerStats
	source, ok := o.deps.PortfolioRiskState.(portfolioBreakerSource)
	if !ok || state == nil || state.Equity <= 0 {
		return nil, stats, nil
	}
	warnings := make([]string, 0, 2)
	guard := &guardedBreaker{repo: source, resetAfter: now.Add(-portfolioBreakerResultWindow), now: now.UTC(), tripped: map[string]string{}}
	drawdown := risk.NewDrawdownBreaker(risk.DrawdownBreakerConfig{MaxDailyDD: cfg.MaxDailyLossPct * state.Equity}, source)
	if cfg.MaxDailyLossPct > 0 && state.DailyLossPct >= cfg.MaxDailyLossPct {
		if err := guard.Trip(ctx, domain.RiskBreakerScopeGlobal, fmt.Sprintf("daily_loss_pct=%.4f exceeded max_daily_loss_pct=%.4f", state.DailyLossPct, cfg.MaxDailyLossPct)); err != nil {
			return nil, stats, fmt.Errorf("portfolio_allocator: trip daily-loss breaker: %w", err)
		}
	} else if err := drawdownCheck(ctx, drawdown, guard, -state.DailyLossPct*state.Equity); err != nil {
		return nil, stats, fmt.Errorf("portfolio_allocator: check drawdown breaker: %w", err)
	}
	if cfg.MaxDrawdownPct > 0 && state.DrawdownPct >= cfg.MaxDrawdownPct {
		if err := guard.Trip(ctx, domain.RiskBreakerScopeGlobal, fmt.Sprintf("drawdown_pct=%.4f exceeded max_drawdown_pct=%.4f over %d days", state.DrawdownPct, cfg.MaxDrawdownPct, cfg.DrawdownWindowDays)); err != nil {
			return nil, stats, fmt.Errorf("portfolio_allocator: trip drawdown breaker: %w", err)
		}
	}
	results, err := source.RecentStrategyTradeResults(ctx, now.Add(-portfolioBreakerResultWindow), 2000)
	if err != nil {
		return nil, stats, fmt.Errorf("portfolio_allocator: load closed positions for breakers: %w", err)
	}
	losses := risk.NewConsecutiveLossBreaker(risk.ConsecutiveLossConfig{Threshold: risk.DefaultConsecutiveLossThreshold}, guard)
	streaks := map[uuid.UUID]int{}
	for _, result := range results {
		if result.StrategyID == uuid.Nil {
			continue
		}
		stats.Sampled++
		if result.RealizedPnL > 0 {
			stats.Wins++
			streaks[result.StrategyID] = 0
		} else {
			streaks[result.StrategyID]++
			if streaks[result.StrategyID] > stats.MaxConsecutiveLosses {
				stats.MaxConsecutiveLosses = streaks[result.StrategyID]
			}
		}
		if err := losses.RecordResult(ctx, result.StrategyID.String(), result.RealizedPnL); err != nil {
			return nil, stats, fmt.Errorf("portfolio_allocator: record strategy result: %w", err)
		}
	}
	for scope, reason := range guard.tripped {
		if scope == domain.RiskBreakerScopeGlobal {
			state.CircuitBreakerOpen = true
		} else if strategyID, ok := strategyScopeID(scope); ok {
			if state.StrategyBreakerOpen == nil {
				state.StrategyBreakerOpen = map[uuid.UUID]bool{}
			}
			state.StrategyBreakerOpen[strategyID] = true
		}
		warnings = append(warnings, fmt.Sprintf("breaker_open:%s:%s", scope, reason))
	}
	sort.Strings(warnings)
	return warnings, stats, nil
}

// portfolioBreakerStats summarises the recent closed results the breaker pass
// observed so the in-memory risk engine and regime rules see the same data.
type portfolioBreakerStats struct {
	Sampled              int
	Wins                 int
	MaxConsecutiveLosses int
}

// RollingWinRate is the share of sampled closed results with positive
// realized P&L; NaN when nothing was sampled.
func (s portfolioBreakerStats) RollingWinRate() float64 {
	if s.Sampled == 0 {
		return math.NaN()
	}
	return float64(s.Wins) / float64(s.Sampled)
}

// applyPortfolioRegimeControls feeds the ledger-derived state into the
// in-memory risk engine and evaluates the regime rules. A paused regime
// closes allocation for this run through the circuit-breaker flag so the
// allocator rejects every candidate with an explicit reason.
func (o *JobOrchestrator) applyPortfolioRegimeControls(ctx context.Context, state *portfolio.PortfolioState, stats portfolioBreakerStats) []string {
	if state == nil {
		return nil
	}
	warnings := make([]string, 0, 2)
	if o.deps.RiskEngine != nil {
		if err := o.deps.RiskEngine.UpdateMetrics(ctx, -state.DailyLossPct*state.Equity, state.DrawdownPct, stats.MaxConsecutiveLosses); err != nil {
			o.logger.Warn("portfolio_allocator: risk engine metrics update failed", slog.Any("error", err))
			warnings = append(warnings, "risk_engine_metrics_update_failed")
		}
	}
	decision := regime.Evaluate(regime.Snapshot{
		ConsecutiveLosses: stats.MaxConsecutiveLosses,
		RollingWinRate:    stats.RollingWinRate(),
	}, o.deps.RegimeRules)
	if decision.Paused {
		state.CircuitBreakerOpen = true
		reason := "regime_pause:" + strings.Join(decision.Reasons, ",")
		o.logger.Warn("portfolio_allocator: regime rules paused new allocations", slog.String("reasons", strings.Join(decision.Reasons, ",")))
		warnings = append(warnings, reason)
	}
	return warnings
}

// drawdownCheck routes DrawdownBreaker.CheckDrawdown through the guard so an
// already-open or recently reset global scope is not re-tripped.
func drawdownCheck(ctx context.Context, breaker *risk.DrawdownBreaker, guard *guardedBreaker, realizedPnL float64) error {
	if err := guard.Allow(ctx, domain.RiskBreakerScopeGlobal); err != nil {
		if errors.Is(err, risk.ErrBreakerTripped) {
			guard.tripped[domain.RiskBreakerScopeGlobal] = "already_open"
			return nil
		}
		return err
	}
	tripped := ""
	breaker.OnTrip = func(_, reason string) { tripped = reason }
	if err := breaker.CheckDrawdown(ctx, realizedPnL); err != nil {
		return err
	}
	if tripped != "" {
		guard.tripped[domain.RiskBreakerScopeGlobal] = tripped
	}
	return nil
}

// recordPortfolioLadderMetrics feeds paper execution outcomes into the
// capital ladder for each laddered strategy the run touched: fill rate from
// this run's paper intents, win rate from recent closed positions, and the
// ledger drawdown.
func (o *JobOrchestrator) recordPortfolioLadderMetrics(ctx context.Context, decisions []domain.AllocationDecision, state portfolio.PortfolioState, now time.Time) error {
	ladderSource, ok := o.deps.PortfolioRiskState.(portfolioLadderSource)
	if !ok || len(state.StrategyStepPct) == 0 {
		return nil
	}
	type tally struct{ attempted, hits int }
	fills := make(map[uuid.UUID]*tally)
	for _, decision := range decisions {
		if decision.StrategyID == nil || decision.Mode != domain.AllocationDecisionModePaper {
			continue
		}
		if _, laddered := state.StrategyStepPct[*decision.StrategyID]; !laddered {
			continue
		}
		switch decision.Action {
		case domain.AllocationDecisionActionExecuted, domain.AllocationDecisionActionExecutionRejected, domain.AllocationDecisionActionPaperOrderIntent:
			entry := fills[*decision.StrategyID]
			if entry == nil {
				entry = &tally{}
				fills[*decision.StrategyID] = entry
			}
			entry.attempted++
			if decision.Action == domain.AllocationDecisionActionExecuted {
				entry.hits++
			}
		}
	}
	if len(fills) == 0 {
		return nil
	}
	wins := make(map[uuid.UUID]*tally)
	if breakerSource, ok := o.deps.PortfolioRiskState.(portfolioBreakerSource); ok {
		results, err := breakerSource.RecentStrategyTradeResults(ctx, now.Add(-portfolioLadderMetricsWindow), 5000)
		if err != nil {
			return err
		}
		for _, result := range results {
			entry := wins[result.StrategyID]
			if entry == nil {
				entry = &tally{}
				wins[result.StrategyID] = entry
			}
			entry.attempted++
			if result.RealizedPnL > 0 {
				entry.hits++
			}
		}
	}
	ladder := risk.NewCapitalLadder(risk.CapitalLadderConfig{}, ladderMetricsAdapter{source: ladderSource})
	strategyIDs := make([]uuid.UUID, 0, len(fills))
	for strategyID := range fills {
		strategyIDs = append(strategyIDs, strategyID)
	}
	sort.Slice(strategyIDs, func(i, j int) bool { return strategyIDs[i].String() < strategyIDs[j].String() })
	for _, strategyID := range strategyIDs {
		fill := fills[strategyID]
		fillRate := float64(fill.hits) / float64(fill.attempted)
		winRate := 0.0
		if win := wins[strategyID]; win != nil && win.attempted > 0 {
			winRate = float64(win.hits) / float64(win.attempted)
		}
		if err := ladder.RecordMetrics(ctx, strategyID.String(), fillRate, winRate, state.DrawdownPct); err != nil {
			return err
		}
	}
	return nil
}

func strategyScopeID(scope string) (uuid.UUID, bool) {
	const prefix = "strategy:"
	if !strings.HasPrefix(scope, prefix) {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimPrefix(scope, prefix))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

func opportunityStrategyIDs(opportunities []domain.Opportunity) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(opportunities))
	ids := make([]uuid.UUID, 0, len(opportunities))
	for _, opportunity := range opportunities {
		if opportunity.StrategyID == uuid.Nil {
			continue
		}
		if _, ok := seen[opportunity.StrategyID]; ok {
			continue
		}
		seen[opportunity.StrategyID] = struct{}{}
		ids = append(ids, opportunity.StrategyID)
	}
	return ids
}

// portfolioPositionExposure values an open position at its current mark, or
// at entry price when no mark is available. The second return reports whether
// a mark was present so callers can count positions_missing_mark.
func portfolioPositionExposure(position domain.Position) (float64, bool) {
	price := position.CurrentPrice
	marked := price != nil && *price > 0
	if !marked && position.AvgEntry > 0 {
		p := position.AvgEntry
		price = &p
	}
	if price == nil || *price <= 0 || position.Quantity <= 0 {
		return 0, marked
	}
	return position.Quantity * *price, marked
}

// pipelineRunBatchLister is implemented by repositories that can load the
// source runs for a whole allocation pass in one query.
type pipelineRunBatchLister interface {
	ListByRefs(ctx context.Context, refs []domain.PipelineRunRef) (map[uuid.UUID]domain.PipelineRun, error)
}

// prefetchOpportunityRuns loads every well-formed source run in one round
// trip when the repository supports it; nil means "use Get per opportunity".
func (o *JobOrchestrator) prefetchOpportunityRuns(ctx context.Context, opportunities []domain.Opportunity) map[uuid.UUID]domain.PipelineRun {
	lister, ok := o.deps.RunRepo.(pipelineRunBatchLister)
	if !ok {
		return nil
	}
	refs := make([]domain.PipelineRunRef, 0, len(opportunities))
	for _, opportunity := range opportunities {
		if opportunity.PipelineRunID == nil || *opportunity.PipelineRunID == uuid.Nil || opportunity.PipelineRunTradeDate == nil {
			continue
		}
		refs = append(refs, domain.PipelineRunRef{ID: *opportunity.PipelineRunID, TradeDate: *opportunity.PipelineRunTradeDate})
	}
	if len(refs) == 0 {
		return nil
	}
	runs, err := lister.ListByRefs(ctx, refs)
	if err != nil {
		o.logger.Warn("portfolio_allocator: batch source run load failed; falling back to per-opportunity reads", slog.Any("error", err))
		return nil
	}
	return runs
}

// lookupOpportunityRun serves a source run from the prefetched batch, falling
// back to the repository when the batch is absent or the run was not in it.
func (o *JobOrchestrator) lookupOpportunityRun(ctx context.Context, prefetched map[uuid.UUID]domain.PipelineRun, ref domain.PipelineRunRef) (*domain.PipelineRun, error) {
	if prefetched != nil {
		if run, ok := prefetched[ref.ID]; ok {
			return &run, nil
		}
		return nil, repository.ErrNotFound
	}
	return o.deps.RunRepo.Get(ctx, ref)
}

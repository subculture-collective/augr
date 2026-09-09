package portfolio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/paper"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
	"github.com/google/uuid"
)

var ErrPaperProcessorUnavailable = errors.New("portfolio: paper processor unavailable")

// PaperOrderManagerProcessor adapts the existing order manager to allocator
// paper execution. It always uses the shared local in-memory PaperBroker and
// never enables live trading.
type PaperOrderManagerProcessor struct {
	deps PaperOrderManagerProcessorDeps
}

type PaperOrderManagerProcessorDeps struct {
	// PrepareSignal supplies canonical evidence for actionable stock requests.
	// Native prediction and HOLD paths retain their own execution contracts.
	// Missing stock evidence fails closed once configured.
	PrepareSignal    func(context.Context, PaperOrderRequest) (execution.SignalOrderPreparation, error)
	EconomicWriter   execution.AcceptedOrderFillWriter
	RiskEngine       risk.RiskEngine
	PositionRepo     repository.PositionRepository
	OrderRepo        repository.OrderRepository
	TradeRepo        repository.TradeRepository
	AuditLogRepo     repository.AuditLogRepository
	AgentEventRepo   repository.AgentEventRepository
	DecisionRecorder execution.DecisionRecorder
	OpportunityRepo  repository.OpportunityRepository
	Metrics          execution.OrderMetricsRecorder
	Logger           *slog.Logger
	InitialBalance   float64
	FractionPct      float64
	PaperBroker      *paper.PaperBroker
}

type allocationOrderRepo struct {
	repository.OrderRepository
	opportunityID uuid.UUID
	claimID       uuid.UUID
}

func (r allocationOrderRepo) Create(ctx context.Context, order *domain.Order) error {
	if err := r.DecorateOrder(order); err != nil {
		return err
	}
	return r.OrderRepository.Create(ctx, order)
}

func (r allocationOrderRepo) DecorateOrder(order *domain.Order) error {
	if order == nil || r.opportunityID == uuid.Nil || r.claimID == uuid.Nil {
		return errors.New("portfolio: allocation opportunity and claim are required")
	}
	order.AllocationOpportunityID = &r.opportunityID
	order.AllocationClaimID = &r.claimID
	return nil
}

func (r allocationOrderRepo) WithExecutionAccountLock(ctx context.Context, accountID uuid.UUID, fn func() error) error {
	locker, ok := r.OrderRepository.(repository.ExecutionAccountLocker)
	if !ok {
		return errors.New("portfolio: allocation order repository lacks execution account locker")
	}
	return locker.WithExecutionAccountLock(ctx, accountID, fn)
}

func (r allocationOrderRepo) CreateOptionOrders(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, originType, originID string, orders []*domain.Order) error {
	repo, ok := r.OrderRepository.(repository.AtomicOptionOrderRepository)
	if !ok {
		return errors.New("portfolio: atomic option order repository is required")
	}
	for _, order := range orders {
		if err := r.DecorateOrder(order); err != nil {
			return err
		}
	}
	return repo.CreateOptionOrders(ctx, accountID, environment, originType, originID, orders)
}

func (r allocationOrderRepo) CreateOptionCloseOrdersAndReserve(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, originType, originID string, positionIDs []uuid.UUID, orders []*domain.Order) error {
	repo, ok := r.OrderRepository.(repository.AtomicOptionCloseRepository)
	if !ok {
		return errors.New("portfolio: atomic option close repository is required")
	}
	for _, order := range orders {
		if err := r.DecorateOrder(order); err != nil {
			return err
		}
	}
	return repo.CreateOptionCloseOrdersAndReserve(ctx, accountID, environment, originType, originID, positionIDs, orders)
}

func (r allocationOrderRepo) ReleaseOptionClosePositions(ctx context.Context, accountID uuid.UUID, positionIDs, orderIDs []uuid.UUID) error {
	repo, ok := r.OrderRepository.(repository.AtomicOptionCloseRepository)
	if !ok {
		return errors.New("portfolio: atomic option close repository is required")
	}
	return repo.ReleaseOptionClosePositions(ctx, accountID, positionIDs, orderIDs)
}

func (r allocationOrderRepo) ReconcileOptionCloseReservations(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment) error {
	repo, ok := r.OrderRepository.(repository.AtomicOptionCloseRepository)
	if !ok {
		return errors.New("portfolio: atomic option close repository is required")
	}
	return repo.ReconcileOptionCloseReservations(ctx, accountID, environment)
}

func (r allocationOrderRepo) RejectOptionOrdersAndRelease(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, orderIDs []uuid.UUID) error {
	repo, ok := r.OrderRepository.(repository.OptionDefinitiveRejectionRepository)
	if !ok {
		return errors.New("portfolio: option rejection repository is required")
	}
	return repo.RejectOptionOrdersAndRelease(ctx, accountID, environment, orderIDs)
}

func (r allocationOrderRepo) GetOptionClosePositionByOrder(ctx context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, orderID uuid.UUID) (*domain.Position, error) {
	repo, ok := r.OrderRepository.(repository.OptionCloseReservationLookup)
	if !ok {
		return nil, errors.New("portfolio: option close reservation lookup is required")
	}
	return repo.GetOptionClosePositionByOrder(ctx, accountID, environment, orderID)
}

var _ repository.ExecutionAccountLocker = allocationOrderRepo{}

func NewPaperOrderManagerProcessor(deps PaperOrderManagerProcessorDeps) *PaperOrderManagerProcessor {
	return &PaperOrderManagerProcessor{deps: deps}
}

func (p *PaperOrderManagerProcessor) ProcessPaperOrder(ctx context.Context, request PaperOrderRequest) (PaperOrderResult, error) {
	if p == nil {
		return PaperOrderResult{}, ErrPaperProcessorUnavailable
	}
	initialBalance := p.deps.InitialBalance
	if initialBalance <= 0 {
		initialBalance = 100_000
	}
	if request.NotionalUSD <= 0 {
		return PaperOrderResult{Skipped: true, Reason: "missing_notional_usd"}, nil
	}
	fractionPct := request.NotionalUSD / initialBalance
	if fractionPct <= 0 {
		return PaperOrderResult{Skipped: true, Reason: "missing_fraction_pct"}, nil
	}
	broker := p.deps.PaperBroker
	if broker == nil {
		broker = paper.NewPaperBroker(initialBalance, 0, 0)
	}
	orderRepo := p.deps.OrderRepo
	if orderRepo != nil && request.OpportunityID != uuid.Nil && request.ClaimID != uuid.Nil {
		orderRepo = allocationOrderRepo{OrderRepository: orderRepo, opportunityID: request.OpportunityID, claimID: request.ClaimID}
	}
	manager := execution.NewOrderManager(
		broker,
		"paper",
		p.deps.RiskEngine,
		p.deps.PositionRepo,
		orderRepo,
		p.deps.TradeRepo,
		p.deps.AuditLogRepo,
		p.deps.AgentEventRepo,
		execution.SizingConfig{Method: execution.PositionSizingMethodFixedFractional, FractionPct: fractionPct},
		p.deps.Logger,
	).WithAcceptedOrderFillWriter(p.deps.EconomicWriter).WithLiveTrading(false)
	if request.OpportunityID != uuid.Nil && request.ClaimID != uuid.Nil {
		if p.deps.OpportunityRepo == nil {
			return PaperOrderResult{}, errors.New("portfolio: allocation claim repository is required")
		}
		manager = manager.WithEffectFence(p.claimFence(request.OpportunityID, request.ClaimID))
	}
	if p.deps.Metrics != nil {
		manager = manager.WithMetrics(p.deps.Metrics)
	}
	if p.deps.DecisionRecorder != nil {
		manager = manager.WithDecisionRecorder(p.deps.DecisionRecorder)
	}
	var processErr error
	needsPreparation := p.deps.PrepareSignal != nil && request.Plan.MarketType.Normalize() == domain.MarketTypeStock && request.Signal.Signal != domain.PipelineSignalHold
	if needsPreparation {
		preparation, err := p.deps.PrepareSignal(ctx, request)
		if err != nil {
			return PaperOrderResult{}, fmt.Errorf("portfolio: prepare canonical paper signal: %w", err)
		}
		if preparation == nil {
			return PaperOrderResult{}, errors.New("portfolio: canonical paper signal preparation is missing")
		}
		processErr = manager.ProcessSignalWithPreparation(ctx, request.Scope, request.Signal, request.Plan, preparation)
	} else {
		processErr = manager.ProcessSignal(ctx, request.Scope, request.Signal, request.Plan)
	}
	if p.deps.OrderRepo == nil {
		if processErr != nil && needsPreparation {
			return PaperOrderResult{}, processErr
		}
		return PaperOrderResult{Skipped: true, Reason: "missing_order_repo"}, nil
	}
	if allocationRepo, ok := p.deps.OrderRepo.(repository.AllocationOrderRepository); ok && request.OpportunityID != uuid.Nil {
		order, err := allocationRepo.GetByAllocationOpportunity(ctx, request.Opportunity)
		if err == nil {
			return PaperOrderResult{OrderID: &order.ID, Status: order.Status}, processErr
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return PaperOrderResult{}, errors.Join(processErr, err)
		}
		if processErr != nil {
			return PaperOrderResult{}, processErr
		}
	}
	if processErr != nil {
		return PaperOrderResult{}, processErr
	}
	return PaperOrderResult{Skipped: true, Reason: "paper_order_not_created"}, nil
}

func (p *PaperOrderManagerProcessor) ReconcilePaperOrder(ctx context.Context, opportunity domain.Opportunity, order *domain.Order, claimID uuid.UUID) (PaperOrderResult, error) {
	if p == nil || order == nil || p.deps.PaperBroker == nil || p.deps.OrderRepo == nil {
		return PaperOrderResult{}, ErrPaperProcessorUnavailable
	}
	if opportunity.PipelineRunID == nil || opportunity.PipelineRunTradeDate == nil || order.StrategyID == nil {
		return PaperOrderResult{}, errors.New("portfolio: complete recovered order scope is required")
	}
	versionID, err := uuid.Parse(opportunity.OriginID)
	if err != nil {
		return PaperOrderResult{}, fmt.Errorf("portfolio: recovered strategy version: %w", err)
	}
	scope, err := execution.NewStrategyExecutionScope(opportunity.AccountID, opportunity.Environment, versionID, domain.PipelineRunRef{ID: *opportunity.PipelineRunID, TradeDate: *opportunity.PipelineRunTradeDate}, *order.StrategyID)
	if err != nil {
		return PaperOrderResult{}, err
	}
	if claimID == uuid.Nil || p.deps.OpportunityRepo == nil {
		return PaperOrderResult{}, errors.New("portfolio: allocation claim repository is required")
	}
	manager := execution.NewOrderManager(p.deps.PaperBroker, "paper", p.deps.RiskEngine, p.deps.PositionRepo, p.deps.OrderRepo, p.deps.TradeRepo, p.deps.AuditLogRepo, p.deps.AgentEventRepo, execution.SizingConfig{}, p.deps.Logger).
		WithAcceptedOrderFillWriter(p.deps.EconomicWriter).WithLiveTrading(false).
		WithEffectFence(p.claimFence(opportunity.ID, claimID)).
		WithDecisionRecorder(p.deps.DecisionRecorder)
	status, err := manager.ReconcilePersistedOrder(ctx, scope, order)
	return PaperOrderResult{OrderID: &order.ID, Status: status}, err
}

func (p *PaperOrderManagerProcessor) claimFence(opportunityID, claimID uuid.UUID) func(context.Context) error {
	return func(ctx context.Context) error {
		owned, err := p.deps.OpportunityRepo.RenewAllocationClaim(ctx, opportunityID, claimID, AllocationClaimLease)
		if err != nil {
			return err
		}
		if !owned {
			return errors.New("allocation claim ownership lost")
		}
		return nil
	}
}

// Compile-time assertion that the processor stays on the paper execution boundary.
var (
	_ PaperOrderProcessor  = (*PaperOrderManagerProcessor)(nil)
	_ PaperOrderReconciler = (*PaperOrderManagerProcessor)(nil)
)

// Avoid an unused import regression when domain constants move; this also keeps
// the file colocated with portfolio market semantics.
var _ = domain.MarketTypeStock

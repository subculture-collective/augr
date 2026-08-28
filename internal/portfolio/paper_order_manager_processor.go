package portfolio

import (
	"context"
	"errors"
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
	FinancialLifecycleRepo repository.FinancialLifecycleRepository
	RiskEngine             risk.RiskEngine
	PositionRepo           repository.PositionRepository
	OrderRepo              repository.OrderRepository
	TradeRepo              repository.TradeRepository
	AuditLogRepo           repository.AuditLogRepository
	AgentEventRepo         repository.AgentEventRepository
	DecisionRecorder       execution.DecisionRecorder
	Metrics                execution.OrderMetricsRecorder
	Logger                 *slog.Logger
	InitialBalance         float64
	FractionPct            float64
	PaperBroker            *paper.PaperBroker
}

type allocationOrderRepo struct {
	repository.OrderRepository
	opportunityID uuid.UUID
	claimID       uuid.UUID
}

func (r allocationOrderRepo) Create(ctx context.Context, order *domain.Order) error {
	order.AllocationOpportunityID = &r.opportunityID
	order.AllocationClaimID = &r.claimID
	return r.OrderRepository.Create(ctx, order)
}

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
	).WithFinancialLifecycleRepo(p.deps.FinancialLifecycleRepo).WithLiveTrading(false)
	if p.deps.Metrics != nil {
		manager = manager.WithMetrics(p.deps.Metrics)
	}
	if p.deps.DecisionRecorder != nil {
		manager = manager.WithDecisionRecorder(p.deps.DecisionRecorder)
	}
	processErr := manager.ProcessSignal(ctx, request.Scope, request.Signal, request.Plan)
	if p.deps.OrderRepo == nil {
		return PaperOrderResult{Skipped: true, Reason: "missing_order_repo"}, nil
	}
	if allocationRepo, ok := p.deps.OrderRepo.(repository.AllocationOrderRepository); ok && request.OpportunityID != uuid.Nil {
		order, err := allocationRepo.GetByAllocationOpportunity(ctx, request.OpportunityID)
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
	run, ok := request.Scope.PipelineRun()
	if !ok {
		return PaperOrderResult{Skipped: true, Reason: "missing_pipeline_run"}, nil
	}
	const pageSize = 100
	orders := make([]domain.Order, 0)
	for offset := 0; ; offset += pageSize {
		page, err := p.deps.OrderRepo.GetByRun(ctx, run, repository.OrderFilter{}, pageSize, offset)
		if err != nil {
			return PaperOrderResult{}, errors.Join(processErr, err)
		}
		orders = append(orders, page...)
		if len(page) < pageSize {
			break
		}
	}
	if len(orders) == 0 {
		if processErr != nil {
			return PaperOrderResult{}, processErr
		}
		return PaperOrderResult{Skipped: true, Reason: "paper_order_not_created"}, nil
	}
	var order domain.Order
	found := false
	originType, originID := request.Scope.Origin()
	for _, candidate := range orders {
		if candidate.AccountID == request.Scope.AccountID() && candidate.Environment == request.Scope.Environment() && candidate.OriginType == string(originType) && candidate.OriginID == originID && candidate.PipelineRunID != nil && *candidate.PipelineRunID == run.ID && candidate.PipelineRunTradeDate != nil && candidate.PipelineRunTradeDate.Equal(run.TradeDate) {
			order, found = candidate, true
			break
		}
	}
	if !found {
		if processErr != nil {
			return PaperOrderResult{}, processErr
		}
		return PaperOrderResult{Skipped: true, Reason: "paper_order_scope_mismatch"}, nil
	}
	return PaperOrderResult{OrderID: &order.ID, Status: order.Status, Skipped: false}, processErr
}

// Compile-time assertion that the processor stays on the paper execution boundary.
var _ PaperOrderProcessor = (*PaperOrderManagerProcessor)(nil)

// Avoid an unused import regression when domain constants move; this also keeps
// the file colocated with portfolio market semantics.
var _ = domain.MarketTypeStock

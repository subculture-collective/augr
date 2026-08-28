package copytrading

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/paper"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

type OrderManagerExecutorDeps struct {
	ExecutionAccount   domain.ExecutionAccountBinding
	Broker             *paper.PaperBroker
	Risk               risk.RiskEngine
	Positions          repository.PositionRepository
	Orders             repository.OrderRepository
	Trades             repository.TradeRepository
	FinancialLifecycle repository.FinancialLifecycleRepository
	Audit              repository.AuditLogRepository
	Events             repository.AgentEventRepository
	DecisionRecorder   execution.DecisionRecorder
	Metrics            execution.OrderMetricsRecorder
	Logger             *slog.Logger
}

// OrderManagerExecutor adapts copy intents to Augr's existing risk and paper
// order lifecycle. It never enables live trading.
type OrderManagerExecutor struct{ deps OrderManagerExecutorDeps }

type claimedCopyOrderRepo struct {
	repository.OrderRepository
	intentID uuid.UUID
	claimID  uuid.UUID
}

func (r claimedCopyOrderRepo) Create(ctx context.Context, order *domain.Order) error {
	if err := r.DecorateOrder(order); err != nil {
		return err
	}
	return r.OrderRepository.Create(ctx, order)
}

func (r claimedCopyOrderRepo) DecorateOrder(order *domain.Order) error {
	if order == nil || r.intentID == uuid.Nil || r.claimID == uuid.Nil {
		return errors.New("copy order intent and execution claim are required")
	}
	order.CopyIntentID, order.CopyExecutionClaimID = &r.intentID, &r.claimID
	return nil
}

func NewOrderManagerExecutor(deps OrderManagerExecutorDeps) *OrderManagerExecutor {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &OrderManagerExecutor{deps: deps}
}

func (e *OrderManagerExecutor) ExecuteCopyOrder(ctx context.Context, request PaperOrderRequest) (PaperOrderResult, error) {
	if e == nil || e.deps.Orders == nil {
		return PaperOrderResult{}, fmt.Errorf("copy paper executor dependencies are unavailable")
	}
	locker, ok := e.deps.Orders.(repository.ExecutionAccountLocker)
	if !ok {
		return PaperOrderResult{}, fmt.Errorf("copy pending recovery requires execution account locker")
	}
	var result PaperOrderResult
	err := locker.WithExecutionAccountLock(ctx, request.Scope.AccountID(), func() error {
		var innerErr error
		result, innerErr = e.executeCopyOrderLocked(ctx, request)
		return innerErr
	})
	return result, err
}

// ExecuteCopyOrderWithAccountLockHeld executes under an account lock owned by the caller.
func (e *OrderManagerExecutor) ExecuteCopyOrderWithAccountLockHeld(ctx context.Context, request PaperOrderRequest) (PaperOrderResult, error) {
	return e.executeCopyOrderLocked(ctx, request)
}

func (e *OrderManagerExecutor) executeCopyOrderLocked(ctx context.Context, request PaperOrderRequest) (PaperOrderResult, error) {
	if e == nil || e.deps.Broker == nil || e.deps.Risk == nil || e.deps.Positions == nil || e.deps.Orders == nil || e.deps.Trades == nil {
		return PaperOrderResult{}, fmt.Errorf("copy paper executor dependencies are unavailable")
	}
	if request.Intent.ExecutablePrice == nil || *request.Intent.ExecutablePrice <= 0 || request.Intent.RequestedNotional <= 0 {
		return PaperOrderResult{}, fmt.Errorf("copy intent has no executable price or notional")
	}
	if err := e.deps.ExecutionAccount.Validate(); err != nil {
		return PaperOrderResult{}, fmt.Errorf("copy execution account: %w", err)
	}
	if request.Subscription.AccountID != e.deps.ExecutionAccount.AccountID() || request.Subscription.Environment != e.deps.ExecutionAccount.Environment() ||
		request.Intent.AccountID != request.Subscription.AccountID || request.Intent.Environment != request.Subscription.Environment ||
		request.Intent.SubscriptionID != request.Subscription.ID || request.Intent.OriginType != "copy_subscription" || request.Intent.OriginID != request.Subscription.ID {
		return PaperOrderResult{}, fmt.Errorf("copy execution attribution does not match configured account and origin")
	}
	if request.Scope.AccountID() != request.Subscription.AccountID || request.Scope.Environment() != request.Subscription.Environment || request.Scope.CopyOriginRunID() != request.OriginRunID {
		return PaperOrderResult{}, fmt.Errorf("copy execution scope does not match persisted subscription and run")
	}
	if request.ClaimID == uuid.Nil {
		return PaperOrderResult{}, fmt.Errorf("copy execution claim is required")
	}
	existing, err := e.deps.Orders.GetByCopyOriginRun(ctx, e.deps.ExecutionAccount.AccountID(), e.deps.ExecutionAccount.Environment(), request.Subscription.ID, request.OriginRunID, repository.OrderFilter{Ticker: request.Intent.Ticker, Side: request.Intent.Side}, 2, 0)
	if err != nil {
		return PaperOrderResult{}, err
	}
	balance, err := e.deps.Broker.GetAccountBalance(ctx)
	if err != nil {
		return PaperOrderResult{}, err
	}
	if balance.Equity <= 0 {
		return PaperOrderResult{}, fmt.Errorf("paper account equity is not positive")
	}
	fraction := request.Intent.RequestedNotional / balance.Equity
	orderRepo := claimedCopyOrderRepo{OrderRepository: e.deps.Orders, intentID: request.Intent.ID, claimID: request.ClaimID}
	manager := execution.NewOrderManager(e.deps.Broker, "paper", e.deps.Risk, e.deps.Positions, orderRepo, e.deps.Trades, e.deps.Audit, e.deps.Events, execution.SizingConfig{Method: execution.PositionSizingMethodFixedFractional, FractionPct: fraction}, e.deps.Logger).
		WithFinancialLifecycleRepo(e.deps.FinancialLifecycle).
		WithDecisionRecorder(e.deps.DecisionRecorder).
		WithLiveTrading(false)
	if e.deps.Metrics != nil {
		manager = manager.WithMetrics(e.deps.Metrics)
	}
	signal := domain.PipelineSignalBuy
	if request.Intent.Side == domain.OrderSideSell {
		signal = domain.PipelineSignalSell
	}
	price := *request.Intent.ExecutablePrice
	scope := request.Scope
	plan := execution.TradingPlan{Action: signal, MarketType: domain.MarketTypeStock, Ticker: request.Intent.Ticker, EntryType: "limit", EntryPrice: price, ReferencePrice: price, PositionSize: request.Intent.RequestedNotional / price, Confidence: 1, Rationale: "deterministic copy-subscription rebalance"}
	if len(existing) > 0 {
		result, matchErr := matchingCopyOrderResult(existing, request)
		result.Scope = scope
		if matchErr != nil {
			return result, matchErr
		}
		switch result.Status {
		case domain.OrderStatusPending, domain.OrderStatusSubmitted, domain.OrderStatusPartial:
		default:
			return result, nil
		}
		persisted, reloadErr := e.deps.Orders.Get(ctx, existing[0].ID)
		if reloadErr != nil {
			return result, fmt.Errorf("reload pending copy order: %w", reloadErr)
		}
		order := *persisted
		provider, ok := any(e.deps.Broker).(execution.BrokerClientOrderStatusProvider)
		if !ok {
			return result, fmt.Errorf("copy pending recovery requires client-id lookup")
		}
		externalID, brokerResult, lookupErr := provider.GetOrderStatusByClientOrderIDResult(ctx, order.ClientOrderID)
		if lookupErr != nil && !errors.Is(lookupErr, execution.ErrBrokerOrderNotFound) {
			return result, fmt.Errorf("lookup pending copy order: %w", lookupErr)
		}
		if errors.Is(lookupErr, execution.ErrBrokerOrderNotFound) {
			if order.Status != domain.OrderStatusPending || strings.TrimSpace(order.ExternalID) != "" {
				return result, fmt.Errorf("lookup persisted copy order: %w", lookupErr)
			}
			var submitErr error
			externalID, submitErr = e.deps.Broker.SubmitOrder(ctx, &order)
			if submitErr != nil {
				return PaperOrderResult{Scope: scope, OrderID: &order.ID, Status: order.Status}, fmt.Errorf("resume pending copy order remains ambiguous: %w", submitErr)
			}
			brokerResult, lookupErr = e.deps.Broker.GetOrderStatusResult(ctx, externalID)
			if lookupErr != nil {
				return result, fmt.Errorf("verify resumed copy order: %w", lookupErr)
			}
		}
		order.ExternalID, order.Status = externalID, brokerResult.Status
		order.FilledQuantity, order.FilledAvgPrice, order.FilledAt = brokerResult.FilledQuantity, brokerResult.FilledAvgPrice, brokerResult.FilledAt
		result.Status = order.Status
		submittedAt := time.Now().UTC()
		order.SubmittedAt = &submittedAt
		if order.Status == domain.OrderStatusPending {
			order.Status = domain.OrderStatusSubmitted
		}
		if order.FilledQuantity > persisted.FilledQuantity {
			if err := manager.HandleFillForTest(ctx, &order, plan, scope, uuid.Nil); err != nil {
				return result, fmt.Errorf("persist resumed copy fill: %w", err)
			}
		} else if err := e.deps.Orders.Update(ctx, &order); err != nil {
			return result, fmt.Errorf("persist resumed copy order: %w", err)
		}
		return PaperOrderResult{Scope: scope, OrderID: &order.ID, Status: order.Status}, nil
	}
	if err := manager.ProcessSignalWithAccountLockHeld(ctx, scope, execution.FinalSignal{Signal: signal, Confidence: 1}, plan); err != nil {
		orders, reloadErr := e.deps.Orders.GetByCopyOriginRun(ctx, e.deps.ExecutionAccount.AccountID(), e.deps.ExecutionAccount.Environment(), request.Subscription.ID, request.OriginRunID, repository.OrderFilter{Ticker: request.Intent.Ticker, Side: request.Intent.Side}, 10, 0)
		if reloadErr != nil {
			return PaperOrderResult{}, fmt.Errorf("%v; reload persisted copy order: %w", err, reloadErr)
		}
		persisted, matchErr := matchingCopyOrderResult(orders, request)
		persisted.Scope = scope
		if matchErr == nil || persisted.OrderID != nil {
			return persisted, err
		}
		return PaperOrderResult{}, err
	}
	orders, err := e.deps.Orders.GetByCopyOriginRun(ctx, e.deps.ExecutionAccount.AccountID(), e.deps.ExecutionAccount.Environment(), request.Subscription.ID, request.OriginRunID, repository.OrderFilter{Ticker: request.Intent.Ticker, Side: request.Intent.Side}, 10, 0)
	if err != nil {
		return PaperOrderResult{}, err
	}
	result, err := matchingCopyOrderResult(orders, request)
	result.Scope = scope
	return result, err
}

func matchingCopyOrderResult(orders []domain.Order, request PaperOrderRequest) (PaperOrderResult, error) {
	if len(orders) != 1 {
		return PaperOrderResult{}, fmt.Errorf("copy execution requires exactly one matching order, got %d", len(orders))
	}
	order := orders[0]
	price := *request.Intent.ExecutablePrice
	quantity := request.Intent.RequestedNotional / price
	if order.AccountID != request.Subscription.AccountID || order.Environment != request.Subscription.Environment ||
		order.OriginType != "copy_subscription" || order.OriginID != request.Subscription.ID.String() || order.CopyOriginRebalanceRunID != request.OriginRunID ||
		order.MarketType.Normalize() != domain.MarketTypeStock || order.Ticker != request.Intent.Ticker || order.Side != request.Intent.Side ||
		order.OrderType != domain.OrderTypeLimit || order.LimitPrice == nil || math.Abs(*order.LimitPrice-price) > 1e-8 || math.Abs(order.Quantity-quantity) > 1e-8 {
		return PaperOrderResult{}, fmt.Errorf("persisted copy order does not match requested stock command")
	}
	if order.ID == uuid.Nil || !order.Status.IsValid() {
		return PaperOrderResult{}, fmt.Errorf("persisted copy order result is incomplete")
	}
	if order.Status == domain.OrderStatusRejected || order.Status == domain.OrderStatusCancelled {
		return PaperOrderResult{OrderID: &order.ID, Status: order.Status}, fmt.Errorf("persisted copy order is %s", order.Status)
	}
	return PaperOrderResult{OrderID: &order.ID, Status: order.Status}, nil
}

var _ PaperOrderExecutor = (*OrderManagerExecutor)(nil)

package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

// Order reconciliation polls broker state for orders the submission path
// observed once and then abandoned. Every five minutes it refreshes every
// non-terminal order older than the minimum age through the OrderManager's
// existing ReconcilePersistedOrder path, which records fills, cancels expired
// resting orders, and persists the latest status.
const (
	orderReconcileJobName       = "order_reconcile"
	defaultOrderReconcileMinAge = 2 * time.Minute
	defaultOrderReconcileLimit  = 200
)

var orderReconcileSpec = scheduler.ScheduleSpec{
	Type: scheduler.ScheduleTypeCron,
	Cron: "*/5 * * * *",
}

// NonTerminalOrderLister lists open orders older than a threshold for one
// account environment. The PostgreSQL OrderRepo implements it.
type NonTerminalOrderLister interface {
	ListNonTerminalOlderThan(ctx context.Context, environment domain.AccountEnvironment, threshold time.Time, limit int) ([]domain.Order, error)
}

// OrderReconcileManager is the subset of OrderManager the job needs.
type OrderReconcileManager interface {
	ReconcilePersistedOrder(context.Context, execution.ExecutionScope, *domain.Order) (domain.OrderStatus, error)
}

// OrderReconcileManagerSource builds an order manager whose broker matches the
// persisted order (broker name and market type). The strategy runner
// implements it.
type OrderReconcileManagerSource interface {
	OrderManagerForOrder(context.Context, domain.Order) (OrderReconcileManager, error)
}

// OrderReconcileDeps wires the order reconciliation job.
type OrderReconcileDeps struct {
	ExecutionAccount domain.ExecutionAccountBinding
	Orders           NonTerminalOrderLister
	Managers         OrderReconcileManagerSource
	MinAge           time.Duration // default 2m
	Limit            int           // default 200
	Logger           *slog.Logger
	Now              func() time.Time
}

// OrderReconcileSummary counts the outcomes of one job run.
type OrderReconcileSummary struct {
	Candidates int
	Reconciled int
	Filled     int
	Cancelled  int
	Rejected   int
	StillOpen  int
	Errors     int
}

func (s OrderReconcileSummary) Map() map[string]int {
	return map[string]int{
		"candidates": s.Candidates,
		"reconciled": s.Reconciled,
		"filled":     s.Filled,
		"cancelled":  s.Cancelled,
		"rejected":   s.Rejected,
		"still_open": s.StillOpen,
		"errors":     s.Errors,
	}
}

// OrderReconciler runs the reconciliation pass. It is separate from the
// orchestrator so it can be exercised without cron.
type OrderReconciler struct {
	deps OrderReconcileDeps
}

func NewOrderReconciler(deps OrderReconcileDeps) *OrderReconciler {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.MinAge <= 0 {
		deps.MinAge = defaultOrderReconcileMinAge
	}
	if deps.Limit <= 0 {
		deps.Limit = defaultOrderReconcileLimit
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &OrderReconciler{deps: deps}
}

// Run reconciles every eligible order. Per-order failures are counted and
// logged; the pass continues so one broken order cannot starve the others.
// It returns an error only when no order could be reconciled at all.
func (r *OrderReconciler) Run(ctx context.Context) (OrderReconcileSummary, error) {
	var summary OrderReconcileSummary
	if r == nil || r.deps.Orders == nil || r.deps.Managers == nil {
		return summary, errors.New("order_reconcile: order lister and manager source are required")
	}
	if err := r.deps.ExecutionAccount.Validate(); err != nil {
		return summary, fmt.Errorf("order_reconcile: execution account: %w", err)
	}
	threshold := r.deps.Now().UTC().Add(-r.deps.MinAge)
	orders, err := r.deps.Orders.ListNonTerminalOlderThan(ctx, r.deps.ExecutionAccount.Environment(), threshold, r.deps.Limit)
	if err != nil {
		return summary, fmt.Errorf("order_reconcile: list non-terminal orders: %w", err)
	}
	summary.Candidates = len(orders)
	var firstErr error
	for index := range orders {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		order := orders[index]
		if order.AccountID != r.deps.ExecutionAccount.AccountID() || order.Environment != r.deps.ExecutionAccount.Environment() {
			summary.Errors++
			r.deps.Logger.WarnContext(ctx, "order_reconcile: order escaped execution account scope", "order_id", order.ID)
			continue
		}
		status, reconcileErr := r.reconcileOne(ctx, order)
		if reconcileErr != nil {
			summary.Errors++
			if firstErr == nil {
				firstErr = reconcileErr
			}
			r.deps.Logger.WarnContext(ctx, "order_reconcile: order reconciliation failed", "order_id", order.ID, "ticker", order.Ticker, "broker", order.Broker, "status", order.Status, "error", reconcileErr)
			continue
		}
		summary.Reconciled++
		switch status {
		case domain.OrderStatusFilled:
			summary.Filled++
		case domain.OrderStatusCancelled:
			summary.Cancelled++
		case domain.OrderStatusRejected:
			summary.Rejected++
		default:
			summary.StillOpen++
		}
		r.deps.Logger.InfoContext(ctx, "order_reconcile: order reconciled", "order_id", order.ID, "ticker", order.Ticker, "broker", order.Broker, "prior_status", order.Status, "status", status)
	}
	if summary.Candidates > 0 && summary.Reconciled == 0 && firstErr != nil {
		return summary, fmt.Errorf("order_reconcile: no candidate order could be reconciled: %w", firstErr)
	}
	return summary, nil
}

func (r *OrderReconciler) reconcileOne(ctx context.Context, order domain.Order) (domain.OrderStatus, error) {
	scope, err := execution.ExecutionScopeFromOrder(order)
	if err != nil {
		return "", fmt.Errorf("derive execution scope: %w", err)
	}
	manager, err := r.deps.Managers.OrderManagerForOrder(ctx, order)
	if err != nil {
		return "", fmt.Errorf("build order manager for broker %q: %w", strings.TrimSpace(order.Broker), err)
	}
	if manager == nil {
		return "", fmt.Errorf("no order manager for broker %q", strings.TrimSpace(order.Broker))
	}
	persisted := order
	return manager.ReconcilePersistedOrder(ctx, scope, &persisted)
}

// RegisterOrderReconciliation registers the order_reconcile cron job. Call it
// after RegisterAll and before Start; runtime wiring supplies the deps.
func (o *JobOrchestrator) RegisterOrderReconciliation(deps OrderReconcileDeps) {
	if o == nil {
		return
	}
	if deps.Logger == nil {
		deps.Logger = o.logger
	}
	if deps.ExecutionAccount.AccountID() == uuid.Nil {
		deps.ExecutionAccount = o.deps.ExecutionAccount
	}
	reconciler := NewOrderReconciler(deps)
	o.Register(
		orderReconcileJobName,
		"Refresh non-terminal broker orders older than the minimum age (fills, cancellations, expiry)",
		orderReconcileSpec,
		func(ctx context.Context) error {
			o.logger.Info("order_reconcile: starting")
			summary, err := reconciler.Run(ctx)
			o.SetLastSummary(orderReconcileJobName, summary.Map())
			if err != nil {
				return err
			}
			if summary.Errors > 0 && o.metrics != nil {
				if degraded, ok := o.metrics.(interface{ RecordAutomationJobDegraded(string) }); ok {
					degraded.RecordAutomationJobDegraded(orderReconcileJobName)
				}
			}
			o.logger.Info("order_reconcile: complete",
				slog.Int("candidates", summary.Candidates),
				slog.Int("reconciled", summary.Reconciled),
				slog.Int("filled", summary.Filled),
				slog.Int("cancelled", summary.Cancelled),
				slog.Int("rejected", summary.Rejected),
				slog.Int("still_open", summary.StillOpen),
				slog.Int("errors", summary.Errors),
			)
			return nil
		},
	)
}

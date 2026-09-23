package automation

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

type orderListerStub struct {
	orders    []domain.Order
	err       error
	threshold time.Time
	limit     int
	env       domain.AccountEnvironment
}

func (s *orderListerStub) ListNonTerminalOlderThan(_ context.Context, environment domain.AccountEnvironment, threshold time.Time, limit int) ([]domain.Order, error) {
	s.threshold, s.limit, s.env = threshold, limit, environment
	return s.orders, s.err
}

type reconcileManagerStub struct {
	statuses map[uuid.UUID]domain.OrderStatus
	errs     map[uuid.UUID]error
	calls    []uuid.UUID
	scopes   []execution.ExecutionScope
}

func (s *reconcileManagerStub) ReconcilePersistedOrder(_ context.Context, scope execution.ExecutionScope, order *domain.Order) (domain.OrderStatus, error) {
	s.calls = append(s.calls, order.ID)
	s.scopes = append(s.scopes, scope)
	if err := s.errs[order.ID]; err != nil {
		return "", err
	}
	return s.statuses[order.ID], nil
}

type managerSourceStub struct {
	manager  *reconcileManagerStub
	brokers  []string
	buildErr error
}

func (s *managerSourceStub) OrderManagerForOrder(_ context.Context, order domain.Order) (OrderReconcileManager, error) {
	s.brokers = append(s.brokers, order.Broker)
	if s.buildErr != nil {
		return nil, s.buildErr
	}
	return s.manager, nil
}

func reconcileTestOrder(account domain.ExecutionAccountBinding, broker string, status domain.OrderStatus) domain.Order {
	versionID := uuid.New()
	runID := uuid.New()
	tradeDate := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	return domain.Order{ID: uuid.New(), AccountID: account.AccountID(), Environment: account.Environment(), OriginType: string(ledger.ExecutionOriginStrategyVersion), OriginID: versionID.String(), PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, Ticker: "SPY", MarketType: domain.MarketTypeStock, Side: domain.OrderSideBuy, Quantity: 1, Status: status, Broker: broker, CreatedAt: time.Now().Add(-time.Hour)}
}

func TestOrderReconcilerRunRefreshesEachCandidateWithMatchingBroker(t *testing.T) {
	t.Parallel()
	account, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	filled := reconcileTestOrder(account, "alpaca", domain.OrderStatusSubmitted)
	resting := reconcileTestOrder(account, "paper", domain.OrderStatusPending)
	failing := reconcileTestOrder(account, "alpaca", domain.OrderStatusPartial)
	foreign := reconcileTestOrder(account, "alpaca", domain.OrderStatusSubmitted)
	foreign.AccountID = uuid.New()
	manager := &reconcileManagerStub{
		statuses: map[uuid.UUID]domain.OrderStatus{filled.ID: domain.OrderStatusFilled, resting.ID: domain.OrderStatusSubmitted},
		errs:     map[uuid.UUID]error{failing.ID: errors.New("broker unavailable")},
	}
	lister := &orderListerStub{orders: []domain.Order{filled, resting, failing, foreign}}
	source := &managerSourceStub{manager: manager}
	now := time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC)
	reconciler := NewOrderReconciler(OrderReconcileDeps{ExecutionAccount: account, Orders: lister, Managers: source, Logger: slog.New(slog.NewTextHandler(testDiscardWriter{}, nil)), Now: func() time.Time { return now }})

	summary, err := reconciler.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if lister.env != account.Environment() || !lister.threshold.Equal(now.Add(-defaultOrderReconcileMinAge)) || lister.limit != defaultOrderReconcileLimit {
		t.Fatalf("lister query = env %s threshold %s limit %d", lister.env, lister.threshold, lister.limit)
	}
	if summary.Candidates != 4 || summary.Reconciled != 2 || summary.Filled != 1 || summary.StillOpen != 1 || summary.Errors != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(manager.calls) != 3 || manager.calls[0] != filled.ID || manager.calls[1] != resting.ID || manager.calls[2] != failing.ID {
		t.Fatalf("reconcile calls = %v, want the three in-scope orders", manager.calls)
	}
	if len(source.brokers) != 3 || source.brokers[0] != "alpaca" || source.brokers[1] != "paper" {
		t.Fatalf("managers built for brokers %v", source.brokers)
	}
	if got, _ := manager.scopes[0].PipelineRun(); got.ID != *filled.PipelineRunID {
		t.Fatalf("scope run = %s, want %s", got.ID, *filled.PipelineRunID)
	}
}

func TestOrderReconcilerRunReportsTotalFailureAndListerErrors(t *testing.T) {
	t.Parallel()
	account, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	order := reconcileTestOrder(account, "alpaca", domain.OrderStatusSubmitted)
	logger := slog.New(slog.NewTextHandler(testDiscardWriter{}, nil))

	reconciler := NewOrderReconciler(OrderReconcileDeps{ExecutionAccount: account, Orders: &orderListerStub{orders: []domain.Order{order}}, Managers: &managerSourceStub{buildErr: errors.New("no broker")}, Logger: logger})
	summary, err := reconciler.Run(context.Background())
	if err == nil || summary.Errors != 1 || summary.Reconciled != 0 {
		t.Fatalf("all-failed run = %+v, %v; want error", summary, err)
	}

	reconciler = NewOrderReconciler(OrderReconcileDeps{ExecutionAccount: account, Orders: &orderListerStub{err: errors.New("db down")}, Managers: &managerSourceStub{}, Logger: logger})
	if _, err := reconciler.Run(context.Background()); err == nil {
		t.Fatal("lister failure must surface")
	}

	empty := NewOrderReconciler(OrderReconcileDeps{ExecutionAccount: account, Orders: &orderListerStub{}, Managers: &managerSourceStub{}, Logger: logger})
	if summary, err := empty.Run(context.Background()); err != nil || summary.Candidates != 0 {
		t.Fatalf("empty run = %+v, %v", summary, err)
	}
}

func TestOrderReconcileJobRegistersOnFiveMinuteCron(t *testing.T) {
	t.Parallel()
	if orderReconcileSpec.Cron != "*/5 * * * *" || orderReconcileSpec.Type != "cron" {
		t.Fatalf("spec = %+v", orderReconcileSpec)
	}
	orch := &JobOrchestrator{jobs: map[string]*RegisteredJob{}, logger: slog.New(slog.NewTextHandler(testDiscardWriter{}, nil))}
	orch.RegisterOrderReconciliation(OrderReconcileDeps{})
	job, ok := orch.jobs[orderReconcileJobName]
	if !ok || job.Schedule.Cron != orderReconcileSpec.Cron {
		t.Fatalf("job = %+v registered=%v", job, ok)
	}
}

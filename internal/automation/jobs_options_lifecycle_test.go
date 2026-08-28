package automation

import (
	"context"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

type optionSettlementRepoStub struct{}

func (optionSettlementRepoStub) SettleOptionPosition(_ context.Context, input repository.OptionPositionSettlementInput) (repository.OptionPositionSettlementResult, error) {
	return repository.OptionPositionSettlementResult{PositionID: input.PositionID}, nil
}

func TestRegisterOptionsLifecycleJobRequiresPersistenceAndMarketData(t *testing.T) {
	withoutDeps := NewJobOrchestrator(OrchestratorDeps{})
	withoutDeps.RegisterAll()
	if _, ok := withoutDeps.jobs["options_expiry_settlement"]; ok {
		t.Fatal("expiry job registered without lifecycle dependencies")
	}

	orders := newRecordingOrderRepo()
	withDeps := NewJobOrchestrator(OrchestratorDeps{PositionRepo: newRecordingPositionRepo(), OrderRepo: orders, TradeRepo: newRecordingTradeRepo(orders), OptionSettlementRepo: optionSettlementRepoStub{}, DataService: &data.DataService{}})
	withDeps.RegisterAll()
	job, ok := withDeps.jobs["options_expiry_settlement"]
	if !ok || !job.Enabled || job.Schedule.Cron != "0 23 * * 1-5" {
		t.Fatalf("expiry job not registered correctly: %+v", job)
	}
	if reconcile, ok := withDeps.jobs["options_lifecycle_reconcile"]; !ok || reconcile.Schedule.Cron != "30 23 * * 1-5" {
		t.Fatalf("reconciliation job not registered: %+v", reconcile)
	}
}

func TestOptionsLifecycleReconcileJobAcceptsEmptyDurableGraph(t *testing.T) {
	orders := newRecordingOrderRepo()
	binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	orch := NewJobOrchestrator(OrchestratorDeps{ExecutionAccount: binding, OrderRepo: orders, PositionRepo: newRecordingPositionRepo(), TradeRepo: newRecordingTradeRepo(orders)})
	orch.RegisterAll()
	if err := orch.optionsLifecycleReconcile(context.Background()); err != nil {
		t.Fatalf("optionsLifecycleReconcile() error = %v", err)
	}
	if summary := orch.jobs["options_lifecycle_reconcile"].LastSummary; summary["findings"] != 0 {
		t.Fatalf("unexpected reconciliation summary: %v", summary)
	}
}

type lifecycleScopedPositionRepo struct {
	*recordingPositionRepo
	accountID   uuid.UUID
	environment domain.AccountEnvironment
}

func (r *lifecycleScopedPositionRepo) GetOpenByAccount(_ context.Context, accountID uuid.UUID, environment domain.AccountEnvironment, _ repository.PositionFilter, limit, offset int) ([]domain.Position, error) {
	r.accountID, r.environment = accountID, environment
	var positions []domain.Position
	for _, position := range r.open {
		if position.ClosedAt == nil && position.AccountID == accountID && position.Environment == environment {
			positions = append(positions, *clonePosition(position))
		}
	}
	return paginatePositions(positions, limit, offset), nil
}

func TestOptionsExpiryQueriesConfiguredAccountAndEnvironment(t *testing.T) {
	accountID := uuid.New()
	binding, err := domain.NewExecutionAccountBinding(accountID, domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	local := &domain.Position{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentPaperScored}
	foreignEnvironment := &domain.Position{ID: uuid.New(), AccountID: accountID, Environment: domain.AccountEnvironmentShadow}
	foreignAccount := &domain.Position{ID: uuid.New(), AccountID: uuid.New(), Environment: domain.AccountEnvironmentPaperScored}
	repo := &lifecycleScopedPositionRepo{recordingPositionRepo: newRecordingPositionRepo(local, foreignEnvironment, foreignAccount)}
	positions, err := listAllOpenPositionsByAccount(context.Background(), repo, binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].ID != local.ID {
		t.Fatalf("scoped expiry positions = %+v, want configured account/environment only", positions)
	}
	if repo.accountID != accountID || repo.environment != domain.AccountEnvironmentPaperScored {
		t.Fatalf("expiry query scope = %s/%s", repo.accountID, repo.environment)
	}
}

func TestOptionExpirySessionCloseUsesFridayForDelayedMondayRun(t *testing.T) {
	friday := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	bars := []domain.OHLCV{
		{Timestamp: friday.Add(-24 * time.Hour), Close: 99},
		{Timestamp: friday, Close: 101},
		{Timestamp: friday.Add(72 * time.Hour), Close: 150},
	}
	got, ok := optionExpirySessionClose(bars, friday)
	if !ok || got != 101 {
		t.Fatalf("expiry close=(%v,%v), want Friday close 101", got, ok)
	}
}

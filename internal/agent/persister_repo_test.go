package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type decisionCaptureRepo struct{ decision *domain.AgentDecision }

func (r *decisionCaptureRepo) Create(_ context.Context, decision *domain.AgentDecision) error {
	r.decision = decision
	return nil
}

func (*decisionCaptureRepo) GetByRun(context.Context, domain.PipelineRunRef, repository.AgentDecisionFilter, int, int) ([]domain.AgentDecision, error) {
	return nil, nil
}

func (*decisionCaptureRepo) CountByRun(context.Context, domain.PipelineRunRef, repository.AgentDecisionFilter) (int, error) {
	return 0, nil
}

type persisterTestNode struct{}

func (persisterTestNode) Name() string                                  { return "test" }
func (persisterTestNode) Role() AgentRole                               { return AgentRoleTrader }
func (persisterTestNode) Phase() Phase                                  { return PhaseTrading }
func (persisterTestNode) Execute(context.Context, *PipelineState) error { return nil }

func TestRepoPersisterPersistDecisionStoresFullRunRef(t *testing.T) {
	repo := &decisionCaptureRepo{}
	persister := NewRepoPersister(nil, nil, repo, nil, nil)
	ref := domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}

	if err := persister.PersistDecision(context.Background(), ref, persisterTestNode{}, nil, "hold", nil); err != nil {
		t.Fatalf("PersistDecision() error = %v", err)
	}
	if repo.decision == nil || repo.decision.PipelineRunID != ref.ID || !repo.decision.PipelineRunTradeDate.Equal(ref.TradeDate) {
		t.Fatalf("persisted decision = %+v, want run ref %+v", repo.decision, ref)
	}
}

type phaseCapturePersister struct {
	snapshots []domain.PipelineRunSnapshot
}

func (*phaseCapturePersister) RecordRunStart(context.Context, *domain.PipelineRun) error { return nil }
func (*phaseCapturePersister) FinalizeRun(context.Context, uuid.UUID, time.Time, repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	return repository.PipelineRunFinalizationReceipt{}, nil
}
func (*phaseCapturePersister) SupportsSnapshots() bool { return true }
func (p *phaseCapturePersister) PersistSnapshot(_ context.Context, snapshot *domain.PipelineRunSnapshot) error {
	p.snapshots = append(p.snapshots, *snapshot)
	return nil
}

func (*phaseCapturePersister) PersistDecision(context.Context, domain.PipelineRunRef, Node, *int, string, *DecisionLLMResponse) error {
	return nil
}
func (*phaseCapturePersister) PersistEvent(context.Context, *domain.AgentEvent) error { return nil }

func TestPhaseHelperStoresFullRunRef(t *testing.T) {
	persister := &phaseCapturePersister{}
	helper := newPhaseHelper(persister, nil, nil, nil)
	ref := domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	state := &PipelineState{PipelineRunID: ref.ID, PipelineRunTradeDate: ref.TradeDate}

	if err := helper.persistAnalysisSnapshots(context.Background(), state); err != nil {
		t.Fatalf("persistAnalysisSnapshots() error = %v", err)
	}
	if len(persister.snapshots) != 4 {
		t.Fatalf("snapshots = %d, want 4", len(persister.snapshots))
	}
	for _, snapshot := range persister.snapshots {
		if snapshot.PipelineRunID != ref.ID || !snapshot.PipelineRunTradeDate.Equal(ref.TradeDate) {
			t.Fatalf("snapshot run ref = (%s, %s), want %+v", snapshot.PipelineRunID, snapshot.PipelineRunTradeDate, ref)
		}
	}
	event := helper.newStructuredEvent(ref, uuid.New(), AgentEventKindPipelineStarted, "", "started", "", nil, nil)
	if event.PipelineRunID == nil || *event.PipelineRunID != ref.ID || event.PipelineRunTradeDate == nil || !event.PipelineRunTradeDate.Equal(ref.TradeDate) {
		t.Fatalf("event run ref = (%v, %v), want %+v", event.PipelineRunID, event.PipelineRunTradeDate, ref)
	}
}

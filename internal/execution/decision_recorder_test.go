package execution

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type decisionJournalStub struct {
	created     *domain.TradeDecision
	stored      map[uuid.UUID]domain.TradeDecision
	replay      *replayEventStub
	failAtomic  bool
	failInitial bool
	attachScope *repository.DecisionOrderAttachmentScope
}

func (s *decisionJournalStub) CreateWithInitialReplay(ctx context.Context, decision *domain.TradeDecision) error {
	if s.failInitial {
		s.failInitial = false
		return fmt.Errorf("injected initial replay failure")
	}
	if err := s.Create(ctx, decision); err != nil {
		return err
	}
	if s.replay == nil {
		return nil
	}
	for _, eventType := range []domain.ReplayEventType{domain.ReplayEventTypeDecisionCreated, domain.ReplayEventTypeRiskReviewed} {
		found := false
		for _, event := range s.replay.events {
			if event.TradeDecisionID == decision.ID && event.EventType == eventType {
				found = true
			}
		}
		if !found {
			s.replay.events = append(s.replay.events, domain.ReplayEvent{TradeDecisionID: decision.ID, AccountID: decision.AccountID, Environment: decision.Environment, OriginType: decision.OriginType, OriginID: decision.OriginID, EventType: eventType})
		}
	}
	return nil
}

func (s *decisionJournalStub) AttachOrderWithReplay(_ context.Context, decisionID, orderID uuid.UUID, live bool, source string, occurredAt time.Time) error {
	decision := s.stored[decisionID]
	attached := decision.PaperOrderID
	eventType := domain.ReplayEventTypePaperOrdered
	if live {
		attached, eventType = decision.LiveOrderID, domain.ReplayEventTypeLiveOrdered
	}
	if attached != nil && *attached != orderID {
		return fmt.Errorf("different order")
	}
	if s.failAtomic {
		s.failAtomic = false
		return fmt.Errorf("injected atomic failure")
	}
	if attached == nil {
		if live {
			decision.LiveOrderID = &orderID
		} else {
			decision.PaperOrderID = &orderID
		}
		s.stored[decisionID] = decision
	}
	if s.replay != nil {
		for _, event := range s.replay.events {
			if event.TradeDecisionID == decisionID && event.EventType == eventType {
				return nil
			}
		}
		s.replay.events = append(s.replay.events, domain.ReplayEvent{TradeDecisionID: decisionID, AccountID: decision.AccountID, Environment: decision.Environment, OriginType: decision.OriginType, OriginID: decision.OriginID, EventType: eventType, Source: source, OccurredAt: occurredAt})
	}
	return nil
}

func (s *decisionJournalStub) AttachOrderWithReplayScoped(ctx context.Context, decisionID, orderID uuid.UUID, live bool, source string, occurredAt time.Time, scope repository.DecisionOrderAttachmentScope) error {
	s.attachScope = &scope
	return s.AttachOrderWithReplay(ctx, decisionID, orderID, live, source, occurredAt)
}

func (s *decisionJournalStub) Create(_ context.Context, decision *domain.TradeDecision) error {
	s.created = decision
	if s.stored == nil {
		s.stored = make(map[uuid.UUID]domain.TradeDecision)
	}
	s.stored[decision.ID] = *decision
	return nil
}

func (s *decisionJournalStub) Get(_ context.Context, id uuid.UUID) (*domain.TradeDecision, error) {
	decision, ok := s.stored[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &decision, nil
}

func (*decisionJournalStub) List(context.Context, repository.TradeDecisionFilter, int, int) ([]domain.TradeDecision, error) {
	return nil, nil
}

func (*decisionJournalStub) Count(context.Context, repository.TradeDecisionFilter) (int, error) {
	return 0, nil
}

func (s *decisionJournalStub) AttachPaperOrder(_ context.Context, decisionID, orderID uuid.UUID) (bool, error) {
	decision := s.stored[decisionID]
	if decision.PaperOrderID != nil {
		if *decision.PaperOrderID == orderID {
			return false, nil
		}
		return false, fmt.Errorf("different order")
	}
	decision.PaperOrderID = &orderID
	s.stored[decisionID] = decision
	return true, nil
}

func (s *decisionJournalStub) AttachPaperOrderScoped(ctx context.Context, decisionID, orderID uuid.UUID, scope repository.DecisionOrderAttachmentScope) (bool, error) {
	s.attachScope = &scope
	return s.AttachPaperOrder(ctx, decisionID, orderID)
}

func (s *decisionJournalStub) GetByOrderScoped(_ context.Context, orderID uuid.UUID, live bool, _ repository.DecisionOrderAttachmentScope) (*domain.TradeDecision, error) {
	for _, decision := range s.stored {
		attached := decision.PaperOrderID
		if live {
			attached = decision.LiveOrderID
		}
		if attached != nil && *attached == orderID {
			cloned := decision
			return &cloned, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (*decisionJournalStub) AttachLiveOrder(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

func (s *decisionJournalStub) AttachLiveOrderScoped(ctx context.Context, decisionID, orderID uuid.UUID, scope repository.DecisionOrderAttachmentScope) (bool, error) {
	s.attachScope = &scope
	return s.AttachLiveOrder(ctx, decisionID, orderID)
}

type replayEventStub struct{ events []domain.ReplayEvent }

func (s *replayEventStub) CreateReplayEvent(_ context.Context, event *domain.ReplayEvent) error {
	s.events = append(s.events, *event)
	return nil
}

func (*replayEventStub) ListReplayEvents(context.Context, uuid.UUID) ([]domain.ReplayEvent, error) {
	return nil, nil
}

func TestTradeDecisionJournalRecorderWritesReplayLifecycle(t *testing.T) {
	journal := &decisionJournalStub{}
	replay := &replayEventStub{}
	journal.replay = replay
	recorder := NewTradeDecisionJournalRecorder(journal, replay)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	accountID, versionID, runID := uuid.New(), uuid.New(), uuid.New()
	tradeDate := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	scope, err := NewStrategyExecutionScope(accountID, domain.AccountEnvironmentPaperScored, versionID, domain.PipelineRunRef{ID: runID, TradeDate: tradeDate})
	if err != nil {
		t.Fatal(err)
	}
	decision := &domain.TradeDecision{
		ID: uuid.New(), MarketType: domain.MarketTypeKalshi, InstrumentKey: "KX-TEST",
		RiskStatus: domain.RiskDecisionApproved, Status: domain.TradeDecisionStatusCandidate,
		CreatedAt: now, UpdatedAt: now,
	}

	scoped := recorder.(ScopedDecisionRecorder)
	if err := scoped.RecordDecisionScoped(context.Background(), scope, decision); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	orderID := uuid.New()
	if err := scoped.AttachPaperOrderScoped(context.Background(), scope, decision.ID, orderID); err != nil {
		t.Fatalf("AttachPaperOrder() error = %v", err)
	}

	if journal.created != decision {
		t.Fatal("decision was not persisted")
	}
	want := []domain.ReplayEventType{domain.ReplayEventTypeDecisionCreated, domain.ReplayEventTypeRiskReviewed, domain.ReplayEventTypePaperOrdered}
	if len(replay.events) != len(want) {
		t.Fatalf("events = %d, want %d", len(replay.events), len(want))
	}
	for i := range want {
		if replay.events[i].EventType != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, replay.events[i].EventType, want[i])
		}
		if replay.events[i].TradeDecisionID != decision.ID {
			t.Fatalf("events[%d] decision id mismatch", i)
		}
		if replay.events[i].AccountID != accountID || replay.events[i].Environment != scope.Environment() || replay.events[i].OriginID != versionID.String() {
			t.Fatalf("events[%d] scope = %+v, want persisted decision scope", i, replay.events[i])
		}
	}
}

func TestTradeDecisionJournalRecorderRepairsMissingOrderAttachment(t *testing.T) {
	accountID, versionID, runID := uuid.New(), uuid.New(), uuid.New()
	tradeDate := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	scope, err := NewStrategyExecutionScope(accountID, domain.AccountEnvironmentPaperScored, versionID, domain.PipelineRunRef{ID: runID, TradeDate: tradeDate})
	if err != nil {
		t.Fatal(err)
	}
	decisionID, orderID := uuid.New(), uuid.New()
	decision := &domain.TradeDecision{ID: decisionID, AccountID: accountID, Environment: scope.Environment(), OriginType: "strategy_version", OriginID: versionID.String(), PipelineRunID: &runID, PipelineRunTradeDate: &tradeDate, MarketType: domain.MarketTypeStock, InstrumentKey: "AAPL", RiskStatus: domain.RiskDecisionApproved, Status: domain.TradeDecisionStatusCandidate}
	journal := &decisionJournalStub{stored: map[uuid.UUID]domain.TradeDecision{decisionID: *decision}}
	recorder := NewTradeDecisionJournalRecorder(journal).(RecoverableOrderDecisionRecorder)

	resolved, err := recorder.EnsureOrderDecisionAttachment(context.Background(), scope, decision, orderID, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != decisionID || journal.stored[decisionID].PaperOrderID == nil || *journal.stored[decisionID].PaperOrderID != orderID {
		t.Fatalf("resolved=%s stored=%+v", resolved, journal.stored[decisionID])
	}
}

func TestTradeDecisionJournalRecorderRejectsConflictingAccountRetry(t *testing.T) {
	journal := &decisionJournalStub{}
	journal.replay = &replayEventStub{}
	recorder := NewTradeDecisionJournalRecorder(journal, journal.replay).(ScopedDecisionRecorder)
	run := domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	versionID := uuid.New()
	first, _ := NewStrategyExecutionScope(uuid.New(), domain.AccountEnvironmentPaperScored, versionID, run)
	conflicting, _ := NewStrategyExecutionScope(uuid.New(), domain.AccountEnvironmentPaperScored, versionID, run)
	decision := &domain.TradeDecision{ID: uuid.New(), MarketType: domain.MarketTypeStock, InstrumentKey: "AAPL", Status: domain.TradeDecisionStatusCandidate}
	if err := recorder.RecordDecisionScoped(context.Background(), first, decision); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AttachPaperOrderScoped(context.Background(), conflicting, decision.ID, uuid.New()); err == nil {
		t.Fatal("AttachPaperOrderScoped() accepted conflicting account retry")
	}
}

func TestTradeDecisionJournalRecorderAttachesStrategyFreeCopyOrder(t *testing.T) {
	journal := &decisionJournalStub{replay: &replayEventStub{}}
	recorder := NewTradeDecisionJournalRecorder(journal, journal.replay).(ScopedDecisionRecorder)
	accountID, subscriptionID, copyRunID := uuid.New(), uuid.New(), uuid.New()
	scope, err := NewCopyExecutionScope(accountID, domain.AccountEnvironmentPaperScored, subscriptionID, copyRunID)
	if err != nil {
		t.Fatal(err)
	}
	decision := &domain.TradeDecision{ID: uuid.New(), MarketType: domain.MarketTypeStock, InstrumentKey: "AAPL", Status: domain.TradeDecisionStatusCandidate}
	if err := recorder.RecordDecisionScoped(context.Background(), scope, decision); err != nil {
		t.Fatalf("RecordDecisionScoped() error = %v", err)
	}
	if err := recorder.AttachPaperOrderScoped(context.Background(), scope, decision.ID, uuid.New()); err != nil {
		t.Fatalf("AttachPaperOrderScoped() error = %v", err)
	}
	if decision.PipelineRunID != nil || decision.PipelineRunTradeDate != nil || decision.StrategyID != nil {
		t.Fatalf("copy decision lineage = %+v", decision)
	}
	if journal.attachScope == nil || journal.attachScope.CopyOriginRebalanceRunID == nil || *journal.attachScope.CopyOriginRebalanceRunID != copyRunID || journal.attachScope.PipelineRunID != nil || journal.attachScope.StrategyID != nil {
		t.Fatalf("attachment scope = %+v", journal.attachScope)
	}
}

func TestTradeDecisionJournalRecorderRejectsConflictingLegacyStrategyAttachment(t *testing.T) {
	journal := &decisionJournalStub{replay: &replayEventStub{}}
	recorder := NewTradeDecisionJournalRecorder(journal, journal.replay).(ScopedDecisionRecorder)
	run := domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	accountID, versionID := uuid.New(), uuid.New()
	first, _ := NewStrategyExecutionScope(accountID, domain.AccountEnvironmentPaperScored, versionID, run, uuid.New())
	conflicting, _ := NewStrategyExecutionScope(accountID, domain.AccountEnvironmentPaperScored, versionID, run, uuid.New())
	decision := &domain.TradeDecision{ID: uuid.New(), MarketType: domain.MarketTypeStock, InstrumentKey: "AAPL", Status: domain.TradeDecisionStatusCandidate}
	if err := recorder.RecordDecisionScoped(context.Background(), first, decision); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AttachPaperOrderScoped(context.Background(), conflicting, decision.ID, uuid.New()); err == nil {
		t.Fatal("AttachPaperOrderScoped() accepted conflicting legacy strategy")
	}
}

func TestTradeDecisionJournalRecorderRestartRetryUsesPersistedParentScope(t *testing.T) {
	journal, replay := &decisionJournalStub{}, &replayEventStub{}
	journal.replay = replay
	run := domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	scope, _ := NewStrategyExecutionScope(uuid.New(), domain.AccountEnvironmentPaperScored, uuid.New(), run)
	decision := &domain.TradeDecision{ID: uuid.New(), MarketType: domain.MarketTypeStock, InstrumentKey: "AAPL", Status: domain.TradeDecisionStatusCandidate}
	first := NewTradeDecisionJournalRecorder(journal, replay).(ScopedDecisionRecorder)
	if err := first.RecordDecisionScoped(context.Background(), scope, decision); err != nil {
		t.Fatal(err)
	}
	restarted := NewTradeDecisionJournalRecorder(journal, replay).(ScopedDecisionRecorder)
	if err := restarted.AttachPaperOrderScoped(context.Background(), scope, decision.ID, uuid.New()); err != nil {
		t.Fatalf("restart retry error = %v", err)
	}
	last := replay.events[len(replay.events)-1]
	if last.AccountID != scope.AccountID() || last.Environment != scope.Environment() {
		t.Fatalf("restart replay scope = %+v", last)
	}
}

func TestTradeDecisionJournalRecorderOrderAttachmentIsCASIdempotent(t *testing.T) {
	journal, replay := &decisionJournalStub{}, &replayEventStub{}
	journal.replay = replay
	run := domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	scope, _ := NewStrategyExecutionScope(uuid.New(), domain.AccountEnvironmentPaperScored, uuid.New(), run)
	decision := &domain.TradeDecision{ID: uuid.New(), Status: domain.TradeDecisionStatusCandidate}
	recorder := NewTradeDecisionJournalRecorder(journal, replay).(ScopedDecisionRecorder)
	if err := recorder.RecordDecisionScoped(context.Background(), scope, decision); err != nil {
		t.Fatal(err)
	}
	orderID := uuid.New()
	if err := recorder.AttachPaperOrderScoped(context.Background(), scope, decision.ID, orderID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AttachPaperOrderScoped(context.Background(), scope, decision.ID, orderID); err != nil {
		t.Fatalf("same-order retry failed: %v", err)
	}
	if err := recorder.AttachPaperOrderScoped(context.Background(), scope, decision.ID, uuid.New()); err == nil {
		t.Fatal("different-order retry succeeded")
	}
	ordered := 0
	for _, event := range replay.events {
		if event.EventType == domain.ReplayEventTypePaperOrdered {
			ordered++
		}
	}
	if ordered != 1 {
		t.Fatalf("paper ordered replay events = %d, want 1", ordered)
	}
}

func TestTradeDecisionJournalRecorderAtomicFailureCannotOmitReplay(t *testing.T) {
	replay := &replayEventStub{}
	journal := &decisionJournalStub{replay: replay, failAtomic: true}
	run := domain.PipelineRunRef{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}
	scope, _ := NewStrategyExecutionScope(uuid.New(), domain.AccountEnvironmentPaperScored, uuid.New(), run)
	decision := &domain.TradeDecision{ID: uuid.New(), Status: domain.TradeDecisionStatusCandidate}
	recorder := NewTradeDecisionJournalRecorder(journal, replay).(ScopedDecisionRecorder)
	if err := recorder.RecordDecisionScoped(context.Background(), scope, decision); err != nil {
		t.Fatal(err)
	}
	orderID := uuid.New()
	if err := recorder.AttachPaperOrderScoped(context.Background(), scope, decision.ID, orderID); err == nil {
		t.Fatal("injected atomic failure succeeded")
	}
	if journal.stored[decision.ID].PaperOrderID != nil {
		t.Fatal("failed transaction left order attached")
	}
	if err := recorder.AttachPaperOrderScoped(context.Background(), scope, decision.ID, orderID); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if journal.stored[decision.ID].PaperOrderID == nil {
		t.Fatal("retry did not attach order")
	}
	ordered := 0
	for _, event := range replay.events {
		if event.EventType == domain.ReplayEventTypePaperOrdered {
			ordered++
		}
	}
	if ordered != 1 {
		t.Fatalf("paper ordered events = %d, want 1", ordered)
	}
}

func TestTradeDecisionJournalRecorderInitialReplayFailureStopsAndRetryRepairs(t *testing.T) {
	replay := &replayEventStub{}
	journal := &decisionJournalStub{replay: replay, failInitial: true}
	recorder := NewTradeDecisionJournalRecorder(journal, replay)
	decision := &domain.TradeDecision{ID: uuid.New(), Status: domain.TradeDecisionStatusCandidate}
	if err := recorder.RecordDecision(context.Background(), decision); err == nil {
		t.Fatal("injected initial replay failure succeeded")
	}
	if _, ok := journal.stored[decision.ID]; ok || len(replay.events) != 0 {
		t.Fatal("failed initial transaction left partial state")
	}
	if err := recorder.RecordDecision(context.Background(), decision); err != nil {
		t.Fatalf("restart retry: %v", err)
	}
	if len(replay.events) != 2 {
		t.Fatalf("initial replay events = %d, want 2", len(replay.events))
	}
}

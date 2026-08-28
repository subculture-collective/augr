package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// DecisionRecorder captures pre-order decisions and later attaches order IDs.
type DecisionRecorder interface {
	RecordDecision(ctx context.Context, decision *domain.TradeDecision) error
	AttachPaperOrder(ctx context.Context, decisionID, orderID uuid.UUID) error
	AttachLiveOrder(ctx context.Context, decisionID, orderID uuid.UUID) error
}

// ReplayDecisionRecorder extends decision persistence with ordered lifecycle
// evidence. Callers use this optional seam so non-durable test recorders remain
// compatible while the production recorder writes the replay ledger.
type ReplayDecisionRecorder interface {
	DecisionRecorder
	RecordReplayEvent(ctx context.Context, decisionID uuid.UUID, eventType domain.ReplayEventType, source string, payload any, occurredAt time.Time) error
}

// ScopedDecisionRecorder carries the authoritative execution scope across
// journal updates and replay writes.
type ScopedDecisionRecorder interface {
	DecisionRecorder
	RecordDecisionScoped(context.Context, ExecutionScope, *domain.TradeDecision) error
	AttachPaperOrderScoped(context.Context, ExecutionScope, uuid.UUID, uuid.UUID) error
	AttachLiveOrderScoped(context.Context, ExecutionScope, uuid.UUID, uuid.UUID) error
	RecordReplayEventScoped(context.Context, ExecutionScope, uuid.UUID, domain.ReplayEventType, string, any, time.Time) error
}

type AttachedOrderDecisionRecorder interface {
	ResolveAttachedOrderDecision(context.Context, ExecutionScope, uuid.UUID, bool) (uuid.UUID, error)
}

type RecoverableOrderDecisionRecorder interface {
	EnsureOrderDecisionAttachment(context.Context, ExecutionScope, *domain.TradeDecision, uuid.UUID, bool) (uuid.UUID, error)
}

type tradeDecisionJournalRecorder struct {
	repo       repository.TradeDecisionJournalRepository
	replayRepo repository.ReplayEventRepository
}

// NewTradeDecisionJournalRecorder adapts the Phase 2 repository to the execution seam.
func NewTradeDecisionJournalRecorder(repo repository.TradeDecisionJournalRepository, replayRepos ...repository.ReplayEventRepository) DecisionRecorder {
	if repo == nil {
		return nil
	}
	var replayRepo repository.ReplayEventRepository
	if len(replayRepos) > 0 {
		replayRepo = replayRepos[0]
	}
	return &tradeDecisionJournalRecorder{repo: repo, replayRepo: replayRepo}
}

func (r *tradeDecisionJournalRecorder) RecordDecision(ctx context.Context, decision *domain.TradeDecision) error {
	if r == nil || r.repo == nil || decision == nil {
		return nil
	}
	if r.replayRepo == nil {
		return r.repo.Create(ctx, decision)
	}
	atomic, ok := r.repo.(repository.AtomicDecisionReplayRepository)
	if !ok {
		return fmt.Errorf("decision recorder: atomic initial replay repository is required")
	}
	return atomic.CreateWithInitialReplay(ctx, decision)
}

func (r *tradeDecisionJournalRecorder) RecordDecisionScoped(ctx context.Context, scope ExecutionScope, decision *domain.TradeDecision) error {
	if decision == nil {
		return nil
	}
	if err := bindTradeDecisionScope(scope, decision); err != nil {
		return err
	}
	return r.RecordDecision(ctx, decision)
}

func (r *tradeDecisionJournalRecorder) AttachPaperOrder(ctx context.Context, decisionID, orderID uuid.UUID) error {
	if r == nil || r.repo == nil {
		return nil
	}
	if r.replayRepo != nil {
		atomic, ok := r.repo.(repository.AtomicOrderReplayRepository)
		if !ok {
			return fmt.Errorf("decision recorder: atomic order replay repository is required")
		}
		return atomic.AttachOrderWithReplay(ctx, decisionID, orderID, false, "order_manager", time.Now().UTC())
	}
	applied, err := r.repo.AttachPaperOrder(ctx, decisionID, orderID)
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	return r.RecordReplayEvent(ctx, decisionID, domain.ReplayEventTypePaperOrdered, "order_manager", map[string]any{"order_id": orderID}, time.Now().UTC())
}

func (r *tradeDecisionJournalRecorder) AttachPaperOrderScoped(ctx context.Context, scope ExecutionScope, decisionID, orderID uuid.UUID) error {
	if _, err := r.persistedDecisionScope(ctx, decisionID, &scope); err != nil {
		return err
	}
	if r.replayRepo != nil {
		atomic, ok := r.repo.(repository.ScopedOrderReplayRepository)
		if !ok {
			return fmt.Errorf("decision recorder: scoped atomic order replay repository is required")
		}
		return atomic.AttachOrderWithReplayScoped(ctx, decisionID, orderID, false, "order_manager", time.Now().UTC(), decisionOrderAttachmentScope(scope))
	}
	scoped, ok := r.repo.(repository.ScopedDecisionOrderRepository)
	if !ok {
		return fmt.Errorf("decision recorder: scoped order repository is required")
	}
	_, err := scoped.AttachPaperOrderScoped(ctx, decisionID, orderID, decisionOrderAttachmentScope(scope))
	return err
}

func (r *tradeDecisionJournalRecorder) AttachLiveOrder(ctx context.Context, decisionID, orderID uuid.UUID) error {
	if r == nil || r.repo == nil {
		return nil
	}
	if r.replayRepo != nil {
		atomic, ok := r.repo.(repository.AtomicOrderReplayRepository)
		if !ok {
			return fmt.Errorf("decision recorder: atomic order replay repository is required")
		}
		return atomic.AttachOrderWithReplay(ctx, decisionID, orderID, true, "order_manager", time.Now().UTC())
	}
	applied, err := r.repo.AttachLiveOrder(ctx, decisionID, orderID)
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	return r.RecordReplayEvent(ctx, decisionID, domain.ReplayEventTypeLiveOrdered, "order_manager", map[string]any{"order_id": orderID}, time.Now().UTC())
}

func (r *tradeDecisionJournalRecorder) AttachLiveOrderScoped(ctx context.Context, scope ExecutionScope, decisionID, orderID uuid.UUID) error {
	if _, err := r.persistedDecisionScope(ctx, decisionID, &scope); err != nil {
		return err
	}
	if r.replayRepo != nil {
		atomic, ok := r.repo.(repository.ScopedOrderReplayRepository)
		if !ok {
			return fmt.Errorf("decision recorder: scoped atomic order replay repository is required")
		}
		return atomic.AttachOrderWithReplayScoped(ctx, decisionID, orderID, true, "order_manager", time.Now().UTC(), decisionOrderAttachmentScope(scope))
	}
	scoped, ok := r.repo.(repository.ScopedDecisionOrderRepository)
	if !ok {
		return fmt.Errorf("decision recorder: scoped order repository is required")
	}
	_, err := scoped.AttachLiveOrderScoped(ctx, decisionID, orderID, decisionOrderAttachmentScope(scope))
	return err
}

func (r *tradeDecisionJournalRecorder) RecordReplayEvent(ctx context.Context, decisionID uuid.UUID, eventType domain.ReplayEventType, source string, payload any, occurredAt time.Time) error {
	if r == nil || r.replayRepo == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("decision recorder: marshal %s replay payload: %w", eventType, err)
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	decision, err := r.persistedDecisionScope(ctx, decisionID, nil)
	if err != nil {
		return err
	}
	return r.replayRepo.CreateReplayEvent(ctx, &domain.ReplayEvent{
		AccountID: decision.AccountID, Environment: decision.Environment,
		OriginType: decision.OriginType, OriginID: decision.OriginID,
		TradeDecisionID: decisionID, EventType: eventType, Source: source,
		Payload: raw, OccurredAt: occurredAt,
	})
}

func (r *tradeDecisionJournalRecorder) RecordReplayEventScoped(ctx context.Context, scope ExecutionScope, decisionID uuid.UUID, eventType domain.ReplayEventType, source string, payload any, occurredAt time.Time) error {
	if _, err := r.persistedDecisionScope(ctx, decisionID, &scope); err != nil {
		return err
	}
	return r.RecordReplayEvent(ctx, decisionID, eventType, source, payload, occurredAt)
}

func (r *tradeDecisionJournalRecorder) ResolveAttachedOrderDecision(ctx context.Context, scope ExecutionScope, orderID uuid.UUID, live bool) (uuid.UUID, error) {
	repo, ok := r.repo.(repository.AttachedOrderDecisionRepository)
	if !ok {
		return uuid.Nil, fmt.Errorf("decision recorder: attached order decision repository is required")
	}
	decision, err := repo.GetByOrderScoped(ctx, orderID, live, decisionOrderAttachmentScope(scope))
	if err != nil {
		return uuid.Nil, fmt.Errorf("decision recorder: resolve attached order decision: %w", err)
	}
	if decision == nil || decision.ID == uuid.Nil || !tradeDecisionMatchesScope(*decision, scope) {
		return uuid.Nil, fmt.Errorf("decision recorder: attached order decision scope is invalid")
	}
	return decision.ID, nil
}

func (r *tradeDecisionJournalRecorder) EnsureOrderDecisionAttachment(ctx context.Context, scope ExecutionScope, decision *domain.TradeDecision, orderID uuid.UUID, live bool) (uuid.UUID, error) {
	decisionID, err := r.ResolveAttachedOrderDecision(ctx, scope, orderID, live)
	if err == nil {
		return decisionID, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return uuid.Nil, err
	}
	if decision == nil || decision.ID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("decision recorder: recovered decision intent is required")
	}
	persisted, getErr := r.repo.Get(ctx, decision.ID)
	if getErr == nil {
		if persisted == nil || !tradeDecisionMatchesScope(*persisted, scope) {
			return uuid.Nil, fmt.Errorf("decision recorder: recovered decision intent scope is invalid")
		}
		decision = persisted
	} else if errors.Is(getErr, repository.ErrNotFound) {
		if err := r.RecordDecisionScoped(ctx, scope, decision); err != nil {
			return uuid.Nil, fmt.Errorf("decision recorder: recover persisted decision intent: %w", err)
		}
	} else {
		return uuid.Nil, fmt.Errorf("decision recorder: load recovered decision intent: %w", getErr)
	}
	if live {
		err = r.AttachLiveOrderScoped(ctx, scope, decision.ID, orderID)
	} else {
		err = r.AttachPaperOrderScoped(ctx, scope, decision.ID, orderID)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("decision recorder: recover order attachment: %w", err)
	}
	return r.ResolveAttachedOrderDecision(ctx, scope, orderID, live)
}

func (r *tradeDecisionJournalRecorder) persistedDecisionScope(ctx context.Context, decisionID uuid.UUID, supplied *ExecutionScope) (*domain.TradeDecision, error) {
	if r == nil || r.repo == nil {
		return nil, fmt.Errorf("decision recorder: journal repository is required")
	}
	decision, err := r.repo.Get(ctx, decisionID)
	if err != nil {
		return nil, fmt.Errorf("decision recorder: load persisted decision: %w", err)
	}
	if decision == nil {
		return nil, fmt.Errorf("decision recorder: persisted decision is missing")
	}
	if supplied != nil && !tradeDecisionMatchesScope(*decision, *supplied) {
		return nil, fmt.Errorf("decision recorder: supplied execution scope conflicts with persisted decision")
	}
	return decision, nil
}

func bindTradeDecisionScope(scope ExecutionScope, decision *domain.TradeDecision) error {
	if decision == nil {
		return fmt.Errorf("decision recorder: decision is required")
	}
	originType, originID := scope.Origin()
	run, hasRun := scope.PipelineRun()
	if scope.AccountID() == uuid.Nil || !scope.Environment().IsValid() || originID == "" {
		return fmt.Errorf("decision recorder: complete execution scope is required")
	}
	if (decision.PipelineRunID == nil) != (decision.PipelineRunTradeDate == nil) {
		return fmt.Errorf("decision recorder: decision scope conflicts with execution scope")
	}
	if decision.AccountID != uuid.Nil && decision.AccountID != scope.AccountID() ||
		decision.Environment != "" && decision.Environment != scope.Environment() ||
		decision.OriginType != "" && decision.OriginType != string(originType) ||
		decision.OriginID != "" && decision.OriginID != originID ||
		decision.PipelineRunID != nil && (!hasRun || *decision.PipelineRunID != run.ID) ||
		decision.PipelineRunTradeDate != nil && (!hasRun || !decision.PipelineRunTradeDate.Equal(run.TradeDate)) {
		return fmt.Errorf("decision recorder: decision scope conflicts with execution scope")
	}
	decision.AccountID, decision.Environment = scope.AccountID(), scope.Environment()
	decision.OriginType, decision.OriginID = string(originType), originID
	if hasRun {
		decision.PipelineRunID = &run.ID
		tradeDate := run.TradeDate
		decision.PipelineRunTradeDate = &tradeDate
	}
	if strategyID := scope.LegacyStrategyID(); strategyID != nil {
		if decision.StrategyID != nil && *decision.StrategyID != *strategyID {
			return fmt.Errorf("decision recorder: decision strategy conflicts with execution scope")
		}
		decision.StrategyID = strategyID
	}
	return nil
}

func tradeDecisionMatchesScope(decision domain.TradeDecision, scope ExecutionScope) bool {
	originType, originID := scope.Origin()
	run, ok := scope.PipelineRun()
	legacyStrategyID := scope.LegacyStrategyID()
	lineageMatches := !ok && decision.PipelineRunID == nil && decision.PipelineRunTradeDate == nil ||
		ok && decision.PipelineRunID != nil && *decision.PipelineRunID == run.ID &&
			decision.PipelineRunTradeDate != nil && decision.PipelineRunTradeDate.Equal(run.TradeDate)
	return lineageMatches && decision.AccountID == scope.AccountID() && decision.Environment == scope.Environment() &&
		decision.OriginType == string(originType) && decision.OriginID == originID &&
		uuidPointersEqual(decision.StrategyID, legacyStrategyID)
}

func decisionOrderAttachmentScope(scope ExecutionScope) repository.DecisionOrderAttachmentScope {
	result := repository.DecisionOrderAttachmentScope{StrategyID: scope.LegacyStrategyID()}
	if run, ok := scope.PipelineRun(); ok {
		result.PipelineRunID = &run.ID
		tradeDate := run.TradeDate
		result.PipelineRunTradeDate = &tradeDate
	}
	if copyRunID := scope.CopyOriginRunID(); copyRunID != uuid.Nil {
		result.CopyOriginRebalanceRunID = &copyRunID
	}
	return result
}

func uuidPointersEqual(left, right *uuid.UUID) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

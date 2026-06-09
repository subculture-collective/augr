package execution

import (
	"context"

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

type tradeDecisionJournalRecorder struct {
	repo repository.TradeDecisionJournalRepository
}

// NewTradeDecisionJournalRecorder adapts the Phase 2 repository to the execution seam.
func NewTradeDecisionJournalRecorder(repo repository.TradeDecisionJournalRepository) DecisionRecorder {
	if repo == nil {
		return nil
	}
	return &tradeDecisionJournalRecorder{repo: repo}
}

func (r *tradeDecisionJournalRecorder) RecordDecision(ctx context.Context, decision *domain.TradeDecision) error {
	if r == nil || r.repo == nil || decision == nil {
		return nil
	}
	return r.repo.Create(ctx, decision)
}

func (r *tradeDecisionJournalRecorder) AttachPaperOrder(ctx context.Context, decisionID, orderID uuid.UUID) error {
	if r == nil || r.repo == nil {
		return nil
	}
	return r.repo.AttachPaperOrder(ctx, decisionID, orderID)
}

func (r *tradeDecisionJournalRecorder) AttachLiveOrder(ctx context.Context, decisionID, orderID uuid.UUID) error {
	if r == nil || r.repo == nil {
		return nil
	}
	return r.repo.AttachLiveOrder(ctx, decisionID, orderID)
}

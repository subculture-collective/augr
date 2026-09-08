package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestRepositoriesRejectUnpairedPipelineRunRefsBeforeWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := uuid.New()
	date := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		call func() error
	}{
		{"order ID only", func() error { return NewOrderRepo(nil, uuid.New()).Create(ctx, &domain.Order{PipelineRunID: &id}) }},
		{"order date only", func() error {
			return NewOrderRepo(nil, uuid.New()).Create(ctx, &domain.Order{PipelineRunTradeDate: &date})
		}},
		{"opportunity ID only", func() error {
			return NewOpportunityRepo(nil, uuid.New()).Create(ctx, &domain.Opportunity{PipelineRunID: &id})
		}},
		{"memory date only", func() error {
			return NewMemoryRepo(nil, uuid.New()).Create(ctx, &domain.AgentMemory{PipelineRunTradeDate: &date})
		}},
		{"event ID only", func() error {
			return NewAgentEventRepo(nil, uuid.New()).Create(ctx, &domain.AgentEvent{PipelineRunID: &id})
		}},
		{"decision date only", func() error {
			return NewTradeDecisionJournalRepo(nil, uuid.New()).Create(ctx, &domain.TradeDecision{PipelineRunTradeDate: &date})
		}},
		{"conversation ID only", func() error {
			return NewConversationRepo(nil, uuid.New()).CreateConversation(ctx, &domain.Conversation{PipelineRunID: id})
		}},
		{"conversation date only", func() error {
			return NewConversationRepo(nil, uuid.New()).CreateConversation(ctx, &domain.Conversation{PipelineRunTradeDate: date})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil || !strings.Contains(err.Error(), "must both be set or both be absent") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestValidateOptionalPipelineRunRefRejectsZeroValues(t *testing.T) {
	zeroID := uuid.Nil
	zeroDate := time.Time{}
	if err := validateOptionalPipelineRunRef(&zeroID, &zeroDate); err == nil {
		t.Fatal("expected zero-valued pair to be rejected")
	}
}

func TestValidatePipelineRunRefRejectsZeroValues(t *testing.T) {
	if err := validatePipelineRunRef(uuid.Nil, time.Time{}); err == nil {
		t.Fatal("expected zero-valued required pair to be rejected")
	}
}

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestNormalizePolymarketStrategySide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "yes", input: "yes", want: "YES"},
		{name: "no", input: "NO", want: "NO"},
		{name: "up", input: "up", want: "Up"},
		{name: "down", input: "Down", want: "Down"},
		{name: "over", input: "OVER", want: "Over"},
		{name: "under", input: "under", want: "Under"},
		{name: "blank", input: "", wantErr: true},
		{name: "invalid", input: "sideways", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizePolymarketStrategySide(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizePolymarketStrategySide(%q) error = nil, want error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePolymarketStrategySide(%q) error = %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("normalizePolymarketStrategySide(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExecutionDecisionMetadata_PreservesZeroCostWithLLMProvenance(t *testing.T) {
	t.Parallel()

	runID := uuid.New()
	promptTokens := 12
	completionTokens := 3
	latencyMS := 456
	decisionRepo := &stubAgentDecisionRepository{decisions: []domain.AgentDecision{{
		PipelineRunID:    runID,
		AgentRole:        domain.AgentRoleTrader,
		Phase:            domain.PhaseTrading,
		PromptText:       " system: preserve exact prompt \n",
		LLMProvider:      " openai ",
		LLMModel:         " gpt-4.1 ",
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		LatencyMS:        latencyMS,
		CostUSD:          0,
	}}}

	got := executionDecisionMetadata(context.Background(), decisionRepo, slog.Default(), runID)
	if got == nil {
		t.Fatal("executionDecisionMetadata() = nil, want metadata")
	}
	if got.PromptText != " system: preserve exact prompt \n" {
		t.Fatalf("PromptText = %q, want exact prompt", got.PromptText)
	}
	if got.LLMProvider != " openai " || got.LLMModel != " gpt-4.1 " {
		t.Fatalf("LLM strings = %+v, want exact preserved values", got)
	}
	if got.PromptTokens == nil || *got.PromptTokens != promptTokens {
		t.Fatalf("PromptTokens = %v, want %d", got.PromptTokens, promptTokens)
	}
	if got.CompletionTokens == nil || *got.CompletionTokens != completionTokens {
		t.Fatalf("CompletionTokens = %v, want %d", got.CompletionTokens, completionTokens)
	}
	if got.LatencyMS == nil || *got.LatencyMS != latencyMS {
		t.Fatalf("LatencyMS = %v, want %d", got.LatencyMS, latencyMS)
	}
	if got.CostUSD == nil || *got.CostUSD != 0 {
		t.Fatalf("CostUSD = %v, want 0", got.CostUSD)
	}
}

func TestExecutionDecisionMetadata_OmitsDeterministicDecision(t *testing.T) {
	t.Parallel()

	runID := uuid.New()
	decisionRepo := &stubAgentDecisionRepository{decisions: []domain.AgentDecision{{
		PipelineRunID: runID,
		AgentRole:     domain.AgentRoleTrader,
		Phase:         domain.PhaseTrading,
		CostUSD:       0.25,
	}}}

	if got := executionDecisionMetadata(context.Background(), decisionRepo, slog.Default(), runID); got != nil {
		t.Fatalf("executionDecisionMetadata() = %+v, want nil", got)
	}
}

type stubAgentDecisionRepository struct {
	decisions []domain.AgentDecision
	err       error
}

func (r *stubAgentDecisionRepository) Create(context.Context, *domain.AgentDecision) error {
	return nil
}

func (r *stubAgentDecisionRepository) GetByRun(context.Context, uuid.UUID, repository.AgentDecisionFilter, int, int) ([]domain.AgentDecision, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.decisions, nil
}

func (r *stubAgentDecisionRepository) CountByRun(context.Context, uuid.UUID, repository.AgentDecisionFilter) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	return len(r.decisions), nil
}

var _ repository.AgentDecisionRepository = (*stubAgentDecisionRepository)(nil)

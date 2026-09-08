package generativestrategy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/llm"
)

type proposalEvidenceSourceStub struct {
	items []ProposalEvidence
	err   error
}

func (stub proposalEvidenceSourceStub) ListEligibleGeneratedProposalEvidence(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) ([]ProposalEvidence, error) {
	return stub.items, stub.err
}

func TestModelProposalSourceBindsImmutableEvidenceToTrustedAuthority(t *testing.T) {
	family, input := specFixture(t)
	draft := modelProposalEnvelope{
		SpecKey: input.SpecKey, Inputs: input.Inputs, Entry: input.Entry, Exit: input.Exit, Sizing: input.Sizing,
		MaximumHoldingSeconds: input.MaximumHoldingSeconds, Costs: input.Costs, Capacity: input.Capacity, ExampleTests: input.ExampleTests, Retirement: input.Retirement,
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	provider := llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Content: string(raw), Model: "reviewed-model"}, nil
	})
	source, err := NewModelProposalSource(
		proposalEvidenceSourceStub{items: []ProposalEvidence{{
			Key: input.SpecKey, Universe: input.Universe, AllowedDataFields: allowedDataFields(input.Inputs), ImmutableSummary: "exact immutable scope summary",
		}}}, family, "openai", provider, "requested-model", strings.Repeat("a", 40), strings.Repeat("b", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	items, err := source.ListEligibleGeneratedProposals(context.Background(), uuid.New(), uuid.New(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Key != input.SpecKey || items[0].Request.Input.Family.ID() != family.ID() || items[0].Request.SourceCommit != strings.Repeat("a", 40) {
		t.Fatalf("items=%+v", items)
	}
}

func TestModelProposalSourceFailsClosedOnDuplicateOrOversizedEvidence(t *testing.T) {
	family, input := specFixture(t)
	provider := llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })
	valid := ProposalEvidence{Key: input.SpecKey, Universe: input.Universe, AllowedDataFields: allowedDataFields(input.Inputs), ImmutableSummary: "exact immutable scope summary"}
	for name, test := range map[string]struct {
		items []ProposalEvidence
		limit int
	}{
		"duplicate": {[]ProposalEvidence{valid, valid}, 2},
		"oversized": {[]ProposalEvidence{valid, valid}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			source, err := NewModelProposalSource(proposalEvidenceSourceStub{items: test.items}, family, "openai", provider, "model", strings.Repeat("a", 40), strings.Repeat("b", 64))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.ListEligibleGeneratedProposals(context.Background(), uuid.New(), uuid.New(), test.limit); err == nil {
				t.Fatal("ListEligibleGeneratedProposals() error = nil")
			}
		})
	}
}

func TestReviewedDailyStockFamilyIsStable(t *testing.T) {
	first, err := ReviewedDailyStockFamily()
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReviewedDailyStockFamily()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() || first.Digest() != second.Digest() || first.Slug() != "generated-daily-stock-v1" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestReviewedDailyStockSpecKeyBindsOneInstrument(t *testing.T) {
	scopeID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	instrumentID := uuid.MustParse("20000000-0000-4000-8000-000000000002")
	first, err := ReviewedDailyStockSpecKey(scopeID, instrumentID)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := ReviewedDailyStockSpecKey(scopeID, instrumentID)
	if first != second || len(first) != len("daily_stock_")+32 {
		t.Fatalf("ReviewedDailyStockSpecKey() = %q, %q", first, second)
	}
	if other, _ := ReviewedDailyStockSpecKey(scopeID, uuid.New()); other == first {
		t.Fatal("instrument identity did not affect reviewed key")
	}
}

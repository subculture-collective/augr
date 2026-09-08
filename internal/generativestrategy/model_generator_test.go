package generativestrategy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/llm"
)

func TestModelProposalGeneratorBindsTrustedAuthorityAndProvenance(t *testing.T) {
	family, input := specFixture(t)
	draft := modelProposalEnvelope{
		SpecKey: input.SpecKey, Inputs: input.Inputs, Entry: input.Entry, Exit: input.Exit, Sizing: input.Sizing,
		MaximumHoldingSeconds: input.MaximumHoldingSeconds, Costs: input.Costs, Capacity: input.Capacity, ExampleTests: input.ExampleTests, Retirement: input.Retirement,
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	var captured llm.CompletionRequest
	provider := llm.ProviderFunc(func(_ context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
		captured = request
		return &llm.CompletionResponse{Content: string(raw), Model: "reviewed-model", Usage: llm.CompletionUsage{PromptTokens: 12, CompletionTokens: 34}, CostUSD: 0.125}, nil
	})
	request, err := (ModelProposalGenerator{}).Generate(context.Background(), ModelProposalRequest{
		Family: family, Universe: input.Universe, SpecKey: input.SpecKey, AllowedDataFields: allowedDataFields(input.Inputs), ProviderName: "openai", Provider: provider, Model: "requested-model", ImmutableSummary: "Manifest abc contains reviewed daily AAPL bars.",
		SourceCommit: strings.Repeat("b", 40), SourceTreeSHA256: strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NewSpec(request.Input)
	if err != nil {
		t.Fatal(err)
	}
	if spec.FamilyID() != family.ID() || request.Input.Universe.Benchmark != input.Universe.Benchmark || request.Input.Authoring.Model != "reviewed-model" ||
		request.Input.Authoring.Cost != "0.125" || len(request.Input.ProhibitedBehaviors) != len(requiredProhibitions) || captured.Temperature != 0 ||
		captured.ResponseFormat == nil || captured.ResponseFormat.Type != llm.ResponseFormatJSONObject {
		t.Fatalf("request=%+v input=%+v", captured, request.Input)
	}
}

func TestModelProposalGeneratorRejectsUnknownAuthorityFields(t *testing.T) {
	family, input := specFixture(t)
	provider := llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Content: `{"spec_key":"x","deployment":{"live":true}}`}, nil
	})
	request, err := (ModelProposalGenerator{}).Generate(context.Background(), ModelProposalRequest{
		Family: family, Universe: input.Universe, SpecKey: input.SpecKey, AllowedDataFields: allowedDataFields(input.Inputs), ProviderName: "openai", Provider: provider, Model: "model", ImmutableSummary: "immutable evidence",
	})
	if err == nil || request.Input.Family != nil {
		t.Fatalf("request=%+v err=%v", request, err)
	}
}

func TestModelProposalGeneratorRejectsChangedTrustedSpecKey(t *testing.T) {
	family, input := specFixture(t)
	provider := llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Content: `{"spec_key":"model_selected_key"}`}, nil
	})
	request, err := (ModelProposalGenerator{}).Generate(context.Background(), ModelProposalRequest{
		Family: family, Universe: input.Universe, SpecKey: input.SpecKey, AllowedDataFields: allowedDataFields(input.Inputs), ProviderName: "openai", Provider: provider, Model: "model", ImmutableSummary: "immutable evidence",
	})
	if err == nil || !strings.Contains(err.Error(), "changed trusted spec key") || request.Input.Family != nil {
		t.Fatalf("request=%+v err=%v", request, err)
	}
}

func TestModelProposalGeneratorRejectsUnavailableImmutableField(t *testing.T) {
	family, input := specFixture(t)
	allowed := allowedDataFields(input.Inputs)
	draft := modelProposalEnvelope{
		SpecKey: input.SpecKey, Inputs: input.Inputs, Entry: input.Entry, Exit: input.Exit, Sizing: input.Sizing,
		MaximumHoldingSeconds: input.MaximumHoldingSeconds, Costs: input.Costs, Capacity: input.Capacity, ExampleTests: input.ExampleTests, Retirement: input.Retirement,
	}
	draft.Inputs[0].DatasetKind = "fundamentals"
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	provider := llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Content: string(raw)}, nil
	})
	request, err := (ModelProposalGenerator{}).Generate(context.Background(), ModelProposalRequest{
		Family: family, Universe: input.Universe, SpecKey: input.SpecKey, AllowedDataFields: allowed, ProviderName: "openai", Provider: provider, Model: "model", ImmutableSummary: "immutable evidence",
	})
	if err == nil || !strings.Contains(err.Error(), "unavailable immutable data field") || request.Input.Family != nil {
		t.Fatalf("request=%+v err=%v", request, err)
	}
}

func allowedDataFields(inputs []InputField) []AllowedDataField {
	fields := make([]AllowedDataField, 0, len(inputs))
	for _, input := range inputs {
		fields = append(fields, AllowedDataField{DatasetKind: input.DatasetKind, Field: input.Field, Type: input.Type})
	}
	return fields
}

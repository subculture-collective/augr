package generativestrategy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	llmparse "github.com/PatrickFanella/get-rich-quick/internal/llm/parse"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

const typedProposalSystemPrompt = `Return one constrained stock strategy proposal as a JSON object. Use only the supplied immutable-data fields. Decimal values must be canonical JSON strings. The object must contain: spec_key, inputs, entry, exit, sizing, maximum_holding_seconds, costs, capacity, example_tests, and retirement. Expressions use op/ref/value/args and only ref, decimal, boolean, add, sub, mul, div, lt, lte, gt, gte, eq, and, or, not. Every input must use missing_policy "abstain". Do not include account, deployment, schedule, promotion, order, credential, network, or live-trading fields.`

type ModelProposalRequest struct {
	Family           *strategycatalog.Family
	Universe         Universe
	SpecKey          string
	ProviderName     string
	Provider         llm.Provider
	Model            string
	ImmutableSummary string
	SourceCommit     string
	SourceTreeSHA256 string
}

type modelProposalEnvelope struct {
	SpecKey               string        `json:"spec_key"`
	Inputs                []InputField  `json:"inputs"`
	Entry                 Expr          `json:"entry"`
	Exit                  Expr          `json:"exit"`
	Sizing                Sizing        `json:"sizing"`
	MaximumHoldingSeconds int64         `json:"maximum_holding_seconds"`
	Costs                 Costs         `json:"costs"`
	Capacity              Capacity      `json:"capacity"`
	ExampleTests          []ExampleTest `json:"example_tests"`
	Retirement            Retirement    `json:"retirement"`
}

type ModelProposalGenerator struct{}

// Generate accepts model output only as an untrusted draft. Account, family,
// universe, authority prohibitions, source identity, and authoring provenance
// are supplied by trusted runtime inputs before the normal Spec validator runs.
func (ModelProposalGenerator) Generate(ctx context.Context, request ModelProposalRequest) (ProposalRequest, error) {
	if request.Family == nil || request.Provider == nil || !tokenPattern.MatchString(request.SpecKey) || !tokenPattern.MatchString(request.ProviderName) || strings.TrimSpace(request.Model) == "" ||
		strings.TrimSpace(request.ImmutableSummary) == "" {
		return ProposalRequest{}, fmt.Errorf("typed model proposal requires family, provider, model, and immutable summary")
	}
	userPrompt := strings.TrimSpace(request.ImmutableSummary)
	response, err := request.Provider.Complete(ctx, llm.CompletionRequest{
		Model: request.Model, Messages: []llm.Message{{Role: "system", Content: typedProposalSystemPrompt}, {Role: "user", Content: userPrompt}},
		Temperature: 0, ResponseFormat: &llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
	})
	if err != nil {
		return ProposalRequest{}, fmt.Errorf("generate typed strategy proposal: %w", err)
	}
	if response == nil || strings.TrimSpace(response.Content) == "" || response.Usage.PromptTokens < 0 || response.Usage.CompletionTokens < 0 || response.CostUSD < 0 {
		return ProposalRequest{}, fmt.Errorf("typed strategy proposal response is invalid")
	}
	object, err := llmparse.ExtractJSONObject(response.Content)
	if err != nil {
		return ProposalRequest{}, fmt.Errorf("extract typed strategy proposal: %w", err)
	}
	var draft modelProposalEnvelope
	decoder := json.NewDecoder(bytes.NewBufferString(object))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&draft); err != nil {
		return ProposalRequest{}, fmt.Errorf("decode typed strategy proposal: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ProposalRequest{}, fmt.Errorf("typed strategy proposal has trailing content")
	}
	if draft.SpecKey != request.SpecKey {
		return ProposalRequest{}, fmt.Errorf("typed strategy proposal changed trusted spec key")
	}
	promptDigest := sha256.Sum256([]byte(typedProposalSystemPrompt + "\x00" + userPrompt))
	model := strings.TrimSpace(response.Model)
	if model == "" {
		model = strings.TrimSpace(request.Model)
	}
	input := SpecInput{
		Family: request.Family, SpecKey: draft.SpecKey, Inputs: draft.Inputs, Universe: request.Universe, Entry: draft.Entry, Exit: draft.Exit,
		Sizing: draft.Sizing, MaximumHoldingSeconds: draft.MaximumHoldingSeconds, Costs: draft.Costs, Capacity: draft.Capacity,
		ProhibitedBehaviors: append([]string(nil), requiredProhibitions...), PropertyTests: append([]string(nil), requiredProperties...), ExampleTests: draft.ExampleTests,
		Retirement: draft.Retirement, Authoring: Authoring{
			Provider: request.ProviderName, Model: model, PromptSHA256: hex.EncodeToString(promptDigest[:]), InputTokens: int64(response.Usage.PromptTokens),
			OutputTokens: int64(response.Usage.CompletionTokens), Currency: "USD", Cost: strconv.FormatFloat(response.CostUSD, 'f', -1, 64),
		},
	}
	if _, err := NewSpec(input); err != nil {
		return ProposalRequest{}, fmt.Errorf("validate typed strategy proposal: %w", err)
	}
	return ProposalRequest{Input: input, SourceCommit: request.SourceCommit, SourceTreeSHA256: request.SourceTreeSHA256}, nil
}

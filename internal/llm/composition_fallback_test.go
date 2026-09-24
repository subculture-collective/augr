package llm_test

import (
	"context"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
)

func TestComposerFallbackLoopPicksAnotherCredentialedProvider(t *testing.T) {
	t.Parallel()
	var built []string
	ok := func(name string) llm.Provider {
		built = append(built, name)
		return llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
			return &llm.CompletionResponse{Content: name}, nil
		})
	}
	composer := llm.NewComposer(llm.RuntimeProviderFactories{
		OpenCode:  func(llm.OpenCodeProviderConfig) (llm.Provider, error) { return ok("opencode"), nil },
		OpenAI:    func(llm.OpenAIProviderConfig) (llm.Provider, error) { return ok("openai"), nil },
		Anthropic: func(llm.AnthropicProviderConfig) (llm.Provider, error) { return ok("anthropic"), nil },
	})
	cfg := config.LLMConfig{
		DefaultProvider: "opencode", FallbackProvider: "opencode", ThrottleConcurrency: 1,
		Providers: config.LLMProviderConfigs{
			OpenCode:  config.OpenCodeConfig{BaseURL: "http://opencode:4096", Password: "secret", Model: "openai/gpt-5.6-terra"},
			Anthropic: config.LLMProviderConfig{APIKey: "sk-ant"},
		},
	}
	if composer.BuildProvider(cfg, nil, discardLogger(), nil) == nil {
		t.Fatal("BuildProvider() = nil")
	}
	if len(built) != 2 || built[0] != "opencode" || built[1] != "anthropic" {
		t.Fatalf("built providers = %v, want opencode primary then anthropic fallback (openai lacks a key)", built)
	}
}

func TestComposerFallbackLoopWithoutAlternativesDisablesFallback(t *testing.T) {
	t.Parallel()
	var built int
	composer := llm.NewComposer(llm.RuntimeProviderFactories{
		OpenCode: func(llm.OpenCodeProviderConfig) (llm.Provider, error) {
			built++
			return llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
				return &llm.CompletionResponse{Content: "x"}, nil
			}), nil
		},
	})
	cfg := config.LLMConfig{DefaultProvider: "opencode", FallbackProvider: "opencode", ThrottleConcurrency: 1}
	if composer.BuildProvider(cfg, nil, discardLogger(), nil) == nil {
		t.Fatal("BuildProvider() = nil")
	}
	if built != 1 {
		t.Fatalf("providers built = %d, want primary only", built)
	}
}

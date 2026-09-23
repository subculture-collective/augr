package llm

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
)

// OpenAIProviderConfig is the config shape required by an OpenAI-compatible factory.
type OpenAIProviderConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// AnthropicProviderConfig is the config shape required by an Anthropic factory.
type AnthropicProviderConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// GoogleProviderConfig is the config shape required by a Google factory.
type GoogleProviderConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// OllamaProviderConfig is the config shape required by an Ollama factory.
type OllamaProviderConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// OpenCodeProviderConfig is the config shape required by an OpenCode factory.
type OpenCodeProviderConfig struct {
	BaseURL         string
	Username        string
	Password        string
	Model           string
	UseRequestModel bool
}

// RuntimeProviderFactories provides provider constructors used by the runtime composer.
type RuntimeProviderFactories struct {
	OpenAI     func(OpenAIProviderConfig) (Provider, error)
	Anthropic  func(AnthropicProviderConfig) (Provider, error)
	Google     func(GoogleProviderConfig) (Provider, error)
	OpenRouter func(OpenAIProviderConfig) (Provider, error)
	XAI        func(OpenAIProviderConfig) (Provider, error)
	Ollama     func(OllamaProviderConfig) (Provider, error)
	OpenCode   func(OpenCodeProviderConfig) (Provider, error)
}

// Composer owns runtime LLM provider assembly.
type Composer struct {
	factories RuntimeProviderFactories
}

// NewComposer constructs a runtime composer from provider factories.
func NewComposer(factories RuntimeProviderFactories) Composer {
	return Composer{factories: factories}
}

// BuildProvider builds the configured primary provider and wraps it with the runtime chain.
func (c Composer) BuildProvider(cfg config.LLMConfig, appMetrics any, logger *slog.Logger, budget *Budget) Provider {
	primary := c.newProviderFromConfig(cfg, logger)
	if primary == nil {
		return nil
	}
	return c.WrapProviderChain(primary, cfg, appMetrics, logger, budget)
}

// WrapProviderChain wraps an existing provider with runtime chain layers.
func (c Composer) WrapProviderChain(primary Provider, cfg config.LLMConfig, appMetrics any, logger *slog.Logger, budget *Budget) Provider {
	if primary == nil {
		return nil
	}
	return NewProviderChain(primary, logger, c.chainOptions(cfg, appMetrics, logger, budget)...)
}

// BuildProviderForSelection resolves and constructs a provider for a requested provider name.
func (c Composer) BuildProviderForSelection(cfg config.LLMConfig, providerName, model string, logger *slog.Logger) (Provider, error) {
	return c.buildProviderForSelection(cfg, providerName, model, logger, true)
}

func (c Composer) buildProviderForSelection(cfg config.LLMConfig, providerName, model string, logger *slog.Logger, useRequestModel bool) (Provider, error) {
	_ = logger

	providerName = strings.ToLower(strings.TrimSpace(providerName))
	resolveModel := func(providerModel string) string {
		if m := strings.TrimSpace(model); m != "" {
			return m
		}
		return strings.TrimSpace(providerModel)
	}

	switch providerName {
	case "openai":
		if c.factories.OpenAI == nil {
			return nil, fmt.Errorf("llm: openai factory is not configured")
		}
		return c.factories.OpenAI(OpenAIProviderConfig{
			APIKey:  cfg.Providers.OpenAI.APIKey,
			BaseURL: cfg.Providers.OpenAI.BaseURL,
			Model:   resolveModel(cfg.Providers.OpenAI.Model),
		})
	case "anthropic":
		if c.factories.Anthropic == nil {
			return nil, fmt.Errorf("llm: anthropic factory is not configured")
		}
		return c.factories.Anthropic(AnthropicProviderConfig{
			APIKey:  cfg.Providers.Anthropic.APIKey,
			BaseURL: cfg.Providers.Anthropic.BaseURL,
			Model:   resolveModel(cfg.Providers.Anthropic.Model),
		})
	case "google":
		if c.factories.Google == nil {
			return nil, fmt.Errorf("llm: google factory is not configured")
		}
		return c.factories.Google(GoogleProviderConfig{
			APIKey:  cfg.Providers.Google.APIKey,
			BaseURL: cfg.Providers.Google.BaseURL,
			Model:   resolveModel(cfg.Providers.Google.Model),
		})
	case "openrouter":
		if c.factories.OpenRouter == nil {
			return nil, fmt.Errorf("llm: openrouter factory is not configured")
		}
		return c.factories.OpenRouter(OpenAIProviderConfig{
			APIKey:  cfg.Providers.OpenRouter.APIKey,
			BaseURL: cfg.Providers.OpenRouter.BaseURL,
			Model:   resolveModel(cfg.Providers.OpenRouter.Model),
		})
	case "xai":
		if c.factories.XAI == nil {
			return nil, fmt.Errorf("llm: xai factory is not configured")
		}
		return c.factories.XAI(OpenAIProviderConfig{
			APIKey:  cfg.Providers.XAI.APIKey,
			BaseURL: cfg.Providers.XAI.BaseURL,
			Model:   resolveModel(cfg.Providers.XAI.Model),
		})
	case "ollama":
		if c.factories.Ollama == nil {
			return nil, fmt.Errorf("llm: ollama factory is not configured")
		}
		return c.factories.Ollama(OllamaProviderConfig{
			BaseURL: cfg.Providers.Ollama.BaseURL,
			APIKey:  cfg.Providers.Ollama.APIKey,
			Model:   resolveModel(cfg.Providers.Ollama.Model),
		})
	case "opencode":
		if c.factories.OpenCode == nil {
			return nil, fmt.Errorf("llm: opencode factory is not configured")
		}
		return c.factories.OpenCode(OpenCodeProviderConfig{
			BaseURL:         cfg.Providers.OpenCode.BaseURL,
			Username:        cfg.Providers.OpenCode.Username,
			Password:        cfg.Providers.OpenCode.Password,
			Model:           resolveModel(cfg.Providers.OpenCode.Model),
			UseRequestModel: useRequestModel,
		})
	default:
		if providerName == "" {
			return nil, fmt.Errorf("llm provider name is required")
		}
		return nil, fmt.Errorf("unsupported provider name %q", providerName)
	}
}

// BuildLLMBudget converts config budget fields into an LLM budget guard.
func BuildLLMBudget(cfg config.LLMConfig) *Budget {
	requests := cfg.BudgetRequestsPerDay
	tokens := cfg.BudgetTokensPerDay
	if requests < 0 {
		requests = 0
	}
	if tokens < 0 {
		tokens = 0
	}
	if requests == 0 && tokens == 0 {
		return nil
	}
	return NewBudget(requests, tokens)
}

func (c Composer) newProviderFromConfig(cfg config.LLMConfig, logger *slog.Logger) Provider {
	p, err := c.BuildProviderForSelection(cfg, cfg.DefaultProvider, cfg.QuickThinkModel, logger)
	if err != nil {
		providerName := strings.ToLower(strings.TrimSpace(cfg.DefaultProvider))
		if logger != nil {
			logger.Warn("LLM provider not available", slog.String("provider", providerName), slog.Any("error", err))
		}
		return nil
	}
	return p
}

func (c Composer) chainOptions(cfg config.LLMConfig, appMetrics any, logger *slog.Logger, budget *Budget) []ChainOption {
	var opts []ChainOption

	concurrency := cfg.ThrottleConcurrency
	if concurrency < 1 {
		concurrency = 4
	}
	opts = append(opts, WithThrottle(concurrency), WithThrottleKey(strings.ToLower(strings.TrimSpace(cfg.DefaultProvider))))

	if cfg.RetryMaxAttempts > 1 {
		opts = append(opts, WithRetry(cfg.RetryMaxAttempts))
		if recorder, ok := appMetrics.(retryMetricsRecorder); ok {
			opts = append(opts, WithChainRetryMetrics(&retryMetricsAdapter{
				recorder: recorder,
				provider: configuredPrimaryRetryProviderLabel(cfg.DefaultProvider),
			}))
		}
	}

	if fb, fbModel, ok := c.resolveFallbackSelection(cfg, logger); ok {
		secondary, err := c.buildProviderForSelection(cfg, fb, fbModel, logger, false)
		if err != nil {
			if logger != nil {
				logger.Warn("llm: fallback provider unavailable, skipping",
					slog.String("provider", fb),
					slog.Any("error", err),
				)
			}
		} else {
			opts = append(opts, WithFallback(secondary))
			if metrics, ok := appMetrics.(FallbackMetrics); ok {
				opts = append(opts, WithChainFallbackMetrics(metrics))
			}
		}
	}

	if runtimeCacheEnabled() {
		opts = append(opts, WithCache(NewMemoryResponseCache()))
		if metrics, ok := appMetrics.(CacheMetrics); ok {
			opts = append(opts, WithChainCacheMetrics(metrics))
		}
	}

	if budget != nil {
		opts = append(opts, WithBudget(budget))
		if metrics, ok := appMetrics.(BudgetMetrics); ok {
			opts = append(opts, WithChainBudgetMetrics(metrics))
		}
	}

	if cfg.CallTimeout > 0 {
		opts = append(opts, WithCallTimeout(cfg.CallTimeout))
	}

	return opts
}

// fallbackCandidateOrder is the preference order used when the configured
// fallback provider would loop back onto the primary.
var fallbackCandidateOrder = []string{"openai", "anthropic", "google", "openrouter", "xai", "ollama"}

// resolveFallbackSelection returns the fallback provider and model to build.
// A fallback that names the primary provider with the same (or no distinct)
// model is a loop: when the primary is down the fallback is down too. In that
// case the first other provider with credentials and a factory is used; if
// none exists the fallback is disabled. A same-provider fallback with an
// explicitly different model (for example OpenCode Luna -> Terra) is kept.
func (c Composer) resolveFallbackSelection(cfg config.LLMConfig, logger *slog.Logger) (string, string, bool) {
	fb := strings.ToLower(strings.TrimSpace(cfg.FallbackProvider))
	if fb == "" {
		return "", "", false
	}
	primary := strings.ToLower(strings.TrimSpace(cfg.DefaultProvider))
	fbModel := strings.TrimSpace(cfg.FallbackModel)
	if fb != primary {
		return fb, fbModel, true
	}
	primaryModel := strings.TrimSpace(cfg.QuickThinkModel)
	if fbModel != "" && fbModel != primaryModel {
		if logger != nil {
			logger.Info("llm: fallback uses the primary provider with a different model",
				slog.String("provider", fb),
				slog.String("fallback_model", fbModel),
			)
		}
		return fb, fbModel, true
	}
	for _, candidate := range fallbackCandidateOrder {
		if candidate == primary || !c.providerHasCredentials(cfg, candidate) {
			continue
		}
		if logger != nil {
			logger.Warn("llm: fallback provider equals the primary provider; using another configured provider",
				slog.String("configured_fallback", fb),
				slog.String("selected_fallback", candidate),
			)
		}
		// The configured fallback model belongs to the primary provider; let
		// the candidate use its own configured model.
		return candidate, "", true
	}
	if logger != nil {
		logger.Warn("llm: fallback provider equals the primary provider and no other provider has credentials; fallback disabled",
			slog.String("provider", fb),
		)
	}
	return "", "", false
}

func (c Composer) providerHasCredentials(cfg config.LLMConfig, name string) bool {
	switch name {
	case "openai":
		return c.factories.OpenAI != nil && strings.TrimSpace(cfg.Providers.OpenAI.APIKey) != ""
	case "anthropic":
		return c.factories.Anthropic != nil && strings.TrimSpace(cfg.Providers.Anthropic.APIKey) != ""
	case "google":
		return c.factories.Google != nil && strings.TrimSpace(cfg.Providers.Google.APIKey) != ""
	case "openrouter":
		return c.factories.OpenRouter != nil && strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) != ""
	case "xai":
		return c.factories.XAI != nil && strings.TrimSpace(cfg.Providers.XAI.APIKey) != ""
	case "ollama":
		return c.factories.Ollama != nil && strings.TrimSpace(cfg.Providers.Ollama.BaseURL) != ""
	default:
		return false
	}
}

func runtimeCacheEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LLM_CACHE_ENABLED")), "true")
}

type retryMetricsRecorder interface {
	RecordLLMRetry(provider string)
}

type retryMetricsAdapter struct {
	recorder retryMetricsRecorder
	provider string
}

func (a *retryMetricsAdapter) RecordLLMRetry() {
	if a == nil || a.recorder == nil {
		return
	}
	a.recorder.RecordLLMRetry(a.provider)
}

func configuredPrimaryRetryProviderLabel(provider string) string {
	name := strings.TrimSpace(provider)
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("configured_primary:%s", name)
}

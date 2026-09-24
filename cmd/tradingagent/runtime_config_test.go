package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
)

func TestEmbeddingBaseURLFromOllamaStripsV1(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"http://10.0.0.50:11434/v1":  "http://10.0.0.50:11434",
		"http://10.0.0.50:11434/v1/": "http://10.0.0.50:11434",
		"http://10.0.0.50:11434":     "http://10.0.0.50:11434",
		"http://localhost:11434/":    "http://localhost:11434",
		"":                           "",
	} {
		if got := embeddingBaseURLFromOllama(in); got != want {
			t.Fatalf("embeddingBaseURLFromOllama(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPortfolioAllocatorModeFromConfig(t *testing.T) {
	t.Parallel()
	if got := portfolioAllocatorModeFromConfig(config.Config{PortfolioAllocatorMode: config.PortfolioAllocatorModePaper}); got != portfolio.AllocatorModePaper {
		t.Fatalf("paper mode = %v", got)
	}
	for _, mode := range []string{"", config.PortfolioAllocatorModeShadow, "anything-else"} {
		if got := portfolioAllocatorModeFromConfig(config.Config{PortfolioAllocatorMode: mode}); got != portfolio.AllocatorModeShadow {
			t.Fatalf("mode %q = %v, want shadow", mode, got)
		}
	}
}

func TestLogRuntimeConfigWarnings(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := config.Config{
		Deprecations: []string{"ENABLE_AGENT_MEMORY is deprecated and ignored"},
		Features:     config.FeatureFlags{EnableTickerDiscovery: true},
		Brokers:      config.BrokerConfigs{Kalshi: config.KalshiConfig{Demo: false, APIBaseURL: config.KalshiDemoAPIBaseURL}},
	}
	logRuntimeConfigWarnings(cfg, logger)
	out := buf.String()
	for _, want := range []string{
		"ENABLE_AGENT_MEMORY is deprecated",
		"allocator in shadow mode: no orders will be submitted by the allocator",
		"discovery jobs unavailable: ENABLE_TICKER_DISCOVERY=true but DISCOVERY_EVALUATION_SCOPE_ID is unset",
		"KALSHI_DEMO=false but KALSHI_API_BASE_URL points at the demo exchange",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	logRuntimeConfigWarnings(config.Config{
		PortfolioAllocatorMode:     config.PortfolioAllocatorModePaper,
		DiscoveryEvaluationScopeID: "00000000-0000-4000-8000-000000000064",
		Features:                   config.FeatureFlags{EnableTickerDiscovery: true},
		Brokers:                    config.BrokerConfigs{Kalshi: config.KalshiConfig{Demo: true, APIBaseURL: config.KalshiDemoAPIBaseURL}},
	}, logger)
	if strings.Contains(buf.String(), "WARN") {
		t.Fatalf("unexpected warnings for a fully configured runtime:\n%s", buf.String())
	}
}

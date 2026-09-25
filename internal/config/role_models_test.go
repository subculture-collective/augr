package config

import (
	"strings"
	"testing"
)

func TestParseRoleModels(t *testing.T) {
	t.Parallel()

	got, err := ParseRoleModels(" risk_manager = openai/gpt-6-astra , invest_judge=openai/gpt-6-sol ,")
	if err != nil {
		t.Fatalf("ParseRoleModels() error = %v", err)
	}
	if len(got) != 2 || got["risk_manager"] != "openai/gpt-6-astra" || got["invest_judge"] != "openai/gpt-6-sol" {
		t.Fatalf("ParseRoleModels() = %#v", got)
	}

	if got, err := ParseRoleModels("  "); err != nil || got != nil {
		t.Fatalf("ParseRoleModels(blank) = %#v, %v; want nil, nil", got, err)
	}

	for raw, want := range map[string]string{
		"risk_manager":           "must be role=model",
		"risk_manager=":          "must be role=model",
		"cfo=openai/gpt-6-astra": "unknown agent role",
		"trader=a,trader=b":      "more than once",
	} {
		if _, err := ParseRoleModels(raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseRoleModels(%q) error = %v, want substring %q", raw, err, want)
		}
	}
}

func TestLoadParsesRoleModels(t *testing.T) {
	setMinimalLoadEnv(t)
	t.Setenv("LLM_ROLE_MODELS", "risk_manager=openai/gpt-6-astra")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LLM.RoleModels["risk_manager"] != "openai/gpt-6-astra" {
		t.Fatalf("RoleModels = %#v", cfg.LLM.RoleModels)
	}
}

func TestLoadRejectsMalformedRoleModels(t *testing.T) {
	setMinimalLoadEnv(t)
	t.Setenv("LLM_ROLE_MODELS", "cfo=openai/gpt-6-astra")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "LLM_ROLE_MODELS") {
		t.Fatalf("Load() error = %v, want an LLM_ROLE_MODELS error", err)
	}
}

func TestLoadRedditRequestBudget(t *testing.T) {
	setMinimalLoadEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DataProviders.Reddit.MaxRequestsPerHour != 20 {
		t.Fatalf("default budget = %d, want 20", cfg.DataProviders.Reddit.MaxRequestsPerHour)
	}

	setMinimalLoadEnv(t)
	t.Setenv("REDDIT_MAX_REQUESTS_PER_HOUR", "-1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "REDDIT_MAX_REQUESTS_PER_HOUR") {
		t.Fatalf("Load() error = %v, want a budget validation error", err)
	}
}

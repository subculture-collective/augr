package automation

import (
	"log/slog"
	"testing"
)

func TestKalshiDiscoveryConfigFromEnvOverridesDefaults(t *testing.T) {
	orig := kalshiDiscoveryLookupEnv
	t.Cleanup(func() { kalshiDiscoveryLookupEnv = orig })
	env := map[string]string{
		"KALSHI_DISCOVERY_FETCH_LIMIT":       "250",
		"KALSHI_DISCOVERY_MIN_VOLUME":        "300",
		"KALSHI_DISCOVERY_MIN_OPEN_INTEREST": "not-a-number",
		"KALSHI_DISCOVERY_MAX_SPREAD_PCT":    "-4",
	}
	kalshiDiscoveryLookupEnv = func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}

	cfg := KalshiDiscoveryConfigFromEnv(slog.Default())
	defaults := DefaultKalshiDiscoveryConfig()
	if cfg.FetchLimit != 250 || cfg.MinVolume != 300 {
		t.Fatalf("cfg = %+v, want env overrides applied", cfg)
	}
	if cfg.MinOpenInterest != defaults.MinOpenInterest || cfg.MaxSpreadPct != defaults.MaxSpreadPct {
		t.Fatalf("cfg = %+v, want invalid values to keep defaults %+v", cfg, defaults)
	}

	run := cfg.toRunConfig()
	if run.FetchLimit != 250 || run.Screener.MinVolume != 300 || run.Screener.MinOpenInterest != defaults.MinOpenInterest || run.MaxDeployments != 1 || run.MinConviction != 0.70 {
		t.Fatalf("toRunConfig() = %+v", run)
	}
	if run.Screener.MaxCandidates == 0 || run.Screener.MinDaysToClose == 0 {
		t.Fatalf("toRunConfig() dropped screener defaults: %+v", run.Screener)
	}
}

func TestKalshiDiscoveryConfigZeroValuesUseDefaults(t *testing.T) {
	run := KalshiDiscoveryConfig{}.toRunConfig()
	defaults := DefaultKalshiDiscoveryConfig()
	if run.FetchLimit != defaults.FetchLimit || run.Screener.MinVolume != defaults.MinVolume || run.Screener.MaxSpreadPct != defaults.MaxSpreadPct {
		t.Fatalf("toRunConfig() = %+v, want defaults %+v", run, defaults)
	}
}

package config

import (
	"strings"
	"testing"
	"time"
)

func setMinimalLoadEnv(t *testing.T) {
	t.Helper()
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/tradingagent?sslmode=disable")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("LLM_DEFAULT_PROVIDER", "openai")
	t.Setenv("POLYGON_API_KEY", "test-polygon-key")
	for _, key := range []string{
		"API_RATE_LIMIT", "API_TRUSTED_PROXIES", "API_CORS_ORIGINS", "LLM_DEBATE_TIMEOUT", "STALE_RUN_TTL",
		"SHUTDOWN_DRAIN_TIMEOUT", "RELEASE_DRILLS_VERIFIED", "PORTFOLIO_ALLOCATOR_MODE", "ADMIN_API_KEY",
		"SEC_EDGAR_APP_NAME", "SEC_EDGAR_APP_EMAIL", "KALSHI_DISCOVERY_FETCH_LIMIT", "KALSHI_DISCOVERY_MIN_VOLUME",
		"KALSHI_DISCOVERY_MIN_OPEN_INTEREST", "KALSHI_DISCOVERY_MAX_SPREAD_PCT", "KALSHI_DEMO", "KALSHI_API_BASE_URL",
		"REDIS_REQUIRED", "POLYMARKET_SIGNATURE_TYPE", "SCHEDULER_RELOAD_INTERVAL",
		"AUTOMATION_AUTO_DISABLE_COOLDOWN", "AUTOMATION_MISSED_RUN_CATCHUP",
		"REGIME_MAX_CONSECUTIVE_LOSSES", "REGIME_MIN_ROLLING_WIN_RATE",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadOperationalDefaults(t *testing.T) {
	setMinimalLoadEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.RateLimitPerMinute != 100 {
		t.Fatalf("RateLimitPerMinute = %d, want 100", cfg.Server.RateLimitPerMinute)
	}
	if len(cfg.Server.TrustedProxies) != 0 {
		t.Fatalf("TrustedProxies = %v, want empty", cfg.Server.TrustedProxies)
	}
	if len(cfg.Server.CORSOrigins) != 1 || cfg.Server.CORSOrigins[0] != "*" {
		t.Fatalf("CORSOrigins = %v, want [*]", cfg.Server.CORSOrigins)
	}
	if cfg.LLM.DebateTimeout != 0 {
		t.Fatalf("DebateTimeout = %s, want 0", cfg.LLM.DebateTimeout)
	}
	if cfg.StaleRunTTL != 50*time.Minute {
		t.Fatalf("StaleRunTTL = %s, want 50m", cfg.StaleRunTTL)
	}
	if cfg.ShutdownDrainTimeout != 30*time.Second {
		t.Fatalf("ShutdownDrainTimeout = %s, want 30s", cfg.ShutdownDrainTimeout)
	}
	if cfg.PortfolioAllocatorMode != PortfolioAllocatorModeShadow {
		t.Fatalf("PortfolioAllocatorMode = %q, want shadow", cfg.PortfolioAllocatorMode)
	}
	if cfg.ReleaseDrillsVerified || cfg.AdminAPIKey != "" || cfg.Features.RedisRequired {
		t.Fatalf("unexpected non-default operational flags: %+v", cfg)
	}
	if cfg.SECEdgar.AppName != "Augr" || cfg.SECEdgar.AppEmail != "" {
		t.Fatalf("SECEdgar = %+v", cfg.SECEdgar)
	}
	kd := cfg.Brokers.Kalshi
	if kd.DiscoveryFetchLimit != 500 || kd.DiscoveryMinVolume != 1000 || kd.DiscoveryMinOpenInterest != 500 || kd.DiscoveryMaxSpreadPct != 12 {
		t.Fatalf("Kalshi discovery = %+v", kd)
	}
	if cfg.Brokers.Polymarket.SignatureType != 0 {
		t.Fatalf("SignatureType = %d, want 0", cfg.Brokers.Polymarket.SignatureType)
	}
	if !cfg.Brokers.Kalshi.IsDemoHost() {
		t.Fatalf("IsDemoHost() = false for %q", cfg.Brokers.Kalshi.APIBaseURL)
	}
	if cfg.Brokers.Kalshi.APIBaseURL != KalshiDemoAPIBaseURL {
		t.Fatalf("Kalshi APIBaseURL = %q, want demo default", cfg.Brokers.Kalshi.APIBaseURL)
	}
	if len(cfg.Deprecations) != 0 {
		t.Fatalf("Deprecations = %v, want none", cfg.Deprecations)
	}
	if cfg.Features.SchedulerReloadInterval != time.Minute || cfg.Features.AutomationAutoDisableCooldown != 0 || !cfg.Features.AutomationMissedRunCatchUp {
		t.Fatalf("scheduler/automation defaults = %+v", cfg.Features)
	}
	if cfg.Risk.RegimeMaxConsecutiveLosses != 0 || cfg.Risk.RegimeMinRollingWinRate != 0 {
		t.Fatalf("regime defaults = %d %v, want disabled", cfg.Risk.RegimeMaxConsecutiveLosses, cfg.Risk.RegimeMinRollingWinRate)
	}
}

func TestLoadOperationalValues(t *testing.T) {
	setMinimalLoadEnv(t)
	t.Setenv("API_RATE_LIMIT", "250")
	t.Setenv("API_TRUSTED_PROXIES", "10.0.0.0/8, 172.16.0.0/12")
	t.Setenv("API_CORS_ORIGINS", "https://a.example, https://b.example")
	t.Setenv("LLM_DEBATE_TIMEOUT", "90s")
	t.Setenv("STALE_RUN_TTL", "0")
	t.Setenv("SHUTDOWN_DRAIN_TIMEOUT", "5s")
	t.Setenv("RELEASE_DRILLS_VERIFIED", "true")
	t.Setenv("PORTFOLIO_ALLOCATOR_MODE", "Paper")
	t.Setenv("ADMIN_API_KEY", " admin-secret ")
	t.Setenv("SEC_EDGAR_APP_EMAIL", "ops@example.com")
	t.Setenv("KALSHI_DISCOVERY_FETCH_LIMIT", "750")
	t.Setenv("KALSHI_DISCOVERY_MIN_VOLUME", "2500")
	t.Setenv("KALSHI_DISCOVERY_MIN_OPEN_INTEREST", "900")
	t.Setenv("KALSHI_DISCOVERY_MAX_SPREAD_PCT", "8.5")
	t.Setenv("KALSHI_DEMO", "false")
	t.Setenv("POLYMARKET_SIGNATURE_TYPE", "2")
	t.Setenv("REDIS_REQUIRED", "true")
	t.Setenv("SCHEDULER_RELOAD_INTERVAL", "0")
	t.Setenv("AUTOMATION_AUTO_DISABLE_COOLDOWN", "2h")
	t.Setenv("AUTOMATION_MISSED_RUN_CATCHUP", "false")
	t.Setenv("REGIME_MAX_CONSECUTIVE_LOSSES", "4")
	t.Setenv("REGIME_MIN_ROLLING_WIN_RATE", "0.45")
	t.Setenv("ENABLE_AGENT_MEMORY", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.RateLimitPerMinute != 250 {
		t.Fatalf("RateLimitPerMinute = %d", cfg.Server.RateLimitPerMinute)
	}
	if len(cfg.Server.TrustedProxies) != 2 || cfg.Server.TrustedProxies[1] != "172.16.0.0/12" {
		t.Fatalf("TrustedProxies = %v", cfg.Server.TrustedProxies)
	}
	if len(cfg.Server.CORSOrigins) != 2 || cfg.Server.CORSOrigins[0] != "https://a.example" {
		t.Fatalf("CORSOrigins = %v", cfg.Server.CORSOrigins)
	}
	if cfg.LLM.DebateTimeout != 90*time.Second || cfg.StaleRunTTL != 0 || cfg.ShutdownDrainTimeout != 5*time.Second {
		t.Fatalf("durations = %s %s %s", cfg.LLM.DebateTimeout, cfg.StaleRunTTL, cfg.ShutdownDrainTimeout)
	}
	if !cfg.ReleaseDrillsVerified || cfg.PortfolioAllocatorMode != PortfolioAllocatorModePaper || cfg.AdminAPIKey != "admin-secret" {
		t.Fatalf("flags = %t %q %q", cfg.ReleaseDrillsVerified, cfg.PortfolioAllocatorMode, cfg.AdminAPIKey)
	}
	if cfg.SECEdgar.AppEmail != "ops@example.com" {
		t.Fatalf("SECEdgar = %+v", cfg.SECEdgar)
	}
	kd := cfg.Brokers.Kalshi
	if kd.DiscoveryFetchLimit != 750 || kd.DiscoveryMinVolume != 2500 || kd.DiscoveryMinOpenInterest != 900 || kd.DiscoveryMaxSpreadPct != 8.5 {
		t.Fatalf("Kalshi discovery = %+v", kd)
	}
	if cfg.Brokers.Polymarket.SignatureType != 2 {
		t.Fatalf("SignatureType = %d, want 2", cfg.Brokers.Polymarket.SignatureType)
	}
	if cfg.Brokers.Kalshi.IsDemoHost() {
		t.Fatalf("IsDemoHost() = true for %q", cfg.Brokers.Kalshi.APIBaseURL)
	}
	if cfg.Brokers.Kalshi.APIBaseURL != KalshiProductionAPIBaseURL {
		t.Fatalf("Kalshi APIBaseURL = %q, want production default", cfg.Brokers.Kalshi.APIBaseURL)
	}
	if !cfg.Features.RedisRequired {
		t.Fatal("RedisRequired = false")
	}
	if cfg.Features.SchedulerReloadInterval != 0 || cfg.Features.AutomationAutoDisableCooldown != 2*time.Hour || cfg.Features.AutomationMissedRunCatchUp {
		t.Fatalf("scheduler/automation values = %+v", cfg.Features)
	}
	if cfg.Risk.RegimeMaxConsecutiveLosses != 4 || cfg.Risk.RegimeMinRollingWinRate != 0.45 {
		t.Fatalf("regime values = %d %v", cfg.Risk.RegimeMaxConsecutiveLosses, cfg.Risk.RegimeMinRollingWinRate)
	}
	if len(cfg.Deprecations) != 1 || !strings.Contains(cfg.Deprecations[0], "ENABLE_AGENT_MEMORY") {
		t.Fatalf("Deprecations = %v", cfg.Deprecations)
	}
}

func TestLoadRejectsInvalidOperationalValues(t *testing.T) {
	cases := []struct{ key, value, want string }{
		{"LLM_DEBATE_TIMEOUT", "soon", "LLM_DEBATE_TIMEOUT must be a valid duration"},
		{"STALE_RUN_TTL", "later", "STALE_RUN_TTL must be a valid duration"},
		{"STALE_RUN_TTL", "-1m", "STALE_RUN_TTL must be >= 0"},
		{"API_RATE_LIMIT", "-1", "API_RATE_LIMIT must be >= 0"},
		{"API_TRUSTED_PROXIES", "not-a-cidr", "API_TRUSTED_PROXIES contains invalid CIDR"},
		{"PORTFOLIO_ALLOCATOR_MODE", "live", "PORTFOLIO_ALLOCATOR_MODE \"live\" is not supported"},
		{"SEC_EDGAR_APP_EMAIL", "ops", "SEC_EDGAR_APP_EMAIL must be an email address"},
		{"KALSHI_DISCOVERY_FETCH_LIMIT", "-5", "KALSHI_DISCOVERY_FETCH_LIMIT must be greater than 0"},
		{"KALSHI_DISCOVERY_MAX_SPREAD_PCT", "150", "KALSHI_DISCOVERY_MAX_SPREAD_PCT must be between 0 and 100"},
		{"RELEASE_DRILLS_VERIFIED", "yes-please", "RELEASE_DRILLS_VERIFIED must be a boolean"},
		{"POLYMARKET_SIGNATURE_TYPE", "7", "POLYMARKET_SIGNATURE_TYPE must be 0 (EOA), 1 (POLY_PROXY), or 2 (GNOSIS_SAFE)"},
		{"SCHEDULER_RELOAD_INTERVAL", "-1s", "SCHEDULER_RELOAD_INTERVAL must be >= 0"},
		{"REGIME_MAX_CONSECUTIVE_LOSSES", "-1", "REGIME_MAX_CONSECUTIVE_LOSSES must be >= 0"},
		{"REGIME_MIN_ROLLING_WIN_RATE", "1.5", "REGIME_MIN_ROLLING_WIN_RATE must be between 0 and 1"},
		{"AUTOMATION_AUTO_DISABLE_COOLDOWN", "soon", "AUTOMATION_AUTO_DISABLE_COOLDOWN must be a valid duration"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			setMinimalLoadEnv(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateRejectsPaperAllocatorWithLiveTrading(t *testing.T) {
	cfg := validConfig()
	cfg.PortfolioAllocatorMode = PortfolioAllocatorModePaper
	cfg.Features.EnableLiveTrading = true
	cfg.Brokers.Alpaca = BrokerConfig{APIKey: "k", APISecret: "s"}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "PORTFOLIO_ALLOCATOR_MODE=paper requires ENABLE_LIVE_TRADING=false") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateLLMProviderErrorMentionsOpenCode(t *testing.T) {
	cfg := validConfig()
	cfg.LLM.Providers = LLMProviderConfigs{}
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "OPENCODE_BASE_URL + OPENCODE_SERVER_PASSWORD") {
		t.Fatalf("Validate() error = %v, want OpenCode mention", err)
	}
}

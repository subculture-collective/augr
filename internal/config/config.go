package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

const (
	DefaultPaperInitialCapital        = 100_000.0
	DefaultPaperBuyingPowerMultiplier = 2.0
	DefaultPaperSlippageBPS           = 5.0
	DefaultPaperFeePct                = 0.0001
)

// Config contains application configuration loaded from the environment.
type Config struct {
	Environment                  string
	CanonicalAccountID           string
	DiscoveryEvaluationScopeID   string
	AutomaticShadowPromotion     bool
	Server                       ServerConfig
	Database                     DatabaseConfig
	Redis                        RedisConfig
	LLM                          LLMConfig
	Embedding                    EmbeddingConfig
	DataProviders                DataProviderConfigs
	Brokers                      BrokerConfigs
	Paper                        PaperConfig
	Polygon                      PolygonConnectionConfig
	Risk                         RiskConfig
	Notifications                NotificationConfig
	Features                     FeatureFlags
	LiveTradingAllowedStrategies []string
	LiveTradingAllowedBrokers    []string
	TickerDiscovery              TickerDiscoveryConfig
	HistoryRefreshWatchlistLimit int
	// PortfolioAllocatorMode is "shadow" (default; no allocator orders) or "paper".
	PortfolioAllocatorMode string
	// ReleaseDrillsVerified records operator attestation for release readiness.
	ReleaseDrillsVerified bool
	// AdminAPIKey guards kill-switch deactivation and breaker reset. Empty
	// disables those endpoints.
	AdminAPIKey string
	SECEdgar    SECEdgarConfig
	// StaleRunTTL bounds how long a pipeline run may stay "running" before the
	// reconciler marks it stale. Zero disables the reconciler.
	StaleRunTTL time.Duration
	// ShutdownDrainTimeout bounds how long shutdown waits for in-flight runs
	// before cancelling them.
	ShutdownDrainTimeout time.Duration
	// Deprecations lists environment variables that were set but no longer
	// affect behaviour; the runtime logs each at WARN.
	Deprecations []string
}

// SECEdgarConfig identifies the application to SEC EDGAR. Refreshes are
// disabled when AppEmail is empty.
type SECEdgarConfig struct {
	AppName  string
	AppEmail string
}

const (
	PortfolioAllocatorModeShadow = "shadow"
	PortfolioAllocatorModePaper  = "paper"

	// KalshiProductionAPIBaseURL is the default KALSHI_API_BASE_URL when
	// KALSHI_DEMO=false.
	KalshiProductionAPIBaseURL = "https://api.elections.kalshi.com/trade-api/v2"
	// KalshiDemoAPIBaseURL is the default KALSHI_API_BASE_URL when KALSHI_DEMO=true.
	KalshiDemoAPIBaseURL = "https://external-api.demo.kalshi.co/trade-api/v2"
)

// TickerDiscoveryConfig holds settings for the automated ticker discovery pipeline.
type TickerDiscoveryConfig struct {
	Enabled    bool
	Cron       string
	MinADV     float64
	MaxTickers int
}

// ServerConfig contains HTTP server settings.
type ServerConfig struct {
	Host      string
	Port      int
	JWTSecret string
	// RateLimitPerMinute is the per-client request budget (API_RATE_LIMIT).
	// Zero disables the limiter.
	RateLimitPerMinute int
	// TrustedProxies lists CIDRs whose X-Forwarded-For header is honoured
	// (API_TRUSTED_PROXIES).
	TrustedProxies []string
	// CORSOrigins lists allowed origins (API_CORS_ORIGINS); "*" allows all.
	CORSOrigins []string
}

// DatabaseConfig contains database connection settings.
type DatabaseConfig struct {
	URL      string
	PoolSize int
	SSLMode  string
}

// RedisConfig contains Redis settings.
type RedisConfig struct {
	URL string
}

// LLMConfig contains model selection and provider settings.
type LLMConfig struct {
	DefaultProvider string
	DeepThinkModel  string
	QuickThinkModel string
	Timeout         time.Duration
	Providers       LLMProviderConfigs

	// RoleModels overrides the tier model for individual agent roles
	// (LLM_ROLE_MODELS, e.g. "risk_manager=openai/gpt-6-astra"). Roles without
	// an entry keep the deep or quick tier model.
	RoleModels map[string]string
	// RoleModelsRaw keeps the unparsed LLM_ROLE_MODELS value so Validate can
	// report malformed entries.
	RoleModelsRaw string

	// DebateTimeout bounds one debate-round LLM call before the quick model
	// retry (LLM_DEBATE_TIMEOUT). Zero disables the per-call timeout.
	DebateTimeout time.Duration

	// Resilience settings (PR: llm-resilience).
	FallbackProvider     string
	FallbackModel        string
	RetryMaxAttempts     int
	CallTimeout          time.Duration
	BudgetRequestsPerDay int
	BudgetTokensPerDay   int
	ThrottleConcurrency  int
}

// LLMProviderConfigs contains provider-specific settings.
type LLMProviderConfigs struct {
	OpenAI     LLMProviderConfig
	Anthropic  LLMProviderConfig
	Google     LLMProviderConfig
	OpenRouter LLMProviderConfig
	XAI        LLMProviderConfig
	Ollama     OllamaConfig
	OpenCode   OpenCodeConfig
}

// LLMProviderConfig contains settings for API-backed LLM providers.
type LLMProviderConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// OllamaConfig contains llama-line broker / Ollama-compatible settings.
type OllamaConfig struct {
	BaseURL string
	Model   string
	APIKey  string
}

// OpenCodeConfig contains settings for the isolated OpenCode OAuth service.
type OpenCodeConfig struct {
	BaseURL  string
	Username string
	Password string
	Model    string
}

// EmbeddingConfig contains settings for the embedding provider.
type EmbeddingConfig struct {
	Model   string        // Embedding model name (default: nomic-embed-text).
	BaseURL string        // Ollama server base URL (default: from Ollama config).
	Timeout time.Duration // Per-request timeout (default: 30s).
}

// DataProviderConfigs contains external data provider settings.
type DataProviderConfigs struct {
	Polygon                     DataProviderConfig
	PolygonBulkSnapshotsEnabled bool
	AlphaVantage                DataProviderConfig
	Finnhub                     DataProviderConfig
	FMP                         DataProviderConfig
	NewsAPI                     DataProviderConfig
	Tradier                     TradierConfig
}

// TradierConfig contains Tradier-specific settings.
type TradierConfig struct {
	APIKey  string
	Sandbox bool
}

// DataProviderConfig contains settings for a market data provider.
type DataProviderConfig struct {
	APIKey             string
	RateLimitPerMinute int
}

// BrokerConfigs contains broker integration settings.
type BrokerConfigs struct {
	Alpaca     BrokerConfig
	Binance    BrokerConfig
	Polymarket PolymarketConfig
	Kalshi     KalshiConfig
}

type PolygonConnectionConfig struct {
	RPCURL string `env:"POLYGON_RPC_URL"`
	WSURL  string `env:"POLYGON_WS_URL"`
}

// PolymarketConfig contains credentials and endpoint settings for Polymarket.
// Live trading uses the retail API + gateway API, while legacy data and signal
// workflows may still read from the historical CLOB endpoints during the
// migration window.
type PolymarketConfig struct {
	Address        string
	KeyID          string
	SecretKey      string
	Passphrase     string
	APIBaseURL     string
	GatewayBaseURL string
	CLOBURL        string
	// SignatureType selects the CLOB order-signing scheme
	// (POLYMARKET_SIGNATURE_TYPE): 0 EOA, 1 POLY_PROXY, 2 GNOSIS_SAFE.
	SignatureType int
}

// KalshiConfig contains credentials and endpoint settings for Kalshi.
type KalshiConfig struct {
	APIBaseURL              string
	APIKeyID                string
	PrivateKeyPEMB64        string
	Demo                    bool
	RequestsPerWindow       int
	Window                  time.Duration
	MaxAttempts             int
	BaseBackoff             time.Duration
	MaxBackoff              time.Duration
	JitterRatio             float64
	DryRun                  bool
	AutoExitsEnabled        bool
	SettlementGateThreshold int
	MarkMaxAge              time.Duration
	ProjectionDatabaseURL   string
	ProjectionKeyID         string
	ProjectionSecretB64     string
	// Discovery screener tuning (KALSHI_DISCOVERY_*). Zero values fall back to
	// the automation job defaults.
	DiscoveryFetchLimit      int     // KALSHI_DISCOVERY_FETCH_LIMIT, default 500
	DiscoveryMinVolume       float64 // KALSHI_DISCOVERY_MIN_VOLUME, default 1000
	DiscoveryMinOpenInterest float64 // KALSHI_DISCOVERY_MIN_OPEN_INTEREST, default 500
	DiscoveryMaxSpreadPct    float64 // KALSHI_DISCOVERY_MAX_SPREAD_PCT, default 12
}

// IsDemoHost reports whether the configured API base URL points at the Kalshi
// demo exchange, whose first catalog page is zero-volume markets.
func (c KalshiConfig) IsDemoHost() bool {
	return strings.Contains(strings.ToLower(c.APIBaseURL), "demo.kalshi.co")
}

// BrokerConfig contains broker credentials and execution mode.
type BrokerConfig struct {
	APIKey    string
	APISecret string
	PaperMode bool
}

// PaperConfig identifies one isolated paper-evaluation environment. A scored
// profile may produce promotion evidence; stress mode is always synthetic.
type PaperConfig struct {
	EvaluationMode        domain.PaperEvaluationMode
	InitialCapital        float64
	BuyingPowerMultiplier float64
	SlippageBPS           float64
	FeePct                float64
}

func (c PaperConfig) EvaluationProfile() (domain.PaperEvaluationProfile, error) {
	return domain.NewPaperEvaluationProfile(
		c.EvaluationMode,
		c.InitialCapital,
		c.BuyingPowerMultiplier,
		c.SlippageBPS,
		c.FeePct,
	)
}

// RiskConfig contains application-wide risk management defaults.
type RiskConfig struct {
	MaxPositionSizePct      float64
	MaxDailyLossPct         float64
	MaxDrawdownPct          float64
	MaxOpenPositions        int
	MaxKalshiExposurePct    float64
	CircuitBreakerThreshold float64
	CircuitBreakerCooldown  time.Duration
	Polymarket              PolymarketRiskConfig
	// Regime rules pause new allocations for a run when a threshold trips.
	// Zero disables each rule.
	RegimeMaxConsecutiveLosses int     // REGIME_MAX_CONSECUTIVE_LOSSES
	RegimeMinRollingWinRate    float64 // REGIME_MIN_ROLLING_WIN_RATE (0-1)
}

// PolymarketRiskConfig contains prediction-market-specific risk limits.
type PolymarketRiskConfig struct {
	MaxSingleMarketExposurePct float64 // max fraction of portfolio in one market (default: 0.05)
	MaxTotalExposurePct        float64 // max fraction across all polymarket positions (default: 0.30)
	MaxPositionUSDC            float64 // hard USD cap per position (0 = disabled)
	MinLiquidity               float64 // minimum market liquidity in USDC (default: 1000)
	MaxSpreadPct               float64 // max bid-ask spread as fraction of mid price (default: 0.10)
	MinDaysToResolution        int     // skip markets resolving in fewer than N days (default: 1)
}

// NotificationConfig contains outbound notifier credentials and alert rule thresholds.
type NotificationConfig struct {
	Telegram  TelegramNotificationConfig
	Email     EmailNotificationConfig
	N8N       WebhookNotificationConfig
	PagerDuty WebhookNotificationConfig
	Discord   DiscordNotificationConfig
	Alerts    AlertRulesConfig
}

// TelegramNotificationConfig contains Telegram bot delivery settings.
type TelegramNotificationConfig struct {
	BotToken string
	ChatID   string
}

// EmailNotificationConfig contains SMTP delivery settings.
type EmailNotificationConfig struct {
	SMTPHost string
	SMTPPort int
	Username string
	Password string
	From     string
	To       []string
}

// WebhookNotificationConfig contains reusable webhook delivery settings.
type WebhookNotificationConfig struct {
	URL    string
	Secret string
}

// DiscordNotificationConfig contains Discord webhook URLs for different event types.
type DiscordNotificationConfig struct {
	SignalWebhookURL   string
	DecisionWebhookURL string
	AlertWebhookURL    string
}

// AlertRulesConfig contains alert thresholds and channel routing.
type AlertRulesConfig struct {
	PipelineFailure PipelineFailureAlertRuleConfig
	CircuitBreaker  ImmediateAlertRuleConfig
	LLMProviderDown LLMProviderDownAlertRuleConfig
	HighLatency     HighLatencyAlertRuleConfig
	KillSwitch      ImmediateAlertRuleConfig
	DBConnection    ImmediateAlertRuleConfig
}

// PipelineFailureAlertRuleConfig contains configuration for consecutive pipeline failures.
type PipelineFailureAlertRuleConfig struct {
	Threshold int
	Channels  []string
}

// ImmediateAlertRuleConfig contains routing for immediate alerts.
type ImmediateAlertRuleConfig struct {
	Channels []string
}

// LLMProviderDownAlertRuleConfig contains rolling-window LLM provider health thresholds.
type LLMProviderDownAlertRuleConfig struct {
	ErrorRateThreshold float64
	Window             time.Duration
	Channels           []string
}

// HighLatencyAlertRuleConfig contains pipeline latency thresholds.
type HighLatencyAlertRuleConfig struct {
	Threshold time.Duration
	Channels  []string
}

// FeatureFlags contains boolean feature toggles.
type FeatureFlags struct {
	EnableScheduler     bool
	SchedulerJobTimeout time.Duration
	// SchedulerReloadInterval is how often the scheduler re-reads strategy
	// schedules (SCHEDULER_RELOAD_INTERVAL, default 60s; 0 disables).
	SchedulerReloadInterval time.Duration
	// AutomationAutoDisableCooldown is the first re-arm delay after an
	// automation job auto-disables (AUTOMATION_AUTO_DISABLE_COOLDOWN; 0 uses
	// the orchestrator default of 1h).
	AutomationAutoDisableCooldown time.Duration
	// AutomationMissedRunCatchUp runs daily-or-slower jobs whose last
	// scheduled fire was missed while the process was down
	// (AUTOMATION_MISSED_RUN_CATCHUP, default true).
	AutomationMissedRunCatchUp bool
	EnableRedisCache           bool
	// RedisRequired makes a failing Redis probe fail /healthz. By default the
	// probe is advisory and only marks redis as degraded.
	RedisRequired              bool
	EnableLiveTrading          bool
	EnableTickerDiscovery      bool
	EnablePolymarketAutomation bool
}

// Load loads configuration from the environment and validates it.
func Load() (Config, error) {
	if err := loadDotEnv(); err != nil {
		return Config{}, err
	}

	cfg, err := loadFromEnvironment()
	if err != nil {
		return Config{}, err
	}

	if err := Validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func loadDotEnv() error {
	environment := firstNonEmpty(os.Getenv("APP_ENV"), "development")
	if !strings.EqualFold(environment, "development") {
		return nil
	}

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load .env file: %w", err)
	}

	return nil
}

func loadFromEnvironment() (Config, error) {
	serverPort, err := getEnvInt("APP_PORT", 8080)
	if err != nil {
		return Config{}, err
	}

	databasePoolSize, err := getEnvInt("DATABASE_POOL_SIZE", 10)
	if err != nil {
		return Config{}, err
	}

	llmTimeout, err := getEnvDuration("LLM_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}

	llmRetryMaxAttempts, err := getEnvInt("LLM_RETRY_MAX_ATTEMPTS", 2)
	if err != nil {
		return Config{}, err
	}

	llmCallTimeout, err := getEnvDuration("LLM_CALL_TIMEOUT", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}

	llmBudgetRequestsDay, err := getEnvInt("LLM_BUDGET_REQUESTS_DAY", 0)
	if err != nil {
		return Config{}, err
	}

	llmBudgetTokensDay, err := getEnvInt("LLM_BUDGET_TOKENS_DAY", 0)
	if err != nil {
		return Config{}, err
	}

	llmThrottleConcurrency, err := getEnvInt("LLM_THROTTLE_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}

	alphaVantageRateLimit, err := getEnvInt("ALPHA_VANTAGE_RATE_LIMIT_PER_MINUTE", 5)
	if err != nil {
		return Config{}, err
	}

	finnhubRateLimit, err := getEnvInt("FINNHUB_RATE_LIMIT_PER_MINUTE", 60)
	if err != nil {
		return Config{}, err
	}

	fmpRateLimit, err := getEnvInt("FMP_RATE_LIMIT_PER_MINUTE", 4)
	if err != nil {
		return Config{}, err
	}

	polygonBulkSnapshotsEnabled, err := getEnvBool("POLYGON_BULK_SNAPSHOTS_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	alpacaPaperMode, err := getEnvBool("ALPACA_PAPER_MODE", true)
	if err != nil {
		return Config{}, err
	}

	binancePaperMode, err := getEnvBool("BINANCE_PAPER_MODE", true)
	if err != nil {
		return Config{}, err
	}

	paperInitialCapital, err := getEnvFloat64("PAPER_INITIAL_CAPITAL", DefaultPaperInitialCapital)
	if err != nil {
		return Config{}, err
	}
	paperBuyingPowerMultiplier, err := getEnvFloat64("PAPER_BUYING_POWER_MULTIPLIER", DefaultPaperBuyingPowerMultiplier)
	if err != nil {
		return Config{}, err
	}
	paperSlippageBPS, err := getEnvFloat64("PAPER_SLIPPAGE_BPS", DefaultPaperSlippageBPS)
	if err != nil {
		return Config{}, err
	}
	paperFeePct, err := getEnvFloat64("PAPER_FEE_PCT", DefaultPaperFeePct)
	if err != nil {
		return Config{}, err
	}

	tradierSandbox, err := getEnvBool("TRADIER_SANDBOX", true)
	if err != nil {
		return Config{}, err
	}

	kalshiDemo, err := getEnvBool("KALSHI_DEMO", true)
	if err != nil {
		return Config{}, err
	}
	kalshiRequestsPerWindow, err := getEnvInt("KALSHI_REQUESTS_PER_WINDOW", 60)
	if err != nil {
		return Config{}, err
	}
	kalshiWindow, err := getEnvDuration("KALSHI_REQUEST_WINDOW", time.Minute)
	if err != nil {
		return Config{}, err
	}
	kalshiMaxAttempts, err := getEnvInt("KALSHI_MAX_ATTEMPTS", 3)
	if err != nil {
		return Config{}, err
	}
	kalshiBaseBackoff, err := getEnvDuration("KALSHI_BASE_BACKOFF", 100*time.Millisecond)
	if err != nil {
		return Config{}, err
	}
	kalshiMaxBackoff, err := getEnvDuration("KALSHI_MAX_BACKOFF", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	kalshiJitterRatio, err := getEnvFloat64("KALSHI_JITTER_RATIO", 0.2)
	if err != nil {
		return Config{}, err
	}
	kalshiDryRun, err := getEnvBool("KALSHI_DRY_RUN", true)
	if err != nil {
		return Config{}, err
	}
	kalshiAutoExitsEnabled, err := getEnvBool("KALSHI_AUTO_EXITS_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	kalshiSettlementGateThreshold, err := getEnvInt("KALSHI_SETTLEMENT_GATE_THRESHOLD", 20)
	if err != nil {
		return Config{}, err
	}
	kalshiMarkMaxAge, err := getEnvDuration("KALSHI_MARK_MAX_AGE", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}

	maxPositionSizePct, err := getEnvFloat64("RISK_MAX_POSITION_SIZE_PCT", 0.10)
	if err != nil {
		return Config{}, err
	}

	maxDailyLossPct, err := getEnvFloat64("RISK_MAX_DAILY_LOSS_PCT", 0.02)
	if err != nil {
		return Config{}, err
	}

	maxDrawdownPct, err := getEnvFloat64("RISK_MAX_DRAWDOWN_PCT", 0.10)
	if err != nil {
		return Config{}, err
	}

	maxOpenPositions, err := getEnvInt("RISK_MAX_OPEN_POSITIONS", 10)
	if err != nil {
		return Config{}, err
	}

	maxKalshiExposurePct, err := getEnvFloat64("RISK_MAX_KALSHI_EXPOSURE_PCT", 0.20)
	if err != nil {
		return Config{}, err
	}

	circuitBreakerThreshold, err := getEnvFloat64("RISK_CIRCUIT_BREAKER_THRESHOLD", 0.05)
	if err != nil {
		return Config{}, err
	}

	circuitBreakerCooldown, err := getEnvDuration("RISK_CIRCUIT_BREAKER_COOLDOWN", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}

	regimeMaxConsecutiveLosses, err := getEnvInt("REGIME_MAX_CONSECUTIVE_LOSSES", 0)
	if err != nil {
		return Config{}, err
	}
	regimeMinRollingWinRate, err := getEnvFloat64("REGIME_MIN_ROLLING_WIN_RATE", 0)
	if err != nil {
		return Config{}, err
	}

	pmMaxSingleExposure, err := getEnvFloat64("RISK_POLYMARKET_MAX_SINGLE_EXPOSURE_PCT", 0.05)
	if err != nil {
		return Config{}, err
	}

	pmMaxTotalExposure, err := getEnvFloat64("RISK_POLYMARKET_MAX_TOTAL_EXPOSURE_PCT", 0.30)
	if err != nil {
		return Config{}, err
	}

	pmMaxPositionUSDC, err := getEnvFloat64("RISK_POLYMARKET_MAX_POSITION_USDC", 0)
	if err != nil {
		return Config{}, err
	}

	pmMinLiquidity, err := getEnvFloat64("RISK_POLYMARKET_MIN_LIQUIDITY_USDC", 1000)
	if err != nil {
		return Config{}, err
	}

	pmMaxSpreadPct, err := getEnvFloat64("RISK_POLYMARKET_MAX_SPREAD_PCT", 0.10)
	if err != nil {
		return Config{}, err
	}

	pmMinDaysToResolution, err := getEnvInt("RISK_POLYMARKET_MIN_DAYS_TO_RESOLUTION", 1)
	if err != nil {
		return Config{}, err
	}

	smtpPort, err := getEnvInt("NOTIFY_SMTP_PORT", 587)
	if err != nil {
		return Config{}, err
	}

	pipelineFailureThreshold, err := getEnvInt("ALERT_PIPELINE_FAILURE_THRESHOLD", 3)
	if err != nil {
		return Config{}, err
	}

	llmProviderDownErrorRateThreshold, err := getEnvFloat64("ALERT_LLM_PROVIDER_DOWN_ERROR_RATE_THRESHOLD", 0.5)
	if err != nil {
		return Config{}, err
	}

	llmProviderDownWindow, err := getEnvDuration("ALERT_LLM_PROVIDER_DOWN_WINDOW", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}

	highLatencyThreshold, err := getEnvDuration("ALERT_HIGH_LATENCY_THRESHOLD", 120*time.Second)
	if err != nil {
		return Config{}, err
	}

	enableScheduler, err := getEnvBool("ENABLE_SCHEDULER", false)
	if err != nil {
		return Config{}, err
	}

	schedulerJobTimeout, err := getEnvDuration("SCHEDULER_JOB_TIMEOUT", 0)
	if err != nil {
		return Config{}, err
	}
	schedulerReloadInterval, err := getEnvDuration("SCHEDULER_RELOAD_INTERVAL", time.Minute)
	if err != nil {
		return Config{}, err
	}
	automationAutoDisableCooldown, err := getEnvDuration("AUTOMATION_AUTO_DISABLE_COOLDOWN", 0)
	if err != nil {
		return Config{}, err
	}
	automationMissedRunCatchUp, err := getEnvBool("AUTOMATION_MISSED_RUN_CATCHUP", true)
	if err != nil {
		return Config{}, err
	}

	embeddingTimeout, err := getEnvDuration("EMBEDDING_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}

	enableRedisCache, err := getEnvBool("ENABLE_REDIS_CACHE", true)
	if err != nil {
		return Config{}, err
	}

	redisRequired, err := getEnvBool("REDIS_REQUIRED", false)
	if err != nil {
		return Config{}, err
	}
	var deprecations []string
	if strings.TrimSpace(os.Getenv("ENABLE_AGENT_MEMORY")) != "" {
		deprecations = append(deprecations, "ENABLE_AGENT_MEMORY is deprecated and ignored: agent memory is always available when the memory repository is configured")
	}

	enableLiveTrading, err := getEnvBool("ENABLE_LIVE_TRADING", false)
	if err != nil {
		return Config{}, err
	}

	enableTickerDiscovery, err := getEnvBool("ENABLE_TICKER_DISCOVERY", false)
	if err != nil {
		return Config{}, err
	}
	automaticShadowPromotion, err := getEnvBool("AUTOMATIC_SHADOW_PROMOTION", false)
	if err != nil {
		return Config{}, err
	}

	// Polymarket is retained as a historical/read compatibility surface only.
	// New installations must opt in explicitly; Kalshi is the active event-market
	// provider and a missing environment variable must never restart Polymarket jobs.
	enablePolymarketAutomation, err := getEnvBool("ENABLE_POLYMARKET_AUTOMATION", false)
	if err != nil {
		return Config{}, err
	}

	tickerDiscoveryMinADV, err := getEnvFloat64("TICKER_DISCOVERY_MIN_ADV", 100000)
	if err != nil {
		return Config{}, err
	}

	tickerDiscoveryMaxTickers, err := getEnvInt("TICKER_DISCOVERY_MAX_TICKERS", 30)
	if err != nil {
		return Config{}, err
	}

	historyRefreshWatchlistLimit, err := getEnvInt("HISTORY_REFRESH_WATCHLIST_LIMIT", 250)
	if err != nil {
		return Config{}, err
	}

	apiRateLimit, err := getEnvInt("API_RATE_LIMIT", 100)
	if err != nil {
		return Config{}, err
	}
	llmDebateTimeout, err := getEnvDuration("LLM_DEBATE_TIMEOUT", 0)
	if err != nil {
		return Config{}, err
	}
	staleRunTTL, err := getEnvDuration("STALE_RUN_TTL", 50*time.Minute)
	if err != nil {
		return Config{}, err
	}
	shutdownDrainTimeout, err := getEnvDuration("SHUTDOWN_DRAIN_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	releaseDrillsVerified, err := getEnvBool("RELEASE_DRILLS_VERIFIED", false)
	if err != nil {
		return Config{}, err
	}
	kalshiDiscoveryFetchLimit, err := getEnvInt("KALSHI_DISCOVERY_FETCH_LIMIT", 500)
	if err != nil {
		return Config{}, err
	}
	kalshiDiscoveryMinVolume, err := getEnvFloat64("KALSHI_DISCOVERY_MIN_VOLUME", 1000)
	if err != nil {
		return Config{}, err
	}
	kalshiDiscoveryMinOpenInterest, err := getEnvFloat64("KALSHI_DISCOVERY_MIN_OPEN_INTEREST", 500)
	if err != nil {
		return Config{}, err
	}
	kalshiDiscoveryMaxSpreadPct, err := getEnvFloat64("KALSHI_DISCOVERY_MAX_SPREAD_PCT", 12)
	if err != nil {
		return Config{}, err
	}
	kalshiAPIBaseURLDefault := KalshiDemoAPIBaseURL
	if !kalshiDemo {
		kalshiAPIBaseURLDefault = KalshiProductionAPIBaseURL
	}
	polymarketSignatureType, err := getEnvInt("POLYMARKET_SIGNATURE_TYPE", 0)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Environment:                getEnvString("APP_ENV", "development"),
		CanonicalAccountID:         strings.TrimSpace(os.Getenv("PROJECTION_ACCOUNT_ID")),
		DiscoveryEvaluationScopeID: strings.TrimSpace(os.Getenv("DISCOVERY_EVALUATION_SCOPE_ID")),
		AutomaticShadowPromotion:   automaticShadowPromotion,
		Server: ServerConfig{
			Host:               getEnvString("APP_HOST", "0.0.0.0"),
			Port:               serverPort,
			JWTSecret:          os.Getenv("JWT_SECRET"),
			RateLimitPerMinute: apiRateLimit,
			TrustedProxies:     getEnvCSV("API_TRUSTED_PROXIES"),
			CORSOrigins:        getEnvCSVWithDefault("API_CORS_ORIGINS", []string{"*"}),
		},
		Database: DatabaseConfig{
			URL:      os.Getenv("DATABASE_URL"),
			PoolSize: databasePoolSize,
			SSLMode:  getEnvString("DATABASE_SSL_MODE", "disable"),
		},
		Redis: RedisConfig{
			URL: os.Getenv("REDIS_URL"),
		},
		LLM: LLMConfig{
			DefaultProvider: getEnvString("LLM_DEFAULT_PROVIDER", "opencode"),
			DeepThinkModel:  getEnvString("LLM_DEEP_THINK_MODEL", "openai/gpt-6-sol"),
			QuickThinkModel: getEnvString("LLM_QUICK_THINK_MODEL", "openai/gpt-6-luna"),
			RoleModels:      parseRoleModelsLenient(os.Getenv("LLM_ROLE_MODELS")),
			RoleModelsRaw:   os.Getenv("LLM_ROLE_MODELS"),
			Timeout:         llmTimeout,
			DebateTimeout:   llmDebateTimeout,
			Providers: LLMProviderConfigs{
				OpenAI: LLMProviderConfig{
					APIKey:  os.Getenv("OPENAI_API_KEY"),
					BaseURL: os.Getenv("OPENAI_BASE_URL"),
					Model:   getEnvString("OPENAI_MODEL", "gpt-5-mini"),
				},
				Anthropic: LLMProviderConfig{
					APIKey: os.Getenv("ANTHROPIC_API_KEY"),
					Model:  getEnvString("ANTHROPIC_MODEL", "claude-3-7-sonnet-latest"),
				},
				Google: LLMProviderConfig{
					APIKey: os.Getenv("GOOGLE_API_KEY"),
					Model:  getEnvString("GOOGLE_MODEL", "gemini-2.5-flash"),
				},
				OpenRouter: LLMProviderConfig{
					APIKey:  os.Getenv("OPENROUTER_API_KEY"),
					BaseURL: os.Getenv("OPENROUTER_BASE_URL"),
					Model:   getEnvString("OPENROUTER_MODEL", "openai/gpt-4.1-mini"),
				},
				XAI: LLMProviderConfig{
					APIKey:  os.Getenv("XAI_API_KEY"),
					BaseURL: os.Getenv("XAI_BASE_URL"),
					Model:   getEnvString("XAI_MODEL", "grok-3-mini"),
				},
				Ollama: OllamaConfig{
					BaseURL: getEnvString("OLLAMA_BASE_URL", "http://localhost:11434"),
					Model:   getEnvString("OLLAMA_MODEL", "llama3.2"),
					APIKey:  os.Getenv("OLLAMA_API_KEY"),
				},
				OpenCode: OpenCodeConfig{
					BaseURL:  getEnvString("OPENCODE_BASE_URL", "http://localhost:4096"),
					Username: getEnvString("OPENCODE_SERVER_USERNAME", "opencode"),
					Password: os.Getenv("OPENCODE_SERVER_PASSWORD"),
					Model:    getEnvString("OPENCODE_MODEL", "openai/gpt-6-sol"),
				},
			},
			FallbackProvider:     getEnvString("LLM_FALLBACK_PROVIDER", ""),
			FallbackModel:        getEnvString("LLM_FALLBACK_MODEL", ""),
			RetryMaxAttempts:     llmRetryMaxAttempts,
			CallTimeout:          llmCallTimeout,
			BudgetRequestsPerDay: llmBudgetRequestsDay,
			BudgetTokensPerDay:   llmBudgetTokensDay,
			ThrottleConcurrency:  llmThrottleConcurrency,
		},
		Embedding: EmbeddingConfig{
			Model:   getEnvString("EMBEDDING_MODEL", "nomic-embed-text"),
			BaseURL: getEnvString("EMBEDDING_BASE_URL", ""),
			Timeout: embeddingTimeout,
		},
		DataProviders: DataProviderConfigs{
			PolygonBulkSnapshotsEnabled: polygonBulkSnapshotsEnabled,
			Polygon: DataProviderConfig{
				APIKey: os.Getenv("POLYGON_API_KEY"),
			},
			AlphaVantage: DataProviderConfig{
				APIKey:             os.Getenv("ALPHA_VANTAGE_API_KEY"),
				RateLimitPerMinute: alphaVantageRateLimit,
			},
			Finnhub: DataProviderConfig{
				APIKey:             os.Getenv("FINNHUB_API_KEY"),
				RateLimitPerMinute: finnhubRateLimit,
			},
			FMP: DataProviderConfig{
				APIKey:             os.Getenv("FMP_API_KEY"),
				RateLimitPerMinute: fmpRateLimit,
			},
			NewsAPI: DataProviderConfig{
				APIKey: os.Getenv("NEWSAPI_API_KEY"),
			},
			Tradier: TradierConfig{
				APIKey:  os.Getenv("TRADIER_API_KEY"),
				Sandbox: tradierSandbox,
			},
		},
		Polygon: PolygonConnectionConfig{
			RPCURL: os.Getenv("POLYGON_RPC_URL"),
			WSURL:  os.Getenv("POLYGON_WS_URL"),
		},
		Brokers: BrokerConfigs{
			Alpaca: BrokerConfig{
				APIKey:    os.Getenv("ALPACA_API_KEY"),
				APISecret: os.Getenv("ALPACA_API_SECRET"),
				PaperMode: alpacaPaperMode,
			},
			Binance: BrokerConfig{
				APIKey:    os.Getenv("BINANCE_API_KEY"),
				APISecret: os.Getenv("BINANCE_API_SECRET"),
				PaperMode: binancePaperMode,
			},
			Polymarket: PolymarketConfig{
				Address:        firstEnv("POLYMARKET_ADDRESS", "POLYMARKET_WALLET_ADDRESS", "RELAYER_API_KEY_ADDRESS"),
				KeyID:          firstEnv("POLYMARKET_KEY_ID", "POLYMARKET_API_KEY"),
				SecretKey:      firstEnv("POLYMARKET_SECRET_KEY", "POLYMARKET_SECRET"),
				Passphrase:     os.Getenv("POLYMARKET_PASSPHRASE"),
				APIBaseURL:     getEnvString("POLYMARKET_API_BASE_URL", "https://api.polymarket.us"),
				GatewayBaseURL: getEnvString("POLYMARKET_GATEWAY_BASE_URL", "https://gateway.polymarket.us"),
				CLOBURL:        getEnvString("POLYMARKET_CLOB_URL", "https://clob.polymarket.com"),
				SignatureType:  polymarketSignatureType,
			},
			Kalshi: KalshiConfig{
				APIBaseURL:               getEnvString("KALSHI_API_BASE_URL", kalshiAPIBaseURLDefault),
				APIKeyID:                 os.Getenv("KALSHI_API_KEY_ID"),
				PrivateKeyPEMB64:         os.Getenv("KALSHI_PRIVATE_KEY_PEM_B64"),
				Demo:                     kalshiDemo,
				RequestsPerWindow:        kalshiRequestsPerWindow,
				Window:                   kalshiWindow,
				MaxAttempts:              kalshiMaxAttempts,
				BaseBackoff:              kalshiBaseBackoff,
				MaxBackoff:               kalshiMaxBackoff,
				JitterRatio:              kalshiJitterRatio,
				DryRun:                   kalshiDryRun,
				AutoExitsEnabled:         kalshiAutoExitsEnabled,
				SettlementGateThreshold:  kalshiSettlementGateThreshold,
				MarkMaxAge:               kalshiMarkMaxAge,
				ProjectionDatabaseURL:    os.Getenv("KALSHI_PROJECTION_DATABASE_URL"),
				ProjectionKeyID:          os.Getenv("KALSHI_PROJECTION_KEY_ID"),
				ProjectionSecretB64:      os.Getenv("KALSHI_PROJECTION_SECRET_B64"),
				DiscoveryFetchLimit:      kalshiDiscoveryFetchLimit,
				DiscoveryMinVolume:       kalshiDiscoveryMinVolume,
				DiscoveryMinOpenInterest: kalshiDiscoveryMinOpenInterest,
				DiscoveryMaxSpreadPct:    kalshiDiscoveryMaxSpreadPct,
			},
		},
		Paper: PaperConfig{
			EvaluationMode:        domain.PaperEvaluationMode(getEnvString("PAPER_EVALUATION_MODE", string(domain.PaperEvaluationModeScored))),
			InitialCapital:        paperInitialCapital,
			BuyingPowerMultiplier: paperBuyingPowerMultiplier,
			SlippageBPS:           paperSlippageBPS,
			FeePct:                paperFeePct,
		},
		Risk: RiskConfig{
			MaxPositionSizePct:         maxPositionSizePct,
			MaxDailyLossPct:            maxDailyLossPct,
			MaxDrawdownPct:             maxDrawdownPct,
			MaxOpenPositions:           maxOpenPositions,
			MaxKalshiExposurePct:       maxKalshiExposurePct,
			CircuitBreakerThreshold:    circuitBreakerThreshold,
			CircuitBreakerCooldown:     circuitBreakerCooldown,
			RegimeMaxConsecutiveLosses: regimeMaxConsecutiveLosses,
			RegimeMinRollingWinRate:    regimeMinRollingWinRate,
			Polymarket: PolymarketRiskConfig{
				MaxSingleMarketExposurePct: pmMaxSingleExposure,
				MaxTotalExposurePct:        pmMaxTotalExposure,
				MaxPositionUSDC:            pmMaxPositionUSDC,
				MinLiquidity:               pmMinLiquidity,
				MaxSpreadPct:               pmMaxSpreadPct,
				MinDaysToResolution:        pmMinDaysToResolution,
			},
		},
		Notifications: NotificationConfig{
			Telegram: TelegramNotificationConfig{
				BotToken: os.Getenv("NOTIFY_TELEGRAM_BOT_TOKEN"),
				ChatID:   os.Getenv("NOTIFY_TELEGRAM_CHAT_ID"),
			},
			Email: EmailNotificationConfig{
				SMTPHost: os.Getenv("NOTIFY_SMTP_HOST"),
				SMTPPort: smtpPort,
				Username: os.Getenv("NOTIFY_SMTP_USERNAME"),
				Password: os.Getenv("NOTIFY_SMTP_PASSWORD"),
				From:     os.Getenv("NOTIFY_EMAIL_FROM"),
				To:       getEnvCSV("NOTIFY_EMAIL_TO"),
			},
			N8N: WebhookNotificationConfig{
				URL:    os.Getenv("N8N_WEBHOOK_URL"),
				Secret: os.Getenv("N8N_WEBHOOK_SECRET"),
			},
			PagerDuty: WebhookNotificationConfig{
				URL:    os.Getenv("NOTIFY_PAGERDUTY_WEBHOOK_URL"),
				Secret: os.Getenv("NOTIFY_PAGERDUTY_WEBHOOK_SECRET"),
			},
			Discord: DiscordNotificationConfig{
				SignalWebhookURL:   firstNonEmpty(os.Getenv("NOTIFY_DISCORD_SIGNAL_WEBHOOK_URL"), firstNonEmpty(os.Getenv("DISCORD_WEBHOOK_SIGNALS"), os.Getenv("DISCORD_SIGNAL_WEBHOOK_URL"))),
				DecisionWebhookURL: firstNonEmpty(os.Getenv("NOTIFY_DISCORD_DECISION_WEBHOOK_URL"), firstNonEmpty(os.Getenv("DISCORD_WEBHOOK_DECISIONS"), os.Getenv("DISCORD_DECISION_WEBHOOK_URL"))),
				AlertWebhookURL:    firstNonEmpty(os.Getenv("NOTIFY_DISCORD_ALERT_WEBHOOK_URL"), firstNonEmpty(os.Getenv("DISCORD_WEBHOOK_ALERTS"), os.Getenv("DISCORD_ALERT_WEBHOOK_URL"))),
			},
			Alerts: AlertRulesConfig{
				PipelineFailure: PipelineFailureAlertRuleConfig{
					Threshold: pipelineFailureThreshold,
					Channels:  getEnvCSVWithDefault("ALERT_PIPELINE_FAILURE_CHANNELS", []string{"telegram", "email"}),
				},
				CircuitBreaker: ImmediateAlertRuleConfig{
					Channels: getEnvCSVWithDefault("ALERT_CIRCUIT_BREAKER_CHANNELS", []string{"telegram"}),
				},
				LLMProviderDown: LLMProviderDownAlertRuleConfig{
					ErrorRateThreshold: llmProviderDownErrorRateThreshold,
					Window:             llmProviderDownWindow,
					Channels:           getEnvCSVWithDefault("ALERT_LLM_PROVIDER_DOWN_CHANNELS", []string{"telegram"}),
				},
				HighLatency: HighLatencyAlertRuleConfig{
					Threshold: highLatencyThreshold,
					Channels:  getEnvCSVWithDefault("ALERT_HIGH_LATENCY_CHANNELS", []string{"email"}),
				},
				KillSwitch: ImmediateAlertRuleConfig{
					Channels: getEnvCSVWithDefault("ALERT_KILL_SWITCH_CHANNELS", []string{"telegram"}),
				},
				DBConnection: ImmediateAlertRuleConfig{
					Channels: getEnvCSVWithDefault("ALERT_DB_CONNECTION_CHANNELS", []string{"email", "pagerduty"}),
				},
			},
		},
		Features: FeatureFlags{
			EnableScheduler:               enableScheduler,
			SchedulerJobTimeout:           schedulerJobTimeout,
			SchedulerReloadInterval:       schedulerReloadInterval,
			AutomationAutoDisableCooldown: automationAutoDisableCooldown,
			AutomationMissedRunCatchUp:    automationMissedRunCatchUp,
			EnableRedisCache:              enableRedisCache,
			RedisRequired:                 redisRequired,
			EnableLiveTrading:             enableLiveTrading,
			EnableTickerDiscovery:         enableTickerDiscovery,
			EnablePolymarketAutomation:    enablePolymarketAutomation,
		},
		LiveTradingAllowedStrategies: getEnvCSV("LIVE_TRADING_ALLOWED_STRATEGIES"),
		LiveTradingAllowedBrokers:    getEnvCSV("LIVE_TRADING_ALLOWED_BROKERS"),
		TickerDiscovery: TickerDiscoveryConfig{
			Enabled:    enableTickerDiscovery,
			Cron:       getEnvString("TICKER_DISCOVERY_CRON", "30 10 * * 1-5"),
			MinADV:     tickerDiscoveryMinADV,
			MaxTickers: tickerDiscoveryMaxTickers,
		},
		HistoryRefreshWatchlistLimit: historyRefreshWatchlistLimit,
		PortfolioAllocatorMode:       strings.ToLower(strings.TrimSpace(getEnvString("PORTFOLIO_ALLOCATOR_MODE", PortfolioAllocatorModeShadow))),
		ReleaseDrillsVerified:        releaseDrillsVerified,
		AdminAPIKey:                  strings.TrimSpace(os.Getenv("ADMIN_API_KEY")),
		SECEdgar: SECEdgarConfig{
			AppName:  strings.TrimSpace(getEnvString("SEC_EDGAR_APP_NAME", "Augr")),
			AppEmail: strings.TrimSpace(os.Getenv("SEC_EDGAR_APP_EMAIL")),
		},
		StaleRunTTL:          staleRunTTL,
		ShutdownDrainTimeout: shutdownDrainTimeout,
		Deprecations:         deprecations,
	}

	return cfg, nil
}

func getEnvString(key, defaultValue string) string {
	return firstNonEmpty(os.Getenv(key), defaultValue)
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func getEnvInt(key string, defaultValue int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}

	return parsed, nil
}

func getEnvFloat64(key string, defaultValue float64) (float64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}

	return parsed, nil
}

func getEnvBool(key string, defaultValue bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}

	return parsed, nil
}

func getEnvDuration(key string, defaultValue time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}

	return parsed, nil
}

func getEnvCSV(key string) []string {
	return getEnvCSVWithDefault(key, nil)
}

func getEnvCSVWithDefault(key string, defaultValue []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return append([]string(nil), defaultValue...)
	}

	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

package automation

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/PatrickFanella/get-rich-quick/internal/kalshidiscovery"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

var kalshiDiscoverySpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "15 * * * *"}

var kalshiDiscoveryRun = kalshidiscovery.Run

// KalshiDiscoveryConfig holds the operator-tunable discovery settings. Zero
// values fall back to DefaultKalshiDiscoveryConfig. Runtime may populate it
// from config; until that wiring exists the job reads the KALSHI_DISCOVERY_*
// environment variables through KalshiDiscoveryConfigFromEnv.
type KalshiDiscoveryConfig struct {
	FetchLimit      int     // total open markets fetched across catalog pages
	MinVolume       float64 // screener minimum traded contracts
	MinOpenInterest float64 // screener minimum open interest
	MaxSpreadPct    float64 // screener maximum executable spread percent
	MaxDeployments  int
	MinConviction   float64
}

// DefaultKalshiDiscoveryConfig mirrors the previous hard-coded job settings
// except FetchLimit, which now pages past the zero-volume first page.
func DefaultKalshiDiscoveryConfig() KalshiDiscoveryConfig {
	screener := kalshidiscovery.DefaultScreenerConfig()
	return KalshiDiscoveryConfig{
		FetchLimit:      500,
		MinVolume:       screener.MinVolume,
		MinOpenInterest: screener.MinOpenInterest,
		MaxSpreadPct:    screener.MaxSpreadPct,
		MaxDeployments:  1,
		MinConviction:   0.70,
	}
}

// kalshiDiscoveryLookupEnv is swapped in tests.
var kalshiDiscoveryLookupEnv = os.LookupEnv

// KalshiDiscoveryConfigFromEnv reads KALSHI_DISCOVERY_FETCH_LIMIT,
// KALSHI_DISCOVERY_MIN_VOLUME, KALSHI_DISCOVERY_MIN_OPEN_INTEREST and
// KALSHI_DISCOVERY_MAX_SPREAD_PCT over the defaults. Unparseable or
// non-positive values keep the default and are logged.
func KalshiDiscoveryConfigFromEnv(logger *slog.Logger) KalshiDiscoveryConfig {
	cfg := DefaultKalshiDiscoveryConfig()
	if logger == nil {
		logger = slog.Default()
	}
	readInt := func(key string, dst *int) {
		raw, ok := kalshiDiscoveryLookupEnv(key)
		if !ok || strings.TrimSpace(raw) == "" {
			return
		}
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value <= 0 {
			logger.Warn("kalshi_discovery: ignoring invalid env value", slog.String("key", key), slog.String("value", raw))
			return
		}
		*dst = value
	}
	readFloat := func(key string, dst *float64) {
		raw, ok := kalshiDiscoveryLookupEnv(key)
		if !ok || strings.TrimSpace(raw) == "" {
			return
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || value <= 0 {
			logger.Warn("kalshi_discovery: ignoring invalid env value", slog.String("key", key), slog.String("value", raw))
			return
		}
		*dst = value
	}
	readInt("KALSHI_DISCOVERY_FETCH_LIMIT", &cfg.FetchLimit)
	readFloat("KALSHI_DISCOVERY_MIN_VOLUME", &cfg.MinVolume)
	readFloat("KALSHI_DISCOVERY_MIN_OPEN_INTEREST", &cfg.MinOpenInterest)
	readFloat("KALSHI_DISCOVERY_MAX_SPREAD_PCT", &cfg.MaxSpreadPct)
	return cfg
}

func (c KalshiDiscoveryConfig) toRunConfig() kalshidiscovery.Config {
	defaults := DefaultKalshiDiscoveryConfig()
	if c.FetchLimit <= 0 {
		c.FetchLimit = defaults.FetchLimit
	}
	if c.MinVolume <= 0 {
		c.MinVolume = defaults.MinVolume
	}
	if c.MinOpenInterest <= 0 {
		c.MinOpenInterest = defaults.MinOpenInterest
	}
	if c.MaxSpreadPct <= 0 {
		c.MaxSpreadPct = defaults.MaxSpreadPct
	}
	if c.MaxDeployments <= 0 {
		c.MaxDeployments = defaults.MaxDeployments
	}
	if c.MinConviction <= 0 {
		c.MinConviction = defaults.MinConviction
	}
	screener := kalshidiscovery.DefaultScreenerConfig()
	screener.MinVolume = c.MinVolume
	screener.MinOpenInterest = c.MinOpenInterest
	screener.MaxSpreadPct = c.MaxSpreadPct
	return kalshidiscovery.Config{
		DryRun:         false,
		FetchLimit:     c.FetchLimit,
		MaxDeployments: c.MaxDeployments,
		MinConviction:  c.MinConviction,
		Screener:       screener,
	}
}

func (o *JobOrchestrator) registerKalshiDiscoveryJob() {
	if o.deps.KalshiCatalog == nil || o.deps.StrategyRepo == nil || o.deps.KalshiWatchedRepo == nil || o.deps.KalshiMarketSnapshotsRepo == nil {
		return
	}
	o.Register("kalshi_discovery",
		"Auto-generate Kalshi paper strategies from open markets",
		kalshiDiscoverySpec, o.kalshiDiscovery)
}

func (o *JobOrchestrator) kalshiDiscovery(ctx context.Context) error {
	summary := map[string]int{"fetched": 0, "screened": 0, "proposed": 0, "skipped": 0, "deployed": 0, "created": 0, "reused": 0, "errors": 0, "dry_run": 0}
	defer func() { o.SetLastSummary("kalshi_discovery", summary) }()
	if o.deps.KalshiCatalog == nil {
		return fmt.Errorf("kalshi_discovery: catalog client not configured")
	}

	runCfg := KalshiDiscoveryConfigFromEnv(o.logger).toRunConfig()
	res, err := kalshiDiscoveryRun(ctx, runCfg, kalshidiscovery.Deps{
		Catalog:       o.deps.KalshiCatalog,
		Strategies:    o.deps.StrategyRepo,
		Watched:       o.deps.KalshiWatchedRepo,
		Snapshots:     o.deps.KalshiMarketSnapshotsRepo,
		DiscoveryRuns: o.deps.KalshiDiscoveryRuns,
		Logger:        o.logger,
	})
	if err != nil {
		if isKalshiRateLimit(err) {
			o.logger.Warn("kalshi_discovery: provider rate limited; retaining current catalog")
			return fmt.Errorf("kalshi_discovery: provider rate limited: %w", err)
		}
		return err
	}

	if res != nil {
		summary["fetched"] = res.FetchedAll
		summary["screened"] = res.Screened
		summary["proposed"] = res.Proposed
		summary["skipped"] = res.Skipped
		summary["deployed"] = len(res.Deployed)
		summary["rejected"] = res.Rejected
		for _, deployed := range res.Deployed {
			if deployed.Reused {
				summary["reused"]++
			} else {
				summary["created"]++
			}
			o.logger.Info("kalshi_discovery: strategy selected",
				slog.String("strategy_id", deployed.StrategyID.String()),
				slog.String("ticker", deployed.Ticker),
				slog.String("direction", deployed.Direction),
				slog.Float64("conviction", deployed.Conviction),
				slog.Bool("reused", deployed.Reused),
			)
		}
		summary["errors"] = len(res.Errors)
		if res.DryRun {
			summary["dry_run"] = 1
		}
		o.logger.Info("kalshi_discovery: run complete",
			slog.Int("fetched", res.FetchedAll),
			slog.Int("screened", res.Screened),
			slog.Int("proposed", res.Proposed),
			slog.Int("skipped", res.Skipped),
			slog.Int("deployed", len(res.Deployed)),
			slog.Int("created", summary["created"]),
			slog.Int("reused", summary["reused"]),
			slog.Bool("dry_run", res.DryRun),
		)
	}
	return kalshiDiscoveryCompletionError(res != nil, summary["errors"])
}

func kalshiDiscoveryCompletionError(resultPresent bool, errors int) error {
	if !resultPresent {
		return fmt.Errorf("kalshi_discovery: runner returned no result")
	}
	if errors > 0 {
		return fmt.Errorf("kalshi_discovery: completed with %d domain errors", errors)
	}
	return nil
}

func isKalshiRateLimit(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status=429") || strings.Contains(message, "too_many_requests")
}

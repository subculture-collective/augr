package polymarketdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// Config bundles the full set of parameters for one discovery run.
type Config struct {
	Screener         ScreenerConfig
	Generator        GeneratorConfig
	GammaBaseURL     string // optional override
	MaxDeployments   int    // cap strategies created per run (default 3)
	MinConviction    float64
	ScheduleCron     string // ScheduleCron for deployed strategies (default "0 */6 * * *")
	AutoWatchSlug    bool   // also add winning slugs to watched_markets if not present
	DryRun           bool
}

// Deps bundles external dependencies for one discovery run.
type Deps struct {
	LLMProvider           llm.Provider
	Strategies            repository.StrategyRepository
	PolymarketAccountRepo repository.PolymarketAccountRepository
	PolymarketWatchedRepo repository.PolymarketWatchedMarketsRepository
	Logger                *slog.Logger
}

// DeployedStrategy summarises one strategy created by the pipeline.
type DeployedStrategy struct {
	StrategyID uuid.UUID        `json:"strategy_id"`
	Slug       string           `json:"slug"`
	Template   StrategyTemplate `json:"template"`
	Name       string           `json:"name"`
	Direction  string           `json:"direction"`
	Conviction float64          `json:"conviction"`
	Reused     bool             `json:"reused"`
}

// Result summarises one full discovery run.
type Result struct {
	StartedAt   time.Time          `json:"started_at"`
	Duration    time.Duration      `json:"duration"`
	FetchedAll  int                `json:"fetched_all"`
	Screened    int                `json:"screened"`
	Proposed    int                `json:"proposed"`
	Skipped     int                `json:"skipped"`
	Deployed    []DeployedStrategy `json:"deployed"`
	Errors      []string           `json:"errors,omitempty"`
	DryRun      bool               `json:"dry_run"`
}

// Run executes a full polymarket strategy discovery pipeline.
func Run(ctx context.Context, cfg Config, deps Deps) (*Result, error) {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if cfg.MaxDeployments == 0 {
		cfg.MaxDeployments = 3
	}
	if cfg.ScheduleCron == "" {
		cfg.ScheduleCron = "0 */6 * * *"
	}
	if cfg.MinConviction == 0 {
		cfg.MinConviction = 0.45
	}
	if cfg.Screener == (ScreenerConfig{}) {
		cfg.Screener = DefaultScreenerConfig()
	}

	res := &Result{StartedAt: time.Now(), DryRun: cfg.DryRun}
	start := res.StartedAt
	logger := deps.Logger

	if deps.LLMProvider == nil {
		return nil, fmt.Errorf("polymarketdiscovery: LLMProvider required")
	}
	if deps.Strategies == nil {
		return nil, fmt.Errorf("polymarketdiscovery: StrategyRepository required")
	}

	// Step 1: fetch markets.
	markets, err := FetchOpenMarkets(ctx, cfg.GammaBaseURL, cfg.Screener.FetchLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch open markets: %w", err)
	}
	res.FetchedAll = len(markets)

	// Step 2: screen.
	candidates := ScreenMarkets(markets, cfg.Screener)
	res.Screened = len(candidates)
	logger.Info("polymarketdiscovery: candidates screened",
		slog.Int("fetched", res.FetchedAll), slog.Int("kept", res.Screened),
	)

	// Step 3: per-candidate context + LLM proposal.
	genCfg := cfg.Generator
	if genCfg.Provider == nil {
		genCfg.Provider = deps.LLMProvider
	}

	type accepted struct {
		mc       MarketContext
		proposal Proposal
	}
	var accepts []accepted
	for _, m := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mc, ctxErr := BuildMarketContext(ctx, m, deps.PolymarketAccountRepo)
		if ctxErr != nil {
			logger.Warn("polymarketdiscovery: context build failed",
				slog.String("slug", m.Slug), slog.Any("error", ctxErr),
			)
			res.Errors = append(res.Errors, fmt.Sprintf("context %s: %v", m.Slug, ctxErr))
			continue
		}

		prop, propErr := GenerateProposal(ctx, genCfg, mc, logger)
		if propErr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("propose %s: %v", m.Slug, propErr))
			continue
		}
		res.Proposed++
		if prop.Skip {
			res.Skipped++
			logger.Info("polymarketdiscovery: skipped",
				slog.String("slug", m.Slug), slog.String("reason", prop.SkipReason),
			)
			continue
		}
		if prop.Conviction < cfg.MinConviction {
			res.Skipped++
			logger.Info("polymarketdiscovery: below min conviction",
				slog.String("slug", m.Slug),
				slog.Float64("conviction", prop.Conviction),
				slog.Float64("min", cfg.MinConviction),
			)
			continue
		}
		accepts = append(accepts, accepted{mc: mc, proposal: *prop})
		if len(accepts) >= cfg.MaxDeployments {
			break
		}
	}

	// Step 4: deploy each accepted proposal as a paper strategy + thesis.
	for _, a := range accepts {
		dep, depErr := deployStrategy(ctx, cfg, deps, a.mc, a.proposal)
		if depErr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("deploy %s: %v", a.mc.Market.Slug, depErr))
			continue
		}
		res.Deployed = append(res.Deployed, dep)
	}

	res.Duration = time.Since(start)
	logger.Info("polymarketdiscovery: run complete",
		slog.Int("fetched", res.FetchedAll),
		slog.Int("screened", res.Screened),
		slog.Int("proposed", res.Proposed),
		slog.Int("skipped", res.Skipped),
		slog.Int("deployed", len(res.Deployed)),
		slog.Duration("duration", res.Duration),
		slog.Bool("dry_run", cfg.DryRun),
	)
	StoreLastResult(res)
	return res, nil
}

func deployStrategy(
	ctx context.Context,
	cfg Config,
	deps Deps,
	mc MarketContext,
	p Proposal,
) (DeployedStrategy, error) {
	// Build the polymarket-specific strategy config payload. The agent
	// pipeline reads market_type=polymarket; the JSON sub-object below is
	// stored under the discovery_meta key so downstream consumers can audit
	// the source of the strategy without affecting agent.StrategyConfig.
	metaCfg := map[string]any{
		"discovery_meta": map[string]any{
			"source":          "polymarket_discovery",
			"template":        p.Template,
			"direction":       p.Direction,
			"conviction":      p.Conviction,
			"time_horizon":    p.TimeHorizon,
			"entry_price_max": p.EntryPriceMax,
		},
	}
	cfgBytes, err := json.Marshal(metaCfg)
	if err != nil {
		return DeployedStrategy{}, fmt.Errorf("marshal config: %w", err)
	}

	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = fmt.Sprintf("auto: %s %s", p.Template, mc.Market.Slug)
	} else {
		// Prefix so paper auto strategies are easy to identify in the UI.
		if !strings.HasPrefix(name, "auto:") {
			name = "auto: " + name
		}
	}

	strategy := domain.Strategy{
		ID:           uuid.New(),
		Name:         name,
		Description:  p.Summary,
		Ticker:       mc.Market.Slug,
		MarketType:   domain.MarketTypePolymarket,
		IsPaper:      true,
		Status:       domain.StrategyStatusActive,
		ScheduleCron: cfg.ScheduleCron,
		Config:       json.RawMessage(cfgBytes),
	}

	out := DeployedStrategy{
		Slug:       mc.Market.Slug,
		Template:   p.Template,
		Name:       name,
		Direction:  p.Direction,
		Conviction: p.Conviction,
	}

	if cfg.DryRun {
		out.StrategyID = strategy.ID
		return out, nil
	}

	created, isNew, createErr := discovery.CreateOrReusePaperStrategy(ctx, deps.Strategies, strategy)
	if createErr != nil {
		return DeployedStrategy{}, createErr
	}
	out.StrategyID = created.ID
	out.Reused = !isNew

	// Build and persist thesis.
	thesis := agent.Thesis{
		WatchTerms:   p.WatchTerms,
		Summary:      p.Summary,
		Conviction:   p.Conviction,
		Direction:    p.Direction,
		TimeHorizon:  p.TimeHorizon,
		InvalidateIf: p.InvalidateIf,
		GeneratedAt:  time.Now(),
	}
	// Hard-expire the thesis at market end.
	if end := mc.Market.EndTime(); !end.IsZero() {
		thesis.InvalidAfter = &end
	}
	thesisBytes, marshErr := json.Marshal(thesis)
	if marshErr == nil {
		if err := deps.Strategies.UpdateThesis(ctx, created.ID, thesisBytes); err != nil {
			deps.Logger.Warn("polymarketdiscovery: thesis persist failed",
				slog.String("strategy_id", created.ID.String()), slog.Any("error", err),
			)
		}
	}

	// Optionally ensure slug is watched so signal source matches news against it.
	if cfg.AutoWatchSlug && deps.PolymarketWatchedRepo != nil {
		_ = deps.PolymarketWatchedRepo.Add(ctx, &domain.PolymarketWatchedMarket{
			Slug:    mc.Market.Slug,
			Enabled: true,
			AddedAt: time.Now(),
			AddedBy: "polymarket_discovery",
			Note:    fmt.Sprintf("auto-added by discovery run for %s", p.Template),
		})
	}

	return out, nil
}

// lastResult holds the most recent run output for the API to expose.
var (
	lastResultMu sync.RWMutex
	lastResult   *Result
)

// StoreLastResult records the result of the most recent discovery run.
func StoreLastResult(r *Result) {
	lastResultMu.Lock()
	lastResult = r
	lastResultMu.Unlock()
}

// LastResult returns a copy of the most recent discovery run result, or nil.
func LastResult() *Result {
	lastResultMu.RLock()
	defer lastResultMu.RUnlock()
	if lastResult == nil {
		return nil
	}
	cp := *lastResult
	return &cp
}

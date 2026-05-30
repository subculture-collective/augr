package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/polymarketdiscovery"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

const (
	polymarketDiscoveryProposePerChunk = 2
	polymarketDiscoveryChunkTimeout    = 10 * time.Minute
	polymarketDiscoveryMaxRunAge       = 24 * time.Hour
)

type polymarketDiscoveryChunker struct {
	deps            OrchestratorDeps
	progress        repository.PolymarketDiscoveryRunRepository
	logger          *slog.Logger
	proposePerChunk int
	gammaBaseURL    string
}

var (
	polymarketDiscoveryFetchOpenMarkets = polymarketdiscovery.FetchOpenMarkets
	polymarketDiscoveryScreenMarkets    = polymarketdiscovery.ScreenMarkets
	polymarketDiscoveryBuildContext     = polymarketdiscovery.BuildMarketContext
	polymarketDiscoveryGenerateProposal = polymarketdiscovery.GenerateProposal
	polymarketDiscoveryDeployStrategy   = polymarketdiscovery.DeployStrategy
	polymarketDiscoveryStoreLastResult  = polymarketdiscovery.StoreLastResult
)

func newPolymarketDiscoveryChunker(deps OrchestratorDeps, logger *slog.Logger) polymarketDiscoveryChunker {
	if logger == nil {
		logger = slog.Default()
	}
	return polymarketDiscoveryChunker{deps: deps, progress: deps.PolymarketDiscoveryRuns, logger: logger, proposePerChunk: polymarketDiscoveryProposePerChunk}
}

func (c polymarketDiscoveryChunker) RunChunk(ctx context.Context) error {
	if c.progress == nil {
		return fmt.Errorf("polymarket_discovery: progress repository not configured")
	}
	run, err := c.progress.GetActive(ctx)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			run = nil
		} else {
			return err
		}
	}
	if run == nil {
		now := time.Now()
		run = &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhaseScreen, StartedAt: now, UpdatedAt: now}
		if err := c.progress.Create(ctx, run); err != nil {
			return err
		}
	} else if time.Since(run.StartedAt) > polymarketDiscoveryMaxRunAge {
		now := time.Now()
		run.Status = domain.PolymarketDiscoveryStatusFailed
		run.Phase = domain.PolymarketDiscoveryPhaseDone
		run.CompletedAt = &now
		run.UpdatedAt = now
		run.Errors = append(run.Errors, fmt.Sprintf("run exceeded max age of %s", polymarketDiscoveryMaxRunAge))
		if updateErr := c.progress.Update(ctx, run); updateErr != nil {
			return updateErr
		}
		return fmt.Errorf("polymarket_discovery: run exceeded max age of %s", polymarketDiscoveryMaxRunAge)
	}
	chunkCtx, cancel := context.WithTimeout(ctx, polymarketDiscoveryChunkTimeout)
	defer cancel()
	switch run.Phase {
	case domain.PolymarketDiscoveryPhaseScreen:
		return c.runScreen(chunkCtx, run)
	case domain.PolymarketDiscoveryPhasePropose:
		return c.runPropose(chunkCtx, run)
	case domain.PolymarketDiscoveryPhaseDeploy:
		return c.runDeploy(chunkCtx, run)
	case domain.PolymarketDiscoveryPhaseDone:
		return nil
	default:
		return fmt.Errorf("polymarket_discovery: unknown phase %q", run.Phase)
	}
}

func (c polymarketDiscoveryChunker) runScreen(ctx context.Context, run *domain.PolymarketDiscoveryRun) error {
	cfg := polymarketdiscovery.DefaultScreenerConfig()
	markets, err := polymarketDiscoveryFetchOpenMarkets(ctx, c.gammaBaseURL, cfg.FetchLimit)
	if err != nil {
		return err
	}
	run.Candidates = marketsToDiscoveryCandidates(polymarketDiscoveryScreenMarkets(markets, cfg))
	run.Summary.FetchedAll = len(markets)
	run.Summary.Screened = len(run.Candidates)
	run.CandidateIndex = 0
	run.Phase = domain.PolymarketDiscoveryPhasePropose
	run.UpdatedAt = time.Now()
	return c.progress.Update(ctx, run)
}

func (c polymarketDiscoveryChunker) runPropose(ctx context.Context, run *domain.PolymarketDiscoveryRun) error {
	if c.deps.LLMProvider == nil {
		return fmt.Errorf("polymarket_discovery: LLM provider not configured")
	}
	cfg := polymarketdiscovery.Config{Screener: polymarketdiscovery.DefaultScreenerConfig(), Generator: polymarketdiscovery.GeneratorConfig{Provider: c.deps.LLMProvider}, MaxDeployments: 3, MinConviction: 0.45, ScheduleCron: "0 */6 * * *", AutoWatchSlug: true}
	start := run.CandidateIndex
	end := start + c.proposePerChunk
	if end > len(run.Candidates) {
		end = len(run.Candidates)
	}
	acceptedCount := len(run.Accepted)
	for i := start; i < end; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		cand := run.Candidates[i]
		mc, err := discoveryCandidateToMarketContext(cand)
		if err != nil {
			run.Errors = append(run.Errors, fmt.Sprintf("context %s: %v", cand.Slug, err))
		} else {
			mc, err = polymarketDiscoveryBuildContext(ctx, mc.Market, c.deps.PolymarketAccountRepo)
			if err != nil {
				run.Errors = append(run.Errors, fmt.Sprintf("context %s: %v", cand.Slug, err))
			} else {
				prop, err := polymarketDiscoveryGenerateProposal(ctx, cfg.Generator, mc, c.logger)
				if err != nil {
					run.Errors = append(run.Errors, fmt.Sprintf("propose %s: %v", cand.Slug, err))
				} else {
					run.Summary.Proposed++
					if prop.Skip {
						run.Summary.Skipped++
					} else if prop.Conviction < cfg.MinConviction {
						run.Summary.Skipped++
					} else {
						raw, err := json.Marshal(prop)
						if err != nil {
							run.Errors = append(run.Errors, fmt.Sprintf("marshal %s: %v", cand.Slug, err))
						} else {
							run.Accepted = append(run.Accepted, domain.PolymarketDiscoveryAccepted{Candidate: cand, Proposal: raw})
							run.Summary.Accepted++
							acceptedCount++
						}
					}
				}
			}
		}
		run.CandidateIndex = i + 1
		run.UpdatedAt = time.Now()
		if err := c.progress.Update(ctx, run); err != nil {
			return err
		}
		if acceptedCount >= cfg.MaxDeployments {
			break
		}
	}
	if acceptedCount >= cfg.MaxDeployments || run.CandidateIndex >= len(run.Candidates) {
		run.Phase = domain.PolymarketDiscoveryPhaseDeploy
		run.UpdatedAt = time.Now()
		if err := c.progress.Update(ctx, run); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func (c polymarketDiscoveryChunker) runDeploy(ctx context.Context, run *domain.PolymarketDiscoveryRun) error {
	cfg := polymarketdiscovery.Config{Screener: polymarketdiscovery.DefaultScreenerConfig(), Generator: polymarketdiscovery.GeneratorConfig{Provider: c.deps.LLMProvider}, MaxDeployments: 3, MinConviction: 0.45, ScheduleCron: "0 */6 * * *", AutoWatchSlug: true}
	deps := polymarketdiscovery.Deps{LLMProvider: c.deps.LLMProvider, Strategies: c.deps.StrategyRepo, PolymarketAccountRepo: c.deps.PolymarketAccountRepo, PolymarketWatchedRepo: c.deps.PolymarketWatchedRepo, Logger: c.logger}
	deployed := make(map[string]struct{}, len(run.Deployed))
	for _, dep := range run.Deployed {
		deployed[dep.Slug] = struct{}{}
	}
	for _, accepted := range run.Accepted {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, ok := deployed[accepted.Candidate.Slug]; ok {
			continue
		}
		var prop polymarketdiscovery.Proposal
		if err := json.Unmarshal(accepted.Proposal, &prop); err != nil {
			run.Errors = append(run.Errors, fmt.Sprintf("deploy %s: %v", accepted.Candidate.Slug, err))
			run.UpdatedAt = time.Now()
			if err := c.progress.Update(ctx, run); err != nil {
				return err
			}
			continue
		}
		mc, err := discoveryCandidateToMarketContext(accepted.Candidate)
		if err != nil {
			run.Errors = append(run.Errors, fmt.Sprintf("deploy %s: %v", accepted.Candidate.Slug, err))
			run.UpdatedAt = time.Now()
			if err := c.progress.Update(ctx, run); err != nil {
				return err
			}
			continue
		}
		mc, err = polymarketDiscoveryBuildContext(ctx, mc.Market, c.deps.PolymarketAccountRepo)
		if err != nil {
			run.Errors = append(run.Errors, fmt.Sprintf("deploy %s: %v", accepted.Candidate.Slug, err))
			run.UpdatedAt = time.Now()
			if err := c.progress.Update(ctx, run); err != nil {
				return err
			}
			continue
		}
		dep, err := polymarketDiscoveryDeployStrategy(ctx, cfg, deps, mc, prop)
		if err != nil {
			run.Errors = append(run.Errors, fmt.Sprintf("deploy %s: %v", accepted.Candidate.Slug, err))
			run.UpdatedAt = time.Now()
			if err := c.progress.Update(ctx, run); err != nil {
				return err
			}
			continue
		}
		run.Deployed = append(run.Deployed, domain.PolymarketDiscoveryDeployed{StrategyID: dep.StrategyID.String(), Slug: dep.Slug, Template: string(dep.Template), Name: dep.Name, Direction: dep.Direction, Conviction: dep.Conviction, Reused: dep.Reused})
		run.Summary.Deployed++
		deployed[dep.Slug] = struct{}{}
		run.UpdatedAt = time.Now()
		if err := c.progress.Update(ctx, run); err != nil {
			return err
		}
	}
	run.Phase = domain.PolymarketDiscoveryPhaseDone
	run.Status = domain.PolymarketDiscoveryStatusCompleted
	now := time.Now()
	run.CompletedAt = &now
	run.UpdatedAt = now
	if err := c.progress.Update(ctx, run); err != nil {
		return err
	}
	result := &polymarketdiscovery.Result{StartedAt: run.StartedAt, Duration: now.Sub(run.StartedAt), FetchedAll: run.Summary.FetchedAll, Screened: run.Summary.Screened, Proposed: run.Summary.Proposed, Skipped: run.Summary.Skipped, Errors: append([]string(nil), run.Errors...), Deployed: make([]polymarketdiscovery.DeployedStrategy, 0, len(run.Deployed))}
	for _, dep := range run.Deployed {
		result.Deployed = append(result.Deployed, polymarketdiscovery.DeployedStrategy{StrategyID: uuid.MustParse(dep.StrategyID), Slug: dep.Slug, Template: polymarketdiscovery.StrategyTemplate(dep.Template), Name: dep.Name, Direction: dep.Direction, Conviction: dep.Conviction, Reused: dep.Reused})
	}
	polymarketDiscoveryStoreLastResult(result)
	return nil
}

func discoveryCandidateToMarketContext(cand domain.PolymarketDiscoveryCandidate) (polymarketdiscovery.MarketContext, error) {
	if len(cand.RawMarket) > 0 {
		var market polymarketdiscovery.GammaMarket
		if err := json.Unmarshal(cand.RawMarket, &market); err != nil {
			return polymarketdiscovery.MarketContext{}, err
		}
		return polymarketdiscovery.MarketContext{Market: market}, nil
	}
	market, err := polymarketdiscovery.NewGammaMarketFromSnapshot(map[string]any{
		"slug":             cand.Slug,
		"question":         cand.Question,
		"description":      cand.Description,
		"category":         cand.Category,
		"conditionId":      cand.ConditionID,
		"endDate":          cand.EndDate,
		"resolutionSource": cand.ResolutionSource,
		"volume24hr":       cand.Volume24Hr,
		"liquidity":        cand.Liquidity,
		"bestBid":          cand.BestBid,
		"bestAsk":          cand.BestAsk,
		"spread":           cand.Spread,
		"lastTradePrice":   cand.LastTradePrice,
	})
	if err != nil {
		return polymarketdiscovery.MarketContext{}, err
	}
	return polymarketdiscovery.MarketContext{Market: market}, nil
}

func marketsToDiscoveryCandidates(markets []polymarketdiscovery.GammaMarket) []domain.PolymarketDiscoveryCandidate {
	out := make([]domain.PolymarketDiscoveryCandidate, 0, len(markets))
	for _, m := range markets {
		raw, _ := json.Marshal(m)
		cand := domain.PolymarketDiscoveryCandidate{Slug: m.Slug, Question: m.Question, Description: m.Description, Category: m.Category, ConditionID: m.ConditionID, EndDate: m.EndDate, Volume24Hr: m.Volume24HrFloat(), Liquidity: m.LiquidityFloat(), ResolutionSource: m.ResolutionSource, RawMarket: raw}
		if bid, ok := m.BestBidFloat(); ok {
			cand.BestBid = bid
		}
		if ask, ok := m.BestAskFloat(); ok {
			cand.BestAsk = ask
		}
		if spread, ok := m.SpreadFloat(); ok {
			cand.Spread = spread
		}
		if last, ok := m.LastPriceFloat(); ok {
			cand.LastTradePrice = last
		}
		out = append(out, cand)
	}
	return out
}

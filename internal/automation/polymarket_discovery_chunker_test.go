package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	"github.com/PatrickFanella/get-rich-quick/internal/polymarketdiscovery"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type inMemoryPolymarketDiscoveryRunRepo struct {
	run *domain.PolymarketDiscoveryRun
}

func (r *inMemoryPolymarketDiscoveryRunRepo) clone(run *domain.PolymarketDiscoveryRun) *domain.PolymarketDiscoveryRun {
	if run == nil {
		return nil
	}
	cp := *run
	cp.Candidates = append([]domain.PolymarketDiscoveryCandidate(nil), run.Candidates...)
	cp.Accepted = append([]domain.PolymarketDiscoveryAccepted(nil), run.Accepted...)
	cp.Deployed = append([]domain.PolymarketDiscoveryDeployed(nil), run.Deployed...)
	cp.Errors = append([]string(nil), run.Errors...)
	return &cp
}
func (r *inMemoryPolymarketDiscoveryRunRepo) Create(ctx context.Context, run *domain.PolymarketDiscoveryRun) error {
	r.run = r.clone(run)
	return nil
}
func (r *inMemoryPolymarketDiscoveryRunRepo) Get(ctx context.Context, id uuid.UUID) (*domain.PolymarketDiscoveryRun, error) {
	if r.run == nil || r.run.ID != id {
		return nil, repository.ErrNotFound
	}
	return r.clone(r.run), nil
}
func (r *inMemoryPolymarketDiscoveryRunRepo) GetActive(ctx context.Context) (*domain.PolymarketDiscoveryRun, error) {
	if r.run == nil || r.run.Status != domain.PolymarketDiscoveryStatusRunning {
		return nil, repository.ErrNotFound
	}
	return r.clone(r.run), nil
}
func (r *inMemoryPolymarketDiscoveryRunRepo) Update(ctx context.Context, run *domain.PolymarketDiscoveryRun) error {
	r.run = r.clone(run)
	return nil
}
func (r *inMemoryPolymarketDiscoveryRunRepo) ListLatest(ctx context.Context, limit int) ([]domain.PolymarketDiscoveryRun, error) {
	if r.run == nil {
		return nil, nil
	}
	return []domain.PolymarketDiscoveryRun{*r.clone(r.run)}, nil
}

func TestPolymarketDiscoveryChunkerProposeBudget(t *testing.T) {
	defer restoreDiscoverySeams()
	repo := &inMemoryPolymarketDiscoveryRunRepo{run: &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhasePropose, Candidates: makeCandidates(5)}}
	polymarketDiscoveryGenerateProposal = func(ctx context.Context, cfg polymarketdiscovery.GeneratorConfig, mc polymarketdiscovery.MarketContext, logger *slog.Logger) (*polymarketdiscovery.Proposal, error) {
		return &polymarketdiscovery.Proposal{Conviction: 0.9, Name: mc.Market.Slug}, nil
	}
	c := polymarketDiscoveryChunker{progress: repo, deps: OrchestratorDeps{LLMProvider: llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })}, proposePerChunk: 2}
	if err := c.runPropose(context.Background(), repo.run); err != nil {
		t.Fatal(err)
	}
	if repo.run.CandidateIndex != 2 || len(repo.run.Accepted) != 2 || repo.run.Phase != domain.PolymarketDiscoveryPhasePropose {
		t.Fatalf("unexpected run: %+v", repo.run)
	}
}

func TestPolymarketDiscoveryChunkerStopsAtMaxDeployments(t *testing.T) {
	defer restoreDiscoverySeams()
	repo := &inMemoryPolymarketDiscoveryRunRepo{run: &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhasePropose, Candidates: makeCandidates(5)}}
	polymarketDiscoveryGenerateProposal = func(ctx context.Context, cfg polymarketdiscovery.GeneratorConfig, mc polymarketdiscovery.MarketContext, logger *slog.Logger) (*polymarketdiscovery.Proposal, error) {
		return &polymarketdiscovery.Proposal{Conviction: 0.9, Name: mc.Market.Slug}, nil
	}
	c := polymarketDiscoveryChunker{progress: repo, deps: OrchestratorDeps{LLMProvider: llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })}, proposePerChunk: 5}
	if err := c.runPropose(context.Background(), repo.run); err != nil {
		t.Fatal(err)
	}
	if len(repo.run.Accepted) != 3 || repo.run.Phase != domain.PolymarketDiscoveryPhaseDeploy || repo.run.CandidateIndex != 3 {
		t.Fatalf("unexpected run: %+v", repo.run)
	}
}

func TestPolymarketDiscoveryChunkerAdvancesOnErrorsAndSkips(t *testing.T) {
	defer restoreDiscoverySeams()
	repo := &inMemoryPolymarketDiscoveryRunRepo{run: &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhasePropose, Candidates: []domain.PolymarketDiscoveryCandidate{{Slug: "bad-context"}, {Slug: "skip"}, {Slug: "low"}, {Slug: "ok"}}}}
	polymarketDiscoveryGenerateProposal = func(ctx context.Context, cfg polymarketdiscovery.GeneratorConfig, mc polymarketdiscovery.MarketContext, logger *slog.Logger) (*polymarketdiscovery.Proposal, error) {
		switch mc.Market.Slug {
		case "skip":
			return &polymarketdiscovery.Proposal{Skip: true}, nil
		case "low":
			return &polymarketdiscovery.Proposal{Conviction: 0.1}, nil
		default:
			return &polymarketdiscovery.Proposal{Conviction: 0.9, Name: mc.Market.Slug}, nil
		}
	}
	polymarketDiscoveryBuildContext = func(ctx context.Context, m polymarketdiscovery.GammaMarket, repo repository.PolymarketAccountRepository) (polymarketdiscovery.MarketContext, error) {
		if m.Slug == "bad-context" {
			return polymarketdiscovery.MarketContext{}, errors.New("boom")
		}
		return polymarketdiscovery.MarketContext{Market: m}, nil
	}
	c := polymarketDiscoveryChunker{progress: repo, deps: OrchestratorDeps{LLMProvider: llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })}, proposePerChunk: 4}
	if err := c.runPropose(context.Background(), repo.run); err != nil {
		t.Fatal(err)
	}
	if repo.run.CandidateIndex != 4 {
		t.Fatalf("candidate index = %d", repo.run.CandidateIndex)
	}
	if len(repo.run.Errors) == 0 || fmt.Sprint(repo.run.Errors) == "" || !contains(repo.run.Errors, "bad-context") {
		t.Fatalf("errors = %#v", repo.run.Errors)
	}
}

func TestPolymarketDiscoveryChunkerDeployCompletesAndStoresResult(t *testing.T) {
	defer restoreDiscoverySeams()
	repo := &inMemoryPolymarketDiscoveryRunRepo{run: &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhaseDeploy, StartedAt: time.Now().Add(-time.Minute), Accepted: []domain.PolymarketDiscoveryAccepted{{Candidate: domain.PolymarketDiscoveryCandidate{Slug: "aaa"}, Proposal: json.RawMessage(`{"conviction":0.9,"name":"aaa"}`)}}}}
	var stored *polymarketdiscovery.Result
	polymarketDiscoveryDeployStrategy = func(ctx context.Context, cfg polymarketdiscovery.Config, deps polymarketdiscovery.Deps, mc polymarketdiscovery.MarketContext, prop polymarketdiscovery.Proposal) (polymarketdiscovery.DeployedStrategy, error) {
		return polymarketdiscovery.DeployedStrategy{StrategyID: uuid.New(), Slug: mc.Market.Slug, Name: prop.Name, Direction: prop.Direction, Conviction: prop.Conviction}, nil
	}
	polymarketDiscoveryStoreLastResult = func(r *polymarketdiscovery.Result) { stored = r }
	c := polymarketDiscoveryChunker{progress: repo, deps: OrchestratorDeps{LLMProvider: llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })}, logger: slog.Default()}
	if err := c.runDeploy(context.Background(), repo.run); err != nil {
		t.Fatal(err)
	}
	if repo.run.Status != domain.PolymarketDiscoveryStatusCompleted || repo.run.Phase != domain.PolymarketDiscoveryPhaseDone || repo.run.CompletedAt == nil {
		t.Fatalf("run not completed: %+v", repo.run)
	}
	if stored == nil || len(stored.Deployed) != 1 {
		t.Fatalf("stored result = %#v", stored)
	}
}

func TestDiscoveryCandidateToMarketContextPreservesNumericFields(t *testing.T) {
	cand := domain.PolymarketDiscoveryCandidate{Slug: "aaa", Question: "q", Description: "d", Category: "cat", ConditionID: "cid", EndDate: "2026-01-01T00:00:00Z", Volume24Hr: 12.5, Liquidity: 34.5, BestBid: 0.11, BestAsk: 0.22, Spread: 0.11, LastTradePrice: 0.15, ResolutionSource: "source"}
	mc, err := discoveryCandidateToMarketContext(cand)
	if err != nil {
		t.Fatal(err)
	}
	if got := mc.Market.Volume24HrFloat(); got != 12.5 {
		t.Fatalf("Volume24HrFloat=%v", got)
	}
	if got := mc.Market.LiquidityFloat(); got != 34.5 {
		t.Fatalf("LiquidityFloat=%v", got)
	}
	if got, ok := mc.Market.BestBidFloat(); !ok || got != 0.11 {
		t.Fatalf("BestBidFloat=%v %v", got, ok)
	}
	if got, ok := mc.Market.BestAskFloat(); !ok || got != 0.22 {
		t.Fatalf("BestAskFloat=%v %v", got, ok)
	}
	if got, ok := mc.Market.SpreadFloat(); !ok || got != 0.11 {
		t.Fatalf("SpreadFloat=%v %v", got, ok)
	}
	if got, ok := mc.Market.LastPriceFloat(); !ok || got != 0.15 {
		t.Fatalf("LastPriceFloat=%v %v", got, ok)
	}
}

func TestPolymarketDiscoveryChunkerPersistsCandidateErrorsPerAttempt(t *testing.T) {
	defer restoreDiscoverySeams()
	repo := &inMemoryPolymarketDiscoveryRunRepo{run: &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhasePropose, Candidates: []domain.PolymarketDiscoveryCandidate{{Slug: "bad"}}}}
	polymarketDiscoveryBuildContext = func(ctx context.Context, m polymarketdiscovery.GammaMarket, repo repository.PolymarketAccountRepository) (polymarketdiscovery.MarketContext, error) {
		return polymarketdiscovery.MarketContext{}, errors.New("boom")
	}
	c := polymarketDiscoveryChunker{progress: repo, deps: OrchestratorDeps{LLMProvider: llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })}, proposePerChunk: 1}
	if err := c.runPropose(context.Background(), repo.run); err != nil {
		t.Fatal(err)
	}
	if repo.run.CandidateIndex != 1 || len(repo.run.Errors) != 1 {
		t.Fatalf("run not persisted after error: %+v", repo.run)
	}
}

func TestPolymarketDiscoveryChunkerDeploySkipsAlreadyDeployed(t *testing.T) {
	defer restoreDiscoverySeams()
	repo := &inMemoryPolymarketDiscoveryRunRepo{run: &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhaseDeploy, StartedAt: time.Now().Add(-time.Minute), Accepted: []domain.PolymarketDiscoveryAccepted{{Candidate: domain.PolymarketDiscoveryCandidate{Slug: "aaa"}, Proposal: json.RawMessage(`{"conviction":0.9,"name":"aaa"}`)}}, Deployed: []domain.PolymarketDiscoveryDeployed{{StrategyID: uuid.NewString(), Slug: "aaa"}}, Summary: domain.PolymarketDiscoverySummary{Deployed: 1}}}
	called := 0
	polymarketDiscoveryDeployStrategy = func(ctx context.Context, cfg polymarketdiscovery.Config, deps polymarketdiscovery.Deps, mc polymarketdiscovery.MarketContext, prop polymarketdiscovery.Proposal) (polymarketdiscovery.DeployedStrategy, error) {
		called++
		return polymarketdiscovery.DeployedStrategy{}, nil
	}
	c := polymarketDiscoveryChunker{progress: repo, deps: OrchestratorDeps{LLMProvider: llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })}, logger: slog.Default()}
	if err := c.runDeploy(context.Background(), repo.run); err != nil {
		t.Fatal(err)
	}
	if called != 0 || repo.run.Summary.Deployed != 1 || len(repo.run.Deployed) != 1 {
		t.Fatalf("unexpected deploy retry behavior: called=%d run=%+v", called, repo.run)
	}
}

func TestPolymarketDiscoveryChunkerMaxAgeFailsRun(t *testing.T) {
	defer restoreDiscoverySeams()
	started := time.Now().Add(-(polymarketDiscoveryMaxRunAge + time.Minute))
	repo := &inMemoryPolymarketDiscoveryRunRepo{run: &domain.PolymarketDiscoveryRun{ID: uuid.New(), Status: domain.PolymarketDiscoveryStatusRunning, Phase: domain.PolymarketDiscoveryPhasePropose, StartedAt: started}}
	c := polymarketDiscoveryChunker{progress: repo, deps: OrchestratorDeps{LLMProvider: llm.ProviderFunc(func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) { return nil, nil })}, logger: slog.Default()}
	if err := c.RunChunk(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if repo.run.Status != domain.PolymarketDiscoveryStatusFailed || repo.run.Phase != domain.PolymarketDiscoveryPhaseDone {
		t.Fatalf("run not failed: %+v", repo.run)
	}
}

func TestDiscoveryCandidateToMarketContextRehydratesRawMarket(t *testing.T) {
	raw := []byte(`{"slug":"aaa","question":"q","bestBid":"0.11"}`)
	mc, err := discoveryCandidateToMarketContext(domain.PolymarketDiscoveryCandidate{RawMarket: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := mc.Market.BestBidFloat(); !ok || got != 0.11 {
		t.Fatalf("BestBidFloat=%v %v", got, ok)
	}
	if _, ok := mc.Market.BestAskFloat(); ok {
		t.Fatal("expected absent BestAskFloat")
	}
	if _, ok := mc.Market.SpreadFloat(); ok {
		t.Fatal("expected absent SpreadFloat")
	}
	if _, ok := mc.Market.LastPriceFloat(); ok {
		t.Fatal("expected absent LastPriceFloat")
	}
}

func restoreDiscoverySeams() {
	polymarketDiscoveryFetchOpenMarkets = polymarketdiscovery.FetchOpenMarkets
	polymarketDiscoveryScreenMarkets = polymarketdiscovery.ScreenMarkets
	polymarketDiscoveryBuildContext = polymarketdiscovery.BuildMarketContext
	polymarketDiscoveryGenerateProposal = polymarketdiscovery.GenerateProposal
	polymarketDiscoveryDeployStrategy = polymarketdiscovery.DeployStrategy
	polymarketDiscoveryStoreLastResult = polymarketdiscovery.StoreLastResult
}

func makeCandidates(n int) []domain.PolymarketDiscoveryCandidate {
	out := make([]domain.PolymarketDiscoveryCandidate, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.PolymarketDiscoveryCandidate{Slug: fmt.Sprintf("cand-%d", i), Question: "q"})
	}
	return out
}
func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want || strings.Contains(s, want) {
			return true
		}
	}
	return false
}

package automation

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

// breakerRiskStateStub is a canonical risk-state source that also exposes the
// breaker, ladder, and closed-position surfaces the allocator job wires.
type breakerRiskStateStub struct {
	portfolioRiskStateStub
	mu       sync.Mutex
	breakers map[string]domain.RiskBreakerState
	results  []pgrepo.StrategyTradeResult
	steps    map[uuid.UUID]float64
	metrics  []string
}

func (s *breakerRiskStateStub) Trip(_ context.Context, scope, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.breakers == nil {
		s.breakers = map[string]domain.RiskBreakerState{}
	}
	s.breakers[scope] = domain.RiskBreakerState{Scope: scope, Reason: reason, TrippedAt: at}
	return nil
}

func (s *breakerRiskStateStub) Reset(_ context.Context, scope string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.breakers[scope]
	state.ResetAt = &at
	s.breakers[scope] = state
	return nil
}

func (s *breakerRiskStateStub) Get(_ context.Context, scope string) (*domain.RiskBreakerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.breakers[scope]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &state, nil
}

func (s *breakerRiskStateStub) ListTripped(context.Context) ([]domain.RiskBreakerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.RiskBreakerState, 0, len(s.breakers))
	for _, state := range s.breakers {
		out = append(out, state)
	}
	return out, nil
}

func (s *breakerRiskStateStub) RecentStrategyTradeResults(context.Context, time.Time, int) ([]pgrepo.StrategyTradeResult, error) {
	return s.results, nil
}

func (s *breakerRiskStateStub) CapitalLadderSteps(context.Context, []uuid.UUID) (map[uuid.UUID]float64, error) {
	return s.steps, nil
}

func (s *breakerRiskStateStub) UpdateMetrics(_ context.Context, strategyID string, _, _, _ float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = append(s.metrics, strategyID)
	return nil
}

func (s *breakerRiskStateStub) open(scope string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.breakers[scope]
	return ok && state.ResetAt == nil
}

func TestPortfolioAllocatorScheduleCoversRegularSession(t *testing.T) {
	t.Parallel()
	if portfolioAllocatorSpec.Type != scheduler.ScheduleTypeCron || portfolioAllocatorSpec.Cron != "15,45 9-20 * * 1-5" {
		t.Fatalf("spec = %+v, want cron covering 09:15-20:45 ET", portfolioAllocatorSpec)
	}
	if !portfolioAllocatorSpec.SkipWeekends || !portfolioAllocatorSpec.SkipHolidays {
		t.Fatal("allocator must still skip weekends and holidays")
	}
}

func TestPortfolioAllocatorShadowModeWarnsWhenSelectionsAreNotSubmitted(t *testing.T) {
	t.Parallel()
	now := time.Now()
	opportunityRepo := &portfolioAllocatorOpportunityRepo{items: []domain.Opportunity{{
		ID: uuid.New(), StrategyID: uuid.New(), Status: domain.OpportunityStatusQueued, MarketType: domain.MarketTypeStock,
		Ticker: "AAPL", Side: domain.OrderSideBuy, Signal: domain.PipelineSignalBuy, Confidence: 1, EdgePct: 0.05,
		ExpectedReturnPct: 0.1, MaxLossPct: 0.01, LiquidityUSD: 5_000_000, MarketCapUSD: 10_000_000_000, SpreadPct: 0.001,
		ProposedNotional: 2_000, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now.Add(-time.Hour), DedupeKey: "aapl-warn",
	}}}
	var buffer bytes.Buffer
	orch := NewJobOrchestrator(OrchestratorDeps{
		OpportunityRepo: opportunityRepo, AllocationDecisionRepo: &portfolioAllocatorDecisionRepo{},
		RunRepo: persistedRunsForOpportunities(opportunityRepo.items), PortfolioAccountSnapshot: allocatorSnapshotSource(), PortfolioRiskState: allocatorRiskStateSource(),
	})
	orch.logger = slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelWarn}))
	orch.registerPortfolioAllocatorJobs()
	if err := orch.jobs["portfolio_allocator"].Fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buffer.String(), "shadow mode: 1 opportunities selected, none submitted") {
		t.Fatalf("expected shadow warning, got %q", buffer.String())
	}
}

func TestBuildPortfolioAllocatorStateCountsPositionsMissingMarkAndStrategyBreakers(t *testing.T) {
	t.Parallel()
	mark := 120.0
	strategyID := uuid.New()
	positionRepo := newRecordingPositionRepo(
		&domain.Position{Ticker: "AAPL", MarketType: domain.MarketTypeStock, AssetClass: domain.AssetClassEquity, Quantity: 10, AvgEntry: 100},
		&domain.Position{Ticker: "MSFT", MarketType: domain.MarketTypeStock, AssetClass: domain.AssetClassEquity, Quantity: 1, AvgEntry: 100, CurrentPrice: &mark},
	)
	stub := &breakerRiskStateStub{
		portfolioRiskStateStub: portfolioRiskStateStub{state: portfolio.RuntimeRiskState{OpenBreakerScopes: []string{domain.RiskBreakerScopeStrategy(strategyID.String()), "strategy:not-a-uuid"}}},
		steps:                  map[uuid.UUID]float64{strategyID: 0.5},
	}
	orch := NewJobOrchestrator(OrchestratorDeps{PositionRepo: positionRepo, PortfolioAccountSnapshot: allocatorSnapshotSource(), PortfolioRiskState: stub})
	state, warnings, err := orch.buildPortfolioAllocatorState(context.Background(), portfolio.AllocatorModeShadow, []domain.Opportunity{{StrategyID: strategyID}})
	if err != nil {
		t.Fatal(err)
	}
	if state.PositionsMissingMark != 1 || state.GrossExposure != 1120 {
		t.Fatalf("state = %+v", state)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], portfolio.WarningPositionsMissingMark) {
		t.Fatalf("warnings = %v", warnings)
	}
	if !state.StrategyBreakerOpen[strategyID] || len(state.StrategyBreakerOpen) != 1 {
		t.Fatalf("strategy breakers = %v", state.StrategyBreakerOpen)
	}
	if state.StrategyStepPct[strategyID] != 0.5 {
		t.Fatalf("ladder steps = %v", state.StrategyStepPct)
	}
}

func TestEvaluatePortfolioBreakersTripsGlobalAndStrategyScopes(t *testing.T) {
	t.Parallel()
	losing := uuid.New()
	winning := uuid.New()
	now := time.Now().UTC()
	stub := &breakerRiskStateStub{results: []pgrepo.StrategyTradeResult{
		{StrategyID: losing, RealizedPnL: -1, ClosedAt: now.Add(-3 * time.Hour)},
		{StrategyID: winning, RealizedPnL: 5, ClosedAt: now.Add(-3 * time.Hour)},
		{StrategyID: losing, RealizedPnL: -2, ClosedAt: now.Add(-2 * time.Hour)},
		{StrategyID: losing, RealizedPnL: -3, ClosedAt: now.Add(-time.Hour)},
	}}
	orch := NewJobOrchestrator(OrchestratorDeps{PortfolioRiskState: stub})
	cfg := portfolio.DefaultAllocatorConfig()
	state := portfolio.PortfolioState{Equity: 100000, DailyLossPct: cfg.MaxDailyLossPct + 0.01}
	warnings, _, err := orch.evaluatePortfolioBreakers(context.Background(), &state, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if !state.CircuitBreakerOpen || !stub.open(domain.RiskBreakerScopeGlobal) {
		t.Fatalf("daily loss must trip the global breaker: %+v %v", state, stub.breakers)
	}
	if !state.StrategyBreakerOpen[losing] || state.StrategyBreakerOpen[winning] || !stub.open(domain.RiskBreakerScopeStrategy(losing.String())) {
		t.Fatalf("consecutive losses must trip only the losing strategy: %v", stub.breakers)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v", warnings)
	}

	// An operator reset inside the replay window must not be reopened.
	resetAt := now.Add(-time.Minute)
	stub.breakers[domain.RiskBreakerScopeStrategy(losing.String())] = domain.RiskBreakerState{Scope: domain.RiskBreakerScopeStrategy(losing.String()), ResetAt: &resetAt}
	stub.breakers[domain.RiskBreakerScopeGlobal] = domain.RiskBreakerState{Scope: domain.RiskBreakerScopeGlobal, ResetAt: &resetAt}
	state = portfolio.PortfolioState{Equity: 100000}
	if _, _, err := orch.evaluatePortfolioBreakers(context.Background(), &state, cfg, now); err != nil {
		t.Fatal(err)
	}
	if stub.open(domain.RiskBreakerScopeStrategy(losing.String())) || state.CircuitBreakerOpen {
		t.Fatal("replaying history must not reopen a recently reset breaker")
	}

	// Rolling drawdown beyond the reviewed limit trips the global scope.
	state = portfolio.PortfolioState{Equity: 100000, DrawdownPct: cfg.MaxDrawdownPct}
	old := now.Add(-30 * 24 * time.Hour)
	stub.breakers[domain.RiskBreakerScopeGlobal] = domain.RiskBreakerState{Scope: domain.RiskBreakerScopeGlobal, ResetAt: &old}
	if _, _, err := orch.evaluatePortfolioBreakers(context.Background(), &state, cfg, now); err != nil {
		t.Fatal(err)
	}
	if !state.CircuitBreakerOpen || !stub.open(domain.RiskBreakerScopeGlobal) {
		t.Fatal("drawdown limit must trip the global breaker after an old reset")
	}
}

func TestEvaluatePortfolioBreakersSkipsSourcesWithoutBreakerSupport(t *testing.T) {
	t.Parallel()
	orch := NewJobOrchestrator(OrchestratorDeps{PortfolioRiskState: allocatorRiskStateSource()})
	state := portfolio.PortfolioState{Equity: 100000, DailyLossPct: 1}
	warnings, _, err := orch.evaluatePortfolioBreakers(context.Background(), &state, portfolio.DefaultAllocatorConfig(), time.Now())
	if err != nil || len(warnings) != 0 || state.CircuitBreakerOpen {
		t.Fatalf("plain risk-state source must be a no-op: %v %v %+v", err, warnings, state)
	}
}

func TestRecordPortfolioLadderMetricsOnlyForLadderedStrategies(t *testing.T) {
	t.Parallel()
	laddered, plain := uuid.New(), uuid.New()
	stub := &breakerRiskStateStub{results: []pgrepo.StrategyTradeResult{{StrategyID: laddered, RealizedPnL: 3}, {StrategyID: laddered, RealizedPnL: -1}}}
	orch := NewJobOrchestrator(OrchestratorDeps{PortfolioRiskState: stub})
	state := portfolio.PortfolioState{DrawdownPct: 0.02, StrategyStepPct: map[uuid.UUID]float64{laddered: 0.5}}
	decisions := []domain.AllocationDecision{
		{StrategyID: &laddered, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionExecuted},
		{StrategyID: &laddered, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionExecutionRejected},
		{StrategyID: &plain, Mode: domain.AllocationDecisionModePaper, Action: domain.AllocationDecisionActionExecuted},
		{StrategyID: &laddered, Mode: domain.AllocationDecisionModeShadow, Action: domain.AllocationDecisionActionShadowSelected},
	}
	if err := orch.recordPortfolioLadderMetrics(context.Background(), decisions, state, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(stub.metrics) != 1 || stub.metrics[0] != laddered.String() {
		t.Fatalf("metrics recorded for %v, want only %s", stub.metrics, laddered)
	}
}

func TestPortfolioPositionExposureReportsMissingMark(t *testing.T) {
	t.Parallel()
	mark := 12.0
	if exposure, marked := portfolioPositionExposure(domain.Position{Quantity: 2, AvgEntry: 10}); exposure != 20 || marked {
		t.Fatalf("entry fallback = %v marked=%v", exposure, marked)
	}
	if exposure, marked := portfolioPositionExposure(domain.Position{Quantity: 2, AvgEntry: 10, CurrentPrice: &mark}); exposure != 24 || !marked {
		t.Fatalf("marked = %v marked=%v", exposure, marked)
	}
}

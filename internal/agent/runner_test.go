package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
)

type runnerSpyPersister struct {
	mu           sync.Mutex
	runs         map[uuid.UUID]domain.PipelineRun
	decisions    map[uuid.UUID][]persistedDecision
	completeErr  error
	startErr     error
	eventErr     error
	startHook    func(*domain.PipelineRun)
	eventHook    func(context.Context, *domain.AgentEvent)
	receipt      *repository.PipelineRunFinalizationReceipt
	finalizeHook func(context.Context, repository.PipelineRunFinalization) error
}

type persistedDecision struct {
	role  AgentRole
	phase Phase
	round *int
	text  string
}

func newRunnerSpyPersister() *runnerSpyPersister {
	return &runnerSpyPersister{
		runs:      make(map[uuid.UUID]domain.PipelineRun),
		decisions: make(map[uuid.UUID][]persistedDecision),
	}
}

func (p *runnerSpyPersister) RecordRunStart(_ context.Context, run *domain.PipelineRun) error {
	if p.startHook != nil {
		p.startHook(run)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := *run
	p.runs[run.ID] = cp
	return p.startErr
}

func TestRunnerPersistenceErrorsPreserveRecognizedRunControlCause(t *testing.T) {
	for _, cause := range []runcontrol.Cause{runcontrol.Operator, runcontrol.Shutdown, runcontrol.KillSwitch} {
		cause := cause
		for _, stage := range []string{"start", "completed terminal"} {
			stage := stage
			t.Run(string(cause)+"/"+stage, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(nil)
				persister := newRunnerSpyPersister()
				switch stage {
				case "start":
					persister.startHook = func(*domain.PipelineRun) { cancel(cause) }
					persister.startErr = errors.New("start persistence failed")
				case "completed terminal":
					persister.finalizeHook = func(context.Context, repository.PipelineRunFinalization) error {
						cancel(cause)
						return errors.New("terminal persistence failed")
					}
				}
				runner := NewRunner(Definition{}, Dependencies{Persister: persister})
				prepared := PreparedRun{Strategy: domain.Strategy{ID: uuid.New(), Ticker: "TEST"}, Runtime: RuntimeConfig{SkipPhases: map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseTrading: true, PhaseRiskDebate: true, PhaseExecutionGate: true}}}

				_, err := runner.Run(ctx, prepared)
				if !errors.Is(err, cause) || !strings.Contains(err.Error(), "persistence failed") {
					t.Fatalf("Run() error = %v, want persistence error matching %v", err, cause)
				}
			})
		}
	}
}

func (p *runnerSpyPersister) FinalizeRun(ctx context.Context, runID uuid.UUID, _ time.Time, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finalizeHook != nil {
		if err := p.finalizeHook(ctx, finalization); err != nil {
			return repository.PipelineRunFinalizationReceipt{}, err
		}
	}
	if p.completeErr != nil {
		return repository.PipelineRunFinalizationReceipt{}, p.completeErr
	}
	if p.eventErr != nil {
		return repository.PipelineRunFinalizationReceipt{}, p.eventErr
	}
	if p.receipt != nil {
		return *p.receipt, nil
	}
	run := p.runs[runID]
	if run.Status != domain.PipelineStatusRunning {
		return repository.PipelineRunFinalizationReceipt{Run: run}, nil
	}
	run.Status = finalization.Status
	run.CompletedAt = &finalization.CompletedAt
	run.ErrorMessage = finalization.ErrorMessage
	if finalization.Signal != nil {
		run.Signal = *finalization.Signal
	}
	p.runs[runID] = run
	return repository.PipelineRunFinalizationReceipt{Applied: true, Run: run}, nil
}

func TestRunnerCancellationDuringCompletedFinalizeWins(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	persister := newRunnerSpyPersister()
	persister.finalizeHook = func(finalizeCtx context.Context, finalization repository.PipelineRunFinalization) error {
		if finalization.Status != domain.PipelineStatusCompleted {
			return nil
		}
		cancel(runcontrol.Operator)
		<-finalizeCtx.Done()
		return finalizeCtx.Err()
	}
	runner := NewRunner(Definition{}, Dependencies{Persister: persister})
	result, err := runner.Run(ctx, PreparedRun{Strategy: domain.Strategy{ID: uuid.New(), Ticker: "TEST"}, Runtime: RuntimeConfig{SkipPhases: map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseTrading: true, PhaseRiskDebate: true, PhaseExecutionGate: true}}})
	if !errors.Is(err, runcontrol.Operator) || result == nil || result.Run.Status != domain.PipelineStatusCancelled || !result.TerminalApplied {
		t.Fatalf("Run() = (%+v, %v), want applied operator cancellation", result, err)
	}
}

func TestRunnerRegistersBeforeDurableStart(t *testing.T) {
	registry := NewRunContextRegistry()
	persister := newRunnerSpyPersister()
	persister.startHook = func(run *domain.PipelineRun) {
		if err := registry.Register(run.ID, run.TradeDate, func(error) {}); !errors.Is(err, ErrRunAlreadyRegistered) {
			t.Fatalf("Register() during durable start = %v, want ErrRunAlreadyRegistered", err)
		}
	}
	runner := NewRunner(Definition{}, Dependencies{Persister: persister, RunRegistry: registry})
	result, err := runner.Run(context.Background(), PreparedRun{Strategy: domain.Strategy{ID: uuid.New(), Ticker: "TEST"}, Runtime: RuntimeConfig{SkipPhases: map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseTrading: true, PhaseRiskDebate: true, PhaseExecutionGate: true}}})
	if err != nil || result.Run.Status != domain.PipelineStatusCompleted {
		t.Fatalf("Run() = (%+v, %v), want completed", result, err)
	}
}

func TestRunnerSuccessCASLoserReturnsCanonicalWinner(t *testing.T) {
	for _, status := range []domain.PipelineStatus{domain.PipelineStatusCancelled, domain.PipelineStatusCompleted} {
		t.Run(status.String(), func(t *testing.T) {
			persister := newRunnerSpyPersister()
			winner := domain.PipelineRun{ID: uuid.New(), TradeDate: time.Now().UTC(), Status: status, Signal: domain.PipelineSignalHold}
			persister.receipt = &repository.PipelineRunFinalizationReceipt{Run: winner}
			runner := NewRunner(Definition{}, Dependencies{Persister: persister})
			result, err := runner.Run(context.Background(), PreparedRun{Strategy: domain.Strategy{ID: uuid.New(), Ticker: "TEST"}, Runtime: RuntimeConfig{SkipPhases: map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseTrading: true, PhaseRiskDebate: true, PhaseExecutionGate: true}}})
			if !errors.Is(err, ErrLostTerminalAuthority) {
				t.Fatalf("Run() error = %v, want ErrLostTerminalAuthority", err)
			}
			if result == nil || result.Run.Status != winner.Status || result.Run.ID != winner.ID {
				t.Fatalf("Run() result = %+v, want canonical winner %+v", result, winner)
			}
			if result.TerminalApplied {
				t.Fatal("CAS loser reported terminal result as applied")
			}
		})
	}
}

func TestRunnerOperatorCancellationReceiptWinsBeforeRegistryCancellation(t *testing.T) {
	persister := newRunnerSpyPersister()
	winner := domain.PipelineRun{
		ID:           uuid.New(),
		TradeDate:    time.Now().UTC(),
		Status:       domain.PipelineStatusCancelled,
		Signal:       domain.PipelineSignalHold,
		ErrorMessage: runcontrol.Operator.Error(),
	}
	persister.receipt = &repository.PipelineRunFinalizationReceipt{Run: winner}
	runner := NewRunner(Definition{}, Dependencies{Persister: persister, RunRegistry: NewRunContextRegistry()})

	result, err := runner.Run(context.Background(), PreparedRun{Strategy: domain.Strategy{ID: uuid.New(), Ticker: "TEST"}, Runtime: RuntimeConfig{SkipPhases: map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseTrading: true, PhaseRiskDebate: true, PhaseExecutionGate: true}}})
	if !errors.Is(err, ErrLostTerminalAuthority) || !errors.Is(err, runcontrol.Operator) {
		t.Fatalf("Run() error = %v, want ErrLostTerminalAuthority and Operator", err)
	}
	if result == nil || result.Run.ID != winner.ID || result.Run.Status != winner.Status || result.TerminalApplied {
		t.Fatalf("Run() result = %+v, want canonical cancellation loser", result)
	}
}

func (*runnerSpyPersister) SupportsSnapshots() bool { return false }
func (*runnerSpyPersister) PersistSnapshot(context.Context, *domain.PipelineRunSnapshot) error {
	return nil
}

func (p *runnerSpyPersister) PersistEvent(ctx context.Context, event *domain.AgentEvent) error {
	if p.eventHook != nil {
		p.eventHook(ctx, event)
	}
	return p.eventErr
}

func TestRunnerRun_FinalPhaseCancellationCannotComplete(t *testing.T) {
	tests := []struct {
		name       string
		newContext func() (context.Context, func())
		wantStatus domain.PipelineStatus
		wantCause  error
	}{
		{name: "operator", newContext: func() (context.Context, func()) {
			ctx, cancel := context.WithCancelCause(context.Background())
			return ctx, func() { cancel(runcontrol.Operator) }
		}, wantStatus: domain.PipelineStatusCancelled, wantCause: runcontrol.Operator},
		{name: "shutdown", newContext: func() (context.Context, func()) {
			ctx, cancel := context.WithCancelCause(context.Background())
			return ctx, func() { cancel(runcontrol.Shutdown) }
		}, wantStatus: domain.PipelineStatusCancelled, wantCause: runcontrol.Shutdown},
		{name: "kill switch", newContext: func() (context.Context, func()) {
			ctx, cancel := context.WithCancelCause(context.Background())
			return ctx, func() { cancel(runcontrol.KillSwitch) }
		}, wantStatus: domain.PipelineStatusCancelled, wantCause: runcontrol.KillSwitch},
		{name: "bare cancellation", newContext: func() (context.Context, func()) {
			ctx, cancel := context.WithCancel(context.Background())
			return ctx, cancel
		}, wantStatus: domain.PipelineStatusFailed},
		{name: "deadline", newContext: func() (context.Context, func()) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			return ctx, func() { <-ctx.Done(); cancel() }
		}, wantStatus: domain.PipelineStatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancelAtCompletion := test.newContext()
			persister := newRunnerSpyPersister()
			persister.eventHook = func(_ context.Context, event *domain.AgentEvent) {
				if event.EventKind == AgentEventKindPhaseCompleted.String() {
					cancelAtCompletion()
				}
			}
			runner := NewRunner(Definition{}, Dependencies{Persister: persister})
			prepared := PreparedRun{Strategy: domain.Strategy{ID: uuid.New(), Ticker: "TEST"}, Runtime: RuntimeConfig{SkipPhases: map[Phase]bool{PhaseAnalysis: true, PhaseResearchDebate: true, PhaseTrading: true, PhaseRiskDebate: true}}}
			result, err := runner.Run(ctx, prepared)
			if err == nil {
				t.Fatal("Run() error = nil, want terminal context error")
			}
			if result.Run.Status != test.wantStatus || !result.TerminalApplied {
				t.Fatalf("Run() result = %+v, want status %s applied", result, test.wantStatus)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("Run() error = %v, want cause %v", err, test.wantCause)
			}
		})
	}
}

func (p *runnerSpyPersister) PersistDecision(_ context.Context, runID uuid.UUID, node Node, roundNumber *int, output string, _ *DecisionLLMResponse) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.decisions[runID] = append(p.decisions[runID], persistedDecision{role: node.Role(), phase: node.Phase(), round: cloneRoundNumber(roundNumber), text: output})
	return nil
}

func (p *runnerSpyPersister) decisionCount(runID uuid.UUID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.decisions[runID])
}

type stubAnalysisAgent struct {
	name string
	role AgentRole
	fn   func(context.Context, AnalysisInput) (AnalysisOutput, error)
}

func (a stubAnalysisAgent) Name() string    { return a.name }
func (a stubAnalysisAgent) Role() AgentRole { return a.role }
func (a stubAnalysisAgent) Analyze(ctx context.Context, input AnalysisInput) (AnalysisOutput, error) {
	return a.fn(ctx, input)
}

type stubDebateAgent struct {
	name string
	role AgentRole
	fn   func(context.Context, DebateInput) (DebateOutput, error)
}

func (a stubDebateAgent) Name() string    { return a.name }
func (a stubDebateAgent) Role() AgentRole { return a.role }
func (a stubDebateAgent) Debate(ctx context.Context, input DebateInput) (DebateOutput, error) {
	return a.fn(ctx, input)
}

type stubResearchJudge struct {
	name string
	role AgentRole
	fn   func(context.Context, DebateInput) (ResearchJudgeOutput, error)
}

func (j stubResearchJudge) Name() string    { return j.name }
func (j stubResearchJudge) Role() AgentRole { return j.role }
func (j stubResearchJudge) JudgeResearch(ctx context.Context, input DebateInput) (ResearchJudgeOutput, error) {
	return j.fn(ctx, input)
}

type stubTradeAgent struct {
	name string
	role AgentRole
	fn   func(context.Context, TradingInput) (TradingOutput, error)
}

func (a stubTradeAgent) Name() string    { return a.name }
func (a stubTradeAgent) Role() AgentRole { return a.role }
func (a stubTradeAgent) Trade(ctx context.Context, input TradingInput) (TradingOutput, error) {
	return a.fn(ctx, input)
}

type stubRiskJudge struct {
	name string
	role AgentRole
	fn   func(context.Context, RiskJudgeInput) (RiskJudgeOutput, error)
}

func (j stubRiskJudge) Name() string    { return j.name }
func (j stubRiskJudge) Role() AgentRole { return j.role }
func (j stubRiskJudge) JudgeRisk(ctx context.Context, input RiskJudgeInput) (RiskJudgeOutput, error) {
	return j.fn(ctx, input)
}

func defaultRunnerDefinition() Definition {
	return Definition{
		Analysis: []AnalysisAgent{
			stubAnalysisAgent{name: "market", role: AgentRoleMarketAnalyst, fn: func(context.Context, AnalysisInput) (AnalysisOutput, error) {
				return AnalysisOutput{Report: "market-report"}, nil
			}},
		},
		Research: ResearchDebateStage{
			Debaters: []DebateAgent{
				stubDebateAgent{name: "bull", role: AgentRoleBullResearcher, fn: func(_ context.Context, input DebateInput) (DebateOutput, error) {
					return DebateOutput{Contribution: input.Ticker + "-bull"}, nil
				}},
			},
			Judge: stubResearchJudge{name: "judge", role: AgentRoleInvestJudge, fn: func(_ context.Context, input DebateInput) (ResearchJudgeOutput, error) {
				return ResearchJudgeOutput{InvestmentPlan: input.Ticker + "-plan"}, nil
			}},
		},
		Trader: stubTradeAgent{name: "trader", role: AgentRoleTrader, fn: func(_ context.Context, input TradingInput) (TradingOutput, error) {
			plan := TradingPlan{Action: PipelineSignalBuy, Ticker: input.Ticker, EntryType: "market", EntryPrice: 100, PositionSize: 10, StopLoss: 95, TakeProfit: 110, TimeHorizon: "swing", Confidence: 0.8, Rationale: "test", RiskReward: 2}
			payload, _ := json.Marshal(plan)
			return TradingOutput{Plan: plan, StoredOutput: string(payload)}, nil
		}},
		Risk: RiskDebateStage{
			Debaters: []DebateAgent{
				stubDebateAgent{name: "risk", role: AgentRoleAggressiveAnalyst, fn: func(_ context.Context, input DebateInput) (DebateOutput, error) {
					return DebateOutput{Contribution: input.Ticker + "-risk"}, nil
				}},
			},
			Judge: stubRiskJudge{name: "risk-manager", role: AgentRoleRiskManager, fn: func(_ context.Context, input RiskJudgeInput) (RiskJudgeOutput, error) {
				plan := input.TradingPlan
				plan.PositionSize = 5
				return RiskJudgeOutput{FinalSignal: FinalSignal{Signal: PipelineSignalBuy, Confidence: 0.9}, StoredSignal: `{"action":"buy"}`, TradingPlan: plan}, nil
			}},
		},
	}
}

func strategyWithDebateRounds(t *testing.T, ticker string, rounds int) domain.Strategy {
	t.Helper()
	cfg, err := json.Marshal(StrategyConfig{PipelineConfig: &StrategyPipelineConfig{DebateRounds: &rounds}})
	if err != nil {
		t.Fatalf("marshal strategy config: %v", err)
	}
	return domain.Strategy{ID: uuid.New(), Ticker: ticker, Config: cfg}
}

func TestRunnerPrepare_ResolvesRuntimeFromStrategyConfig(t *testing.T) {
	persister := newRunnerSpyPersister()
	runner := NewRunner(defaultRunnerDefinition(), Dependencies{Persister: persister})
	strategy := strategyWithDebateRounds(t, "AAPL", 4)

	prepared, err := runner.Prepare(strategy, GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared.Runtime.ResearchRounds != 4 || prepared.Runtime.RiskRounds != 4 {
		t.Fatalf("prepared.Runtime rounds = %+v, want 4/4", prepared.Runtime)
	}
	if len(prepared.ConfigSnapshot) == 0 {
		t.Fatal("expected config snapshot to be populated")
	}
	if prepared.Runtime.SkipPhases[PhaseRiskDebate] {
		t.Fatal("risk debate is skipped by default, want canonical risk stage enabled")
	}
}

func TestRunnerRunStrategy_ConcurrentRunsKeepConfigIsolated(t *testing.T) {
	persister := newRunnerSpyPersister()
	runner := NewRunner(defaultRunnerDefinition(), Dependencies{Persister: persister})

	strategyOne := strategyWithDebateRounds(t, "AAPL", 1)
	strategyThree := strategyWithDebateRounds(t, "MSFT", 3)

	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan *RunResult, 2)
	errs := make(chan error, 2)
	for _, strategy := range []domain.Strategy{strategyOne, strategyThree} {
		strategy := strategy
		go func() {
			defer wg.Done()
			prepared, pErr := runner.Prepare(strategy, GlobalSettings{})
			if pErr != nil {
				errs <- pErr
				return
			}
			result, err := runner.Run(context.Background(), prepared)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("RunStrategy() error = %v", err)
	}
	close(results)

	counts := []int{}
	for result := range results {
		counts = append(counts, persister.decisionCount(result.Run.ID))
	}
	sort.Ints(counts)
	want := []int{6, 10}
	if len(counts) != len(want) || counts[0] != want[0] || counts[1] != want[1] {
		t.Fatalf("decision counts = %v, want %v", counts, want)
	}
}

func TestRunnerRunStrategy_AnalysisFailureReturnsWarningButCompletes(t *testing.T) {
	persister := newRunnerSpyPersister()
	def := defaultRunnerDefinition()
	def.Analysis = []AnalysisAgent{
		stubAnalysisAgent{name: "market", role: AgentRoleMarketAnalyst, fn: func(context.Context, AnalysisInput) (AnalysisOutput, error) {
			return AnalysisOutput{Report: "market-report"}, nil
		}},
		stubAnalysisAgent{name: "news", role: AgentRoleNewsAnalyst, fn: func(context.Context, AnalysisInput) (AnalysisOutput, error) {
			return AnalysisOutput{}, errors.New("news provider down")
		}},
	}
	runner := NewRunner(def, Dependencies{Persister: persister})

	result, err := runner.RunStrategy(context.Background(), strategyWithDebateRounds(t, "AAPL", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("RunStrategy() error = %v, want nil", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(result.Warnings))
	}
	if result.Warnings[0].Role != AgentRoleNewsAnalyst {
		t.Fatalf("warning role = %s, want %s", result.Warnings[0].Role, AgentRoleNewsAnalyst)
	}
	if result.Run.Status != domain.PipelineStatusCompleted {
		t.Fatalf("run status = %s, want completed", result.Run.Status)
	}
	if got := result.State.AnalystReports[AgentRoleMarketAnalyst]; got != "market-report" {
		t.Fatalf("market report = %q, want market-report", got)
	}
}

func TestRunnerRunStrategy_RiskJudgeUpdatesCanonicalSignalAndPlan(t *testing.T) {
	persister := newRunnerSpyPersister()
	runner := NewRunner(defaultRunnerDefinition(), Dependencies{Persister: persister})

	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "AAPL", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	result, err := runner.Run(context.Background(), prepared)
	if err != nil {
		t.Fatalf("RunStrategy() error = %v", err)
	}
	if result.Signal != domain.PipelineSignalBuy {
		t.Fatalf("signal = %s, want buy", result.Signal)
	}
	if result.State.FinalSignal.Confidence != 0.9 {
		t.Fatalf("final confidence = %v, want 0.9", result.State.FinalSignal.Confidence)
	}
	if result.State.TradingPlan.PositionSize != 5 {
		t.Fatalf("position size = %v, want 5", result.State.TradingPlan.PositionSize)
	}
	if result.State.RiskDebate.FinalSignal == "" {
		t.Fatal("expected stored risk signal to be populated")
	}
}

func TestRunnerRun_SeedsInitialStateBeforeAnalysis(t *testing.T) {
	persister := newRunnerSpyPersister()
	var captured AnalysisInput
	runner := NewRunner(Definition{
		Analysis: []AnalysisAgent{
			stubAnalysisAgent{name: "market", role: AgentRoleMarketAnalyst, fn: func(_ context.Context, input AnalysisInput) (AnalysisOutput, error) {
				captured = input
				return AnalysisOutput{Report: "seeded-market"}, nil
			}},
		},
		Research: ResearchDebateStage{
			Debaters: []DebateAgent{
				stubDebateAgent{name: "bull", role: AgentRoleBullResearcher, fn: func(_ context.Context, input DebateInput) (DebateOutput, error) {
					return DebateOutput{Contribution: input.Ticker + "-bull"}, nil
				}},
				stubDebateAgent{name: "bear", role: AgentRoleBearResearcher, fn: func(_ context.Context, input DebateInput) (DebateOutput, error) {
					return DebateOutput{Contribution: input.Ticker + "-bear"}, nil
				}},
			},
			Judge: stubResearchJudge{name: "judge", role: AgentRoleInvestJudge, fn: func(_ context.Context, input DebateInput) (ResearchJudgeOutput, error) {
				return ResearchJudgeOutput{InvestmentPlan: input.Ticker + "-plan"}, nil
			}},
		},
		Trader: stubTradeAgent{name: "trader", role: AgentRoleTrader, fn: func(_ context.Context, input TradingInput) (TradingOutput, error) {
			plan := TradingPlan{Action: PipelineSignalHold, Ticker: input.Ticker, Rationale: "seed test"}
			payload, _ := json.Marshal(plan)
			return TradingOutput{Plan: plan, StoredOutput: string(payload)}, nil
		}},
		Risk: RiskDebateStage{
			Debaters: []DebateAgent{
				stubDebateAgent{name: "aggressive", role: AgentRoleAggressiveAnalyst, fn: func(_ context.Context, input DebateInput) (DebateOutput, error) {
					return DebateOutput{Contribution: input.Ticker + "-risk"}, nil
				}},
			},
			Judge: stubRiskJudge{name: "risk-manager", role: AgentRoleRiskManager, fn: func(_ context.Context, input RiskJudgeInput) (RiskJudgeOutput, error) {
				return RiskJudgeOutput{FinalSignal: FinalSignal{Signal: PipelineSignalHold, Confidence: 0.7}, StoredSignal: `{"action":"hold"}`, TradingPlan: input.TradingPlan}, nil
			}},
		},
	}, Dependencies{Persister: persister})

	strategy := strategyWithDebateRounds(t, "AAPL", 1)
	prepped, err := runner.Prepare(strategy, GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	now := time.Date(2026, 4, 5, 14, 30, 0, 0, time.UTC)
	prepped.InitialState = InitialStateSeed{
		Market: &MarketData{
			Bars:       []domain.OHLCV{{Timestamp: now, Open: 100, High: 110, Low: 95, Close: 108, Volume: 2_500}},
			Indicators: []domain.Indicator{{Name: "rsi_14", Value: 62.5, Timestamp: now}},
		},
		News:         []data.NewsArticle{{Title: "AAPL rallies", Summary: "Revenue beats expectations.", PublishedAt: now, Sentiment: 0.8}},
		Fundamentals: &data.Fundamentals{Ticker: "AAPL", MarketCap: 3_000_000_000_000, FetchedAt: now},
		Social:       &data.SocialSentiment{Ticker: "AAPL", Score: 0.71, MeasuredAt: now},
	}

	if _, err := runner.Run(context.Background(), prepped); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if captured.Market == nil || len(captured.Market.Bars) != 1 {
		t.Fatalf("captured market bars = %+v, want seeded market data", captured.Market)
	}
	if got := captured.Market.Indicators[0].Name; got != "rsi_14" {
		t.Fatalf("captured indicator = %q, want rsi_14", got)
	}
	if len(captured.News) != 1 || captured.News[0].Title != "AAPL rallies" {
		t.Fatalf("captured news = %+v, want seeded news", captured.News)
	}
	if captured.Fundamentals == nil || captured.Fundamentals.Ticker != "AAPL" {
		t.Fatalf("captured fundamentals = %+v, want seeded fundamentals", captured.Fundamentals)
	}
	if captured.Social == nil || captured.Social.Score != 0.71 {
		t.Fatalf("captured social = %+v, want seeded social", captured.Social)
	}
}

func TestRunnerRun_PhaseOrdering(t *testing.T) {
	t.Parallel()

	var order []string
	var mu sync.Mutex
	recordPhase := func(phase string) {
		mu.Lock()
		order = append(order, phase)
		mu.Unlock()
	}

	def := Definition{
		Analysis: []AnalysisAgent{
			stubAnalysisAgent{name: "market", role: AgentRoleMarketAnalyst, fn: func(_ context.Context, _ AnalysisInput) (AnalysisOutput, error) {
				recordPhase("analysis")
				return AnalysisOutput{Report: "ok"}, nil
			}},
		},
		Research: ResearchDebateStage{
			Debaters: []DebateAgent{
				stubDebateAgent{name: "bull", role: AgentRoleBullResearcher, fn: func(_ context.Context, _ DebateInput) (DebateOutput, error) {
					recordPhase("research")
					return DebateOutput{Contribution: "bull-contrib"}, nil
				}},
			},
			Judge: stubResearchJudge{name: "judge", role: AgentRoleInvestJudge, fn: func(_ context.Context, _ DebateInput) (ResearchJudgeOutput, error) {
				return ResearchJudgeOutput{InvestmentPlan: "plan"}, nil
			}},
		},
		Trader: stubTradeAgent{name: "trader", role: AgentRoleTrader, fn: func(_ context.Context, input TradingInput) (TradingOutput, error) {
			recordPhase("trading")
			plan := TradingPlan{Action: PipelineSignalBuy, Ticker: input.Ticker, EntryType: "market", EntryPrice: 100, PositionSize: 10, StopLoss: 90, TakeProfit: 120, Confidence: 0.8}
			payload, _ := json.Marshal(plan)
			return TradingOutput{Plan: plan, StoredOutput: string(payload)}, nil
		}},
		Risk: RiskDebateStage{
			Debaters: []DebateAgent{
				stubDebateAgent{name: "risk", role: AgentRoleAggressiveAnalyst, fn: func(_ context.Context, _ DebateInput) (DebateOutput, error) {
					recordPhase("risk")
					return DebateOutput{Contribution: "risk-contrib"}, nil
				}},
			},
			Judge: stubRiskJudge{name: "rm", role: AgentRoleRiskManager, fn: func(_ context.Context, input RiskJudgeInput) (RiskJudgeOutput, error) {
				return RiskJudgeOutput{FinalSignal: FinalSignal{Signal: PipelineSignalBuy, Confidence: 0.9}, StoredSignal: `{"action":"buy"}`, TradingPlan: input.TradingPlan}, nil
			}},
		},
	}

	persister := newRunnerSpyPersister()
	runner := NewRunner(def, Dependencies{Persister: persister})

	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	prepared.Runtime.SkipPhases = nil // Enable all phases.

	if _, err := runner.Run(context.Background(), prepared); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{"analysis", "research", "trading", "risk"}
	if len(order) != len(want) {
		t.Fatalf("phase order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("phase[%d] = %q, want %q (full order: %v)", i, order[i], want[i], order)
		}
	}
}

func TestRunnerRun_PhaseSkip(t *testing.T) {
	t.Parallel()

	analysisRan := false
	def := defaultRunnerDefinition()
	def.Analysis = []AnalysisAgent{
		stubAnalysisAgent{name: "market", role: AgentRoleMarketAnalyst, fn: func(_ context.Context, _ AnalysisInput) (AnalysisOutput, error) {
			analysisRan = true
			return AnalysisOutput{Report: "ok"}, nil
		}},
	}

	persister := newRunnerSpyPersister()
	runner := NewRunner(def, Dependencies{Persister: persister})

	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	prepared.Runtime.SkipPhases = map[Phase]bool{
		PhaseAnalysis: true,
	}

	result, err := runner.Run(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if analysisRan {
		t.Error("analysis phase ran despite being skipped")
	}
	if result.Run.Status != domain.PipelineStatusCompleted {
		t.Errorf("run status = %s, want completed", result.Run.Status)
	}
}

func TestRunnerRun_ErrorMidPipeline_HaltsSubsequentPhases(t *testing.T) {
	t.Parallel()

	tradingRan := false
	def := defaultRunnerDefinition()
	def.Research = ResearchDebateStage{
		Debaters: []DebateAgent{
			stubDebateAgent{name: "bull", role: AgentRoleBullResearcher, fn: func(_ context.Context, _ DebateInput) (DebateOutput, error) {
				return DebateOutput{}, errors.New("research failed")
			}},
		},
		Judge: stubResearchJudge{name: "judge", role: AgentRoleInvestJudge, fn: func(_ context.Context, _ DebateInput) (ResearchJudgeOutput, error) {
			return ResearchJudgeOutput{InvestmentPlan: "plan"}, nil
		}},
	}
	def.Trader = stubTradeAgent{name: "trader", role: AgentRoleTrader, fn: func(_ context.Context, input TradingInput) (TradingOutput, error) {
		tradingRan = true
		plan := TradingPlan{Action: PipelineSignalHold, Ticker: input.Ticker}
		payload, _ := json.Marshal(plan)
		return TradingOutput{Plan: plan, StoredOutput: string(payload)}, nil
	}}

	persister := newRunnerSpyPersister()
	runner := NewRunner(def, Dependencies{Persister: persister})

	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	prepared.Runtime.SkipPhases = nil // Enable all phases.

	result, runErr := runner.Run(context.Background(), prepared)
	if runErr == nil {
		t.Fatal("expected error from research phase")
	}
	if tradingRan {
		t.Error("trading phase ran after research failure")
	}
	if result.Run.Status != domain.PipelineStatusFailed {
		t.Errorf("run status = %s, want failed", result.Run.Status)
	}
}

func TestRunnerRun_ContextCancellation(t *testing.T) {
	t.Parallel()

	def := defaultRunnerDefinition()
	def.Analysis = []AnalysisAgent{
		stubAnalysisAgent{name: "slow", role: AgentRoleMarketAnalyst, fn: func(ctx context.Context, _ AnalysisInput) (AnalysisOutput, error) {
			<-ctx.Done()
			return AnalysisOutput{}, ctx.Err()
		}},
	}

	persister := newRunnerSpyPersister()
	runner := NewRunner(def, Dependencies{Persister: persister})

	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay to let Run start.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	result, runErr := runner.Run(ctx, prepared)
	if runErr == nil {
		t.Fatal("expected error from cancelled context")
	}
	if result.Run.Status != domain.PipelineStatusFailed {
		t.Errorf("run status = %s, want failed", result.Run.Status)
	}
}

func TestRunnerRun_TypedCancellation(t *testing.T) {
	for _, cause := range []runcontrol.Cause{runcontrol.Operator, runcontrol.Shutdown, runcontrol.KillSwitch} {
		t.Run(string(cause), func(t *testing.T) {
			def := defaultRunnerDefinition()
			def.Analysis = []AnalysisAgent{stubAnalysisAgent{name: "slow", role: AgentRoleMarketAnalyst, fn: func(ctx context.Context, _ AnalysisInput) (AnalysisOutput, error) {
				<-ctx.Done()
				return AnalysisOutput{}, ctx.Err()
			}}}
			persister := newRunnerSpyPersister()
			events := make(chan PipelineEvent, 16)
			runner := NewRunner(def, Dependencies{Persister: persister, Events: events})
			prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			go func() { time.Sleep(10 * time.Millisecond); cancel(cause) }()
			result, runErr := runner.Run(ctx, prepared)
			if runErr == nil {
				t.Fatal("expected cancellation error")
			}
			if result.Run.Status != domain.PipelineStatusCancelled {
				t.Fatalf("status = %s, want cancelled", result.Run.Status)
			}
			close(events)
			found := false
			for event := range events {
				found = found || event.Type == PipelineCancelled
			}
			if !found {
				t.Fatal("pipeline_cancelled WebSocket event not emitted")
			}
		})
	}
}

func TestRunnerRun_FailedAndCancelledUseDurableSignal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status domain.PipelineStatus
		cause  error
	}{
		{name: "failed", status: domain.PipelineStatusFailed},
		{name: "cancelled", status: domain.PipelineStatusCancelled, cause: runcontrol.Operator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := defaultRunnerDefinition()
			def.Trader = stubTradeAgent{name: "failed", role: AgentRoleTrader, fn: func(context.Context, TradingInput) (TradingOutput, error) {
				return TradingOutput{}, errors.New("phase failed")
			}}
			winner := domain.PipelineRun{ID: uuid.New(), TradeDate: time.Now().UTC(), Status: tc.status, Signal: domain.PipelineSignalSell}
			persister := newRunnerSpyPersister()
			persister.receipt = &repository.PipelineRunFinalizationReceipt{Applied: true, Run: winner}
			runner := NewRunner(def, Dependencies{Persister: persister})
			prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if tc.cause != nil {
				var cancel context.CancelCauseFunc
				ctx, cancel = context.WithCancelCause(ctx)
				cancel(tc.cause)
			}

			result, runErr := runner.Run(ctx, prepared)
			if runErr == nil {
				t.Fatal("Run() error = nil, want phase error")
			}
			if result.Signal != winner.Signal || result.State.FinalSignal.Signal != winner.Signal {
				t.Fatalf("signals = %q/%q, want durable %q", result.Signal, result.State.FinalSignal.Signal, winner.Signal)
			}
			if !result.TerminalApplied {
				t.Fatal("terminal winner not marked applied")
			}
		})
	}
}

func TestRunnerRun_DeadlineIsFailed(t *testing.T) {
	def := defaultRunnerDefinition()
	def.Analysis = []AnalysisAgent{stubAnalysisAgent{name: "slow", role: AgentRoleMarketAnalyst, fn: func(ctx context.Context, _ AnalysisInput) (AnalysisOutput, error) {
		<-ctx.Done()
		return AnalysisOutput{}, ctx.Err()
	}}}
	persister := newRunnerSpyPersister()
	runner := NewRunner(def, Dependencies{Persister: persister})
	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, _ := runner.Run(ctx, prepared)
	if result.Run.Status != domain.PipelineStatusFailed || !strings.Contains(result.Run.ErrorMessage, "pipeline timeout") {
		t.Fatalf("run = %+v, want timeout failure", result.Run)
	}
}

func TestClassifyRunFailure_CancellationCauseSemantics(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		err        error
		wantStatus domain.PipelineStatus
		wantReason string
	}{
		{
			name:       "kill switch",
			ctx:        cancelledRunnerContext(t, runcontrol.KillSwitch),
			err:        context.Canceled,
			wantStatus: domain.PipelineStatusCancelled,
		},
		{
			name:       "bare cancellation",
			ctx:        cancelledRunnerContext(t, nil),
			err:        context.Canceled,
			wantStatus: domain.PipelineStatusFailed,
		},
		{
			name:       "monitor failure",
			ctx:        cancelledRunnerContext(t, errors.New("scheduler: poll kill switch: network error")),
			err:        context.Canceled,
			wantStatus: domain.PipelineStatusFailed,
			wantReason: "scheduler: poll kill switch: network error",
		},
		{
			name:       "deadline",
			ctx:        cancelledRunnerContext(t, context.DeadlineExceeded),
			err:        context.DeadlineExceeded,
			wantStatus: domain.PipelineStatusFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, _, _, reason := classifyRunFailure(test.ctx, test.err)
			if status != test.wantStatus {
				t.Fatalf("status = %s, want %s", status, test.wantStatus)
			}
			if test.wantReason != "" && reason != test.wantReason {
				t.Fatalf("reason = %q, want %q", reason, test.wantReason)
			}
		})
	}
}

func cancelledRunnerContext(t *testing.T, cause error) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	return ctx
}

func TestRunnerRun_PanicInPhaseMarksRunFailed(t *testing.T) {
	t.Parallel()

	const sensitivePanic = "provider-secret-value"
	def := defaultRunnerDefinition()
	def.Trader = stubTradeAgent{name: "trader", role: AgentRoleTrader, fn: func(context.Context, TradingInput) (TradingOutput, error) {
		panic(sensitivePanic)
	}}

	persister := newRunnerSpyPersister()
	events := make(chan PipelineEvent, 64)
	var logs bytes.Buffer
	runner := NewRunner(def, Dependencies{
		Persister: persister,
		Events:    events,
		Logger:    slog.New(slog.NewTextHandler(&logs, nil)),
	})

	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	result, runErr := runner.Run(context.Background(), prepared)
	if runErr == nil {
		t.Fatal("Run() error = nil, want panic recovery error")
	}
	if !strings.Contains(runErr.Error(), "panic recovered") {
		t.Fatalf("Run() error = %q, want panic recovered substring", runErr.Error())
	}
	if strings.Contains(runErr.Error(), sensitivePanic) {
		t.Fatalf("Run() error leaked panic value: %q", runErr.Error())
	}
	if result == nil {
		t.Fatal("Run() result = nil, want failed result")
	}
	if !result.TerminalApplied {
		t.Fatal("panic winner did not report applied terminal result")
	}
	if result.Run.Status != domain.PipelineStatusFailed {
		t.Fatalf("run status = %s, want failed", result.Run.Status)
	}
	if !strings.Contains(result.Run.ErrorMessage, "panic recovered") {
		t.Fatalf("run error_message = %q, want panic recovered substring", result.Run.ErrorMessage)
	}
	if strings.Contains(result.Run.ErrorMessage, sensitivePanic) {
		t.Fatalf("run error_message leaked panic value: %q", result.Run.ErrorMessage)
	}
	if strings.Contains(logs.String(), sensitivePanic) {
		t.Fatalf("runner logs leaked panic value: %q", logs.String())
	}

	close(events)
	pipelineErrors := 0
	for event := range events {
		if event.Type == PipelineError {
			pipelineErrors++
			if strings.Contains(event.Error, sensitivePanic) {
				t.Fatalf("pipeline event leaked panic value: %q", event.Error)
			}
		}
	}
	if pipelineErrors == 0 {
		t.Fatal("expected at least one PipelineError event after panic recovery")
	}
}

func TestRunnerRun_PanicCASLoserReturnsWinnerWithoutTerminalTelemetry(t *testing.T) {
	def := defaultRunnerDefinition()
	def.Trader = stubTradeAgent{name: "trader", role: AgentRoleTrader, fn: func(context.Context, TradingInput) (TradingOutput, error) {
		panic("losing panic")
	}}
	winner := domain.PipelineRun{
		ID:        uuid.New(),
		TradeDate: time.Now().UTC(),
		Status:    domain.PipelineStatusCancelled,
		Signal:    domain.PipelineSignalSell,
	}
	persister := newRunnerSpyPersister()
	persister.receipt = &repository.PipelineRunFinalizationReceipt{Run: winner}
	events := make(chan PipelineEvent, 64)
	var logs bytes.Buffer
	runner := NewRunner(def, Dependencies{Persister: persister, Events: events, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatal(err)
	}

	result, runErr := runner.Run(context.Background(), prepared)
	if runErr == nil || !strings.Contains(runErr.Error(), "panic recovered") {
		t.Fatalf("Run() error = %v, want panic recovery error", runErr)
	}
	if result == nil || result.Run.ID != winner.ID || result.Run.Status != winner.Status || result.Signal != winner.Signal {
		t.Fatalf("Run() result = %+v, want canonical winner %+v", result, winner)
	}
	if result.TerminalApplied {
		t.Fatal("panic CAS loser reported terminal result as applied")
	}
	if result.State.FinalSignal.Signal != winner.Signal {
		t.Fatalf("state final signal = %s, want canonical %s", result.State.FinalSignal.Signal, winner.Signal)
	}
	if logs.Len() != 0 {
		t.Fatalf("CAS loser emitted terminal log telemetry: %s", logs.String())
	}
	close(events)
	for event := range events {
		if event.Type == PipelineError || event.Type == PipelineCancelled || event.Type == LLMCacheStatsReported {
			t.Fatalf("CAS loser emitted terminal telemetry: %+v", event)
		}
	}
}

func TestRunnerRun_TerminalPersistenceFailureFailsClosed(t *testing.T) {
	t.Parallel()

	persister := newRunnerSpyPersister()
	persister.completeErr = errors.New("database unavailable")
	events := make(chan PipelineEvent, 64)
	runner := NewRunner(defaultRunnerDefinition(), Dependencies{Persister: persister, Events: events})
	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	result, runErr := runner.Run(context.Background(), prepared)
	if runErr == nil || !strings.Contains(runErr.Error(), "persist completed terminal status") {
		t.Fatalf("Run() error = %v, want terminal persistence failure", runErr)
	}
	if result == nil || result.Run.Status != domain.PipelineStatusRunning {
		t.Fatalf("Run() result = %+v, want non-terminal result", result)
	}

	close(events)
	var completed, failed int
	for event := range events {
		switch event.Type {
		case PipelineCompleted:
			completed++
		case PipelineError:
			failed++
		}
	}
	if completed != 0 || failed != 0 {
		t.Fatalf("terminal events: completed=%d failed=%d, want none", completed, failed)
	}
}

func TestRunnerRun_TerminalEventFailureFailsClosed(t *testing.T) {
	t.Parallel()

	persister := newRunnerSpyPersister()
	persister.eventErr = errors.New("event store unavailable")
	events := make(chan PipelineEvent, 64)
	runner := NewRunner(defaultRunnerDefinition(), Dependencies{Persister: persister, Events: events})
	prepared, err := runner.Prepare(strategyWithDebateRounds(t, "TEST", 1), GlobalSettings{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	result, runErr := runner.Run(context.Background(), prepared)
	if runErr == nil || !strings.Contains(runErr.Error(), "persist completed terminal status") {
		t.Fatalf("Run() error = %v, want terminal event failure", runErr)
	}
	if result == nil || result.Run.Status != domain.PipelineStatusRunning {
		t.Fatalf("Run() result = %+v, want non-terminal result", result)
	}

	close(events)
	for event := range events {
		if event.Type == PipelineCompleted {
			t.Fatal("emitted PipelineCompleted after terminal event persistence failure")
		}
	}
}

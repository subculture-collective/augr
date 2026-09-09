package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	agentanalysts "github.com/PatrickFanella/get-rich-quick/internal/agent/analysts"
	agentdebate "github.com/PatrickFanella/get-rich-quick/internal/agent/debate"
	agentrisk "github.com/PatrickFanella/get-rich-quick/internal/agent/risk"
	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	agenttrader "github.com/PatrickFanella/get-rich-quick/internal/agent/trader"
	"github.com/PatrickFanella/get-rich-quick/internal/api"
	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/eventmarkets"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	alpacaexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/alpaca"
	binanceexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/binance"
	kalshiexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/kalshi"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/paper"
	polymarketexecution "github.com/PatrickFanella/get-rich-quick/internal/execution/polymarket"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	polymarketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
	"github.com/PatrickFanella/get-rich-quick/internal/metrics"
	"github.com/PatrickFanella/get-rich-quick/internal/notification"
	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

const (
	strategyMarketLookback = 400 * 24 * time.Hour
	strategyNewsLookback   = 7 * 24 * time.Hour
	strategySocialLookback = 7 * 24 * time.Hour
	postCloseDataGrace     = 30 * time.Minute
	requiredNewsMaxAge     = 36 * time.Hour
	requiredNewsMinDirect  = 3
	nativeTerminalTimeout  = 10 * time.Second
)

var defaultAnalysisRoles = []agent.AgentRole{
	agent.AgentRoleMarketAnalyst,
	agent.AgentRoleFundamentalsAnalyst,
	agent.AgentRoleNewsAnalyst,
	agent.AgentRoleSocialMediaAnalyst,
}

type marketDataService interface {
	GetOHLCV(ctx context.Context, marketType domain.MarketType, ticker string, timeframe data.Timeframe, from, to time.Time) ([]domain.OHLCV, error)
	GetFundamentals(ctx context.Context, marketType domain.MarketType, ticker string) (data.Fundamentals, error)
	GetNews(ctx context.Context, marketType domain.MarketType, ticker string, from, to time.Time) ([]data.NewsArticle, error)
	GetSocialSentiment(ctx context.Context, marketType domain.MarketType, ticker string, from, to time.Time) ([]data.SocialSentiment, error)
}

type promptOverrideSource interface {
	Overrides() map[agent.AgentRole]string
}

type polymarketMarketDataSource interface {
	GetMarketData(ctx context.Context, slug string) (*agent.PredictionMarketData, error)
}

type kalshiMarketDataSource interface {
	LoadSnapshot(ctx context.Context, ticker string) (kalshiexecution.Snapshot, error)
}

type polymarketTickFeed interface {
	Ticks(slug string) <-chan polymarketdata.Tick
}

type realStrategyRunner struct {
	stockCapture            stockEvidenceCapture
	preparePaperStockSignal func(context.Context, execution.ExecutionScope, execution.TradingPlan) (execution.SignalOrderPreparation, error)

	executionAccount       domain.ExecutionAccountBinding
	runGroupMu             sync.Mutex
	runGroup               *runcontrol.Group
	cfg                    config.Config
	globals                agent.GlobalSettings
	dataService            marketDataService
	optionsProvider        data.OptionsDataProvider
	runRepo                repository.PipelineRunRepository
	snapshotRepo           repository.PipelineRunSnapshotRepository
	decisionRepo           repository.AgentDecisionRepository
	eventRepo              repository.AgentEventRepository
	orderRepo              repository.OrderRepository
	positionRepo           repository.PositionRepository
	tradeRepo              repository.TradeRepository
	opportunityRepo        repository.OpportunityRepository
	auditLogRepo           repository.AuditLogRepository
	economicWriter         execution.AcceptedEconomicWriter
	riskEngine             risk.RiskEngine
	tradeDecisionRecorder  execution.DecisionRecorder
	metrics                *metrics.Metrics
	notificationManager    *notification.Manager
	runRegistry            *agent.RunContextRegistry
	llmBudget              *llm.Budget
	promptOverrides        promptOverrideSource
	logger                 *slog.Logger
	localPaperMu           sync.Mutex
	localPaperBroker       *paper.PaperBroker
	portfolioAllocatorMode portfolio.AllocatorMode
	kalshiLiveClient       kalshiexecution.LiveClient
	kalshiDataProvider     kalshiMarketDataSource
	polymarketClient       *polymarketexecution.Client // nil if not configured
	polymarketMarketData   polymarketMarketDataSource
	kalshiMarketData       kalshiMarketDataSource
	polymarketFeed         polymarketTickFeed
	polymarketStopGuard    *polymarketexecution.StopGuard
	polymarketWorkers      sync.Map
	polymarketWorkerCtx    context.Context
	polymarketWorkerStop   context.CancelFunc
	polymarketWorkerWG     sync.WaitGroup
	hub                    *api.Hub // nil until wired; optional WebSocket broadcast
}

func newRealStrategyRunner(
	executionAccount domain.ExecutionAccountBinding,
	cfg config.Config,
	dataService marketDataService,
	runRepo repository.PipelineRunRepository,
	snapshotRepo repository.PipelineRunSnapshotRepository,
	decisionRepo repository.AgentDecisionRepository,
	eventRepo repository.AgentEventRepository,
	orderRepo repository.OrderRepository,
	positionRepo repository.PositionRepository,
	tradeRepo repository.TradeRepository,
	auditLogRepo repository.AuditLogRepository,
	economicWriter execution.AcceptedEconomicWriter,
	riskEngine risk.RiskEngine,
	appMetrics *metrics.Metrics,
	notificationManager *notification.Manager,
	runRegistry *agent.RunContextRegistry,
	llmBudget *llm.Budget,
	promptOverrides promptOverrideSource,
	tradeDecisionRecorder execution.DecisionRecorder,
	kalshiDataProvider kalshiMarketDataSource,
	kalshiLiveClient kalshiexecution.LiveClient,
	polymarketFeed polymarketTickFeed,
	logger *slog.Logger,
) *realStrategyRunner {
	if logger == nil {
		logger = slog.Default()
	}

	workerCtx, workerStop := context.WithCancel(context.Background())
	runner := &realStrategyRunner{
		executionAccount:      executionAccount,
		runGroup:              runcontrol.NewGroup(),
		cfg:                   cfg,
		globals:               globalSettingsFromConfig(cfg),
		dataService:           dataService,
		runRepo:               runRepo,
		snapshotRepo:          snapshotRepo,
		decisionRepo:          decisionRepo,
		eventRepo:             eventRepo,
		orderRepo:             orderRepo,
		positionRepo:          positionRepo,
		tradeRepo:             tradeRepo,
		auditLogRepo:          auditLogRepo,
		economicWriter:        economicWriter,
		riskEngine:            riskEngine,
		tradeDecisionRecorder: tradeDecisionRecorder,
		metrics:               appMetrics,
		notificationManager:   notificationManager,
		runRegistry:           runRegistry,
		llmBudget:             llmBudget,
		promptOverrides:       promptOverrides,
		logger:                logger,
		polymarketFeed:        polymarketFeed,
		kalshiDataProvider:    kalshiDataProvider,
		kalshiLiveClient:      kalshiLiveClient,
		polymarketWorkerCtx:   workerCtx,
		polymarketWorkerStop:  workerStop,
		localPaperBroker:      newConfiguredPaperBroker(cfg.Paper, logger),
	}
	runner.setRiskPortfolioSnapshotSource(runner.localPaperBroker)

	// Wire Polymarket client if credentials are configured.
	pm := cfg.Brokers.Polymarket
	if cfg.Features.EnablePolymarketAutomation && strings.TrimSpace(pm.KeyID) != "" {
		client := polymarketexecution.NewClient(pm.KeyID, pm.SecretKey, logger)
		liveAuthorized := polymarketLiveExecutionAuthorized(cfg, executionAccount)
		if liveAuthorized {
			client.SetL2Auth(pm.Address, pm.KeyID, pm.SecretKey, pm.Passphrase)
		}
		client.SetAPIBaseURL(pm.APIBaseURL)
		client.SetGatewayBaseURL(pm.GatewayBaseURL)
		runner.polymarketClient = client
		runner.polymarketMarketData = client
		if liveAuthorized && strings.TrimSpace(pm.SecretKey) != "" {
			exitRepo, _ := orderRepo.(repository.AtomicPredictionExitRepository)
			if guard, err := polymarketexecution.NewStopGuard(polymarketexecution.StopGuardConfig{ExecutionAccount: executionAccount, Broker: polymarketexecution.NewBroker(client), ExitRepo: exitRepo, EconomicWriter: runner.economicWriter, Logger: logger, Metrics: appMetrics}); err == nil {
				runner.polymarketStopGuard = guard
			} else {
				logger.Warn("polymarket stop guard disabled", slog.String("error", err.Error()))
			}
		}
	}
	if cfg.Features.EnablePolymarketAutomation && runner.polymarketMarketData == nil {
		client := polymarketexecution.NewClient("", "", logger)
		client.SetGatewayBaseURL(pm.GatewayBaseURL)
		runner.polymarketMarketData = client
	}
	if runner.kalshiMarketData == nil {
		runner.kalshiMarketData = runner.kalshiDataProvider
	}

	return runner
}

func polymarketLiveExecutionAuthorized(cfg config.Config, account domain.ExecutionAccountBinding) bool {
	if !cfg.Features.EnableLiveTrading || account.Environment() != domain.AccountEnvironmentLive {
		return false
	}
	for _, broker := range cfg.LiveTradingAllowedBrokers {
		if strings.EqualFold(strings.TrimSpace(broker), "polymarket") {
			return true
		}
	}
	return false
}

func (r *realStrategyRunner) RunStrategy(ctx context.Context, strategy domain.Strategy, executionVersionID uuid.UUID) (*api.StrategyRunResult, error) {
	if executionVersionID == uuid.Nil {
		return nil, errors.New("strategy execution version ID is required")
	}
	group := r.strategyRunGroup()
	if !group.HasLease(ctx) {
		admittedCtx, lease, err := group.Admit(ctx)
		if err != nil {
			return nil, err
		}
		ctx = admittedCtx
		defer lease.Done()
	}
	if strategy.MarketType.Normalize() == domain.MarketTypeKalshi {
		return r.runKalshiNative(ctx, strategy, executionVersionID)
	}
	if strategy.MarketType.Normalize() == domain.MarketTypePolymarket {
		if !r.cfg.Features.EnablePolymarketAutomation {
			return nil, errors.New("polymarket execution is retired; use a Kalshi strategy")
		}
		return r.runPolymarketNative(ctx, strategy, executionVersionID)
	}
	if generativestrategy.HasRuntimeBinding(strategy.Config) {
		return r.runGeneratedStrategyNative(ctx, strategy, executionVersionID)
	}

	runner, prepared, strategyConfig, eventsCh, err := r.prepareStrategyRun(ctx, strategy, executionVersionID)
	if err != nil {
		if persistErr := r.recordStrategyPreparationFailure(ctx, strategy, executionVersionID, err); persistErr != nil {
			return nil, recognizedRunControlError(ctx, errors.Join(err, persistErr))
		}
		return nil, recognizedRunControlError(ctx, err)
	}

	// Drain phase events to the WebSocket hub in a background goroutine.
	// The channel is closed after runner.Run returns so the goroutine exits naturally.
	var eventDrainer sync.WaitGroup
	var finishEventDrainer sync.Once
	finishDrainer := func() {
		finishEventDrainer.Do(func() {
			if eventsCh != nil {
				close(eventsCh)
				eventDrainer.Wait()
			}
		})
	}
	defer finishDrainer()
	if eventsCh != nil {
		eventDrainer.Add(1)
		go func() {
			defer eventDrainer.Done()
			r.drainPipelineEvents(eventsCh)
		}()
	}

	result, err := runner.Run(ctx, prepared)
	finishDrainer()
	if err != nil {
		if result == nil {
			return nil, err
		}
		r.recordAgentTerminalMetrics(result)
		return &api.StrategyRunResult{Run: result.Run, Signal: result.Signal}, err
	}
	if result == nil {
		return nil, errors.New("strategy runner returned no result")
	}
	canonical := &api.StrategyRunResult{Run: result.Run, Signal: result.Signal}
	if !result.TerminalApplied {
		err := fmt.Errorf("strategy runner: %w: durable status=%s signal=%s", agent.ErrLostTerminalAuthority, result.Run.Status, result.Run.Signal)
		if result.Run.Status == domain.PipelineStatusCancelled {
			err = runcontrol.JoinCauseFromErrorMessage(err, result.Run.ErrorMessage)
		}
		return canonical, err
	}
	r.recordAgentTerminalMetrics(result)

	run, err := r.findRun(ctx, domain.PipelineRunRef{ID: result.Run.ID, TradeDate: result.Run.TradeDate})
	if err != nil {
		return canonical, err
	}

	agent.ApplyStrategyRiskOverridesToResult(result, strategyConfig)
	signal := result.Signal
	state := agent.PipelineStateFromView(result.State)
	planTicker := result.State.TradingPlan.Ticker
	if planTicker == "" {
		planTicker = strategy.Ticker
	}

	receipt, err := r.runRepo.RefineCompletedSignal(ctx, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, run.Signal, signal)
	if err != nil {
		return canonical, err
	}
	if !receipt.Applied {
		canonical = &api.StrategyRunResult{Run: receipt.Run, Signal: receipt.Run.Signal}
		return canonical, fmt.Errorf("pipeline run signal refinement lost: durable status=%s signal=%s", receipt.Run.Status, receipt.Run.Signal)
	}
	run = &receipt.Run
	canonical = &api.StrategyRunResult{Run: *run, Signal: run.Signal}

	if strategy.MarketType.Normalize() == domain.MarketTypePolymarket {
		normalizedSide, err := normalizePolymarketStrategySide(result.State.TradingPlan.Side)
		if err != nil {
			return canonical, fmt.Errorf("polymarket strategy %s: %w", strategy.Name, err)
		}
		result.State.TradingPlan.Side = normalizedSide
		entryPrice := result.State.TradingPlan.EntryPrice
		if entryPrice > 0 && entryPrice > 1 {
			return canonical, fmt.Errorf("polymarket strategy %s: entry price %.4f outside valid range [0,1]", strategy.Name, entryPrice)
		}
	}

	decisionMetadata := r.executionDecisionMetadata(ctx, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})

	finalSignal := execution.FinalSignal{Signal: signal, Confidence: result.State.FinalSignal.Confidence}
	tradingPlan := execution.TradingPlan{
		Action:           signal,
		MarketType:       strategy.MarketType.Normalize(),
		Ticker:           planTicker,
		EntryType:        result.State.TradingPlan.EntryType,
		EntryPrice:       result.State.TradingPlan.EntryPrice,
		PositionSize:     result.State.TradingPlan.PositionSize,
		StopLoss:         result.State.TradingPlan.StopLoss,
		TakeProfit:       result.State.TradingPlan.TakeProfit,
		TimeHorizon:      result.State.TradingPlan.TimeHorizon,
		Confidence:       result.State.TradingPlan.Confidence,
		Rationale:        result.State.TradingPlan.Rationale,
		RiskReward:       result.State.TradingPlan.RiskReward,
		Side:             result.State.TradingPlan.Side,
		DecisionMetadata: decisionMetadata,
	}
	var opportunitySpread *domain.OptionSpread
	if strategy.MarketType.Normalize() == domain.MarketTypeOptions {
		scope, err := executionScopeFromPersistedRun(run, strategy)
		if err != nil {
			return canonical, err
		}
		if r.portfolioAllocatorOwnsPaperExecution(strategy, signal) {
			opportunitySpread, err = r.buildAllocatorOptionsOpportunity(ctx, strategy, finalSignal)
			if err != nil {
				return canonical, err
			}
			tradingPlan.Depth = opportunitySpread.LiquidityUSD
			tradingPlan.Spread = opportunitySpread.SpreadPct
		} else {
			if err := r.executeOptionsSignal(ctx, scope, strategy, finalSignal); err != nil {
				return canonical, err
			}
		}
	} else if !r.portfolioAllocatorOwnsPaperExecution(strategy, signal) {
		scope, err := executionScopeFromPersistedRun(run, strategy)
		if err != nil {
			return canonical, err
		}
		orderManager, err := r.newOrderManager(ctx, strategy, prepared.Config, strategyConfig, scope)
		if err != nil {
			return canonical, err
		}
		if err := r.processStrategySignal(ctx, orderManager, scope, finalSignal, tradingPlan); err != nil {
			return canonical, err
		}
	}
	if err := r.recordPortfolioOpportunity(ctx, strategy, run, finalSignal, tradingPlan, opportunitySpread); err != nil {
		return canonical, err
	}

	// Notification delivery is an optional side effect and must not monopolize a
	// scheduler execution slot when a webhook is slow or rate limited.
	notificationCtx, cancelNotifications := context.WithTimeout(ctx, 30*time.Second)
	defer cancelNotifications()
	if err := r.dispatchNotifications(notificationCtx, strategy, run, state); err != nil {
		r.logger.WarnContext(ctx, "notification dispatch failed (non-fatal)", "error", err, "run_id", run.ID)
	}

	orders, err := loadResultOrders(ctx, r.orderRepo, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})
	if err != nil {
		return canonical, err
	}
	if err = validateResultOrders(orders, r.executionAccount, executionVersionID, run.ID, run.TradeDate); err != nil {
		return canonical, err
	}
	positions, err := loadResultPositions(ctx, r.positionRepo, r.executionAccount, executionVersionID)
	if err != nil {
		return canonical, err
	}

	return &api.StrategyRunResult{
		Run:       *run,
		Signal:    signal,
		Orders:    orders,
		Positions: positions,
	}, nil
}

func (r *realStrategyRunner) runGeneratedStrategyNative(ctx context.Context, strategy domain.Strategy, executionVersionID uuid.UUID) (*api.StrategyRunResult, error) {
	binding, err := generativestrategy.ParseRuntimeBinding(strategy.Config)
	if err != nil {
		return nil, recognizedRunControlError(ctx, err)
	}
	lineage, err := domain.ParseActivePromotionExecutionLineage(strategy.Config, r.executionAccount.AccountID())
	if err != nil {
		return nil, recognizedRunControlError(ctx, err)
	}
	if strategy.MarketType.Normalize() != domain.MarketTypeStock || !strategy.IsPaper || strategy.Status != domain.StrategyStatusActive {
		return nil, errors.New("generated runtime requires an active paper stock strategy")
	}
	now := time.Now().UTC()
	run := domain.PipelineRun{
		ID: uuid.New(), StrategyID: strategy.ID, Ticker: strategy.Ticker, TradeDate: now.Truncate(24 * time.Hour),
		Status: domain.PipelineStatusRunning, StartedAt: now, ConfigSnapshot: strategy.Config,
	}
	if err := bindStrategyRunScope(&run, r.executionAccount, executionVersionID); err != nil {
		return nil, err
	}
	bindPromotionRunLineage(&run, executionVersionID, lineage)
	if err := r.startNativeRun(ctx, "generated_strategy", &run); err != nil {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, err
	}
	completeRun := func(status domain.PipelineStatus, signal domain.PipelineSignal, message string) error {
		return r.completeNativeRun(ctx, "generated_strategy", &run, status, signal, message)
	}
	failRun := func(runErr error) (*api.StrategyRunResult, error) {
		status, message := nativeFailure(ctx, runErr)
		if updateErr := completeRun(status, domain.PipelineSignalHold, message); updateErr != nil {
			runErr = errors.Join(runErr, updateErr)
		}
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, recognizedRunControlError(ctx, runErr)
	}
	if r.dataService == nil {
		return failRun(errors.New("generated runtime market data service is required"))
	}
	bars, err := r.dataService.GetOHLCV(ctx, domain.MarketTypeStock, strategy.Ticker, data.Timeframe1d, now.Add(-strategyMarketLookback), now)
	if err != nil || len(bars) == 0 {
		if err == nil {
			err = errors.New("no bars returned")
		}
		return failRun(fmt.Errorf("generated runtime load ohlcv for %s: %w", strategy.Ticker, err))
	}
	latest := bars[len(bars)-1]
	holding, err := r.generatedStrategyHolding(ctx, strategy, executionVersionID)
	if err != nil {
		return failRun(err)
	}
	decision, err := binding.EvaluateBar(now, generativestrategy.RuntimeBar{
		Timestamp: latest.Timestamp, Open: latest.Open, High: latest.High, Low: latest.Low, Close: latest.Close, Volume: latest.Volume,
	}, holding, lineage.DeploymentBudgetUSD)
	if err != nil {
		return failRun(err)
	}
	signal := domain.PipelineSignalHold
	switch decision.Action {
	case "buy":
		signal = domain.PipelineSignalBuy
	case "sell":
		signal = domain.PipelineSignalSell
	}
	scope, err := executionScopeFromPersistedRun(&run, strategy)
	if err != nil {
		return failRun(err)
	}
	snapshotPayload, err := json.Marshal(map[string]any{
		"schema": "typed-generative-runtime-decision-v1", "source_version_id": binding.SourceVersionID(), "source_version_sha256": binding.SourceVersionDigest(),
		"spec_id": binding.Spec().ID(), "spec_sha256": binding.Spec().Digest(), "ticker": strategy.Ticker, "bar": latest,
		"holding": holding, "action": decision.Action, "execution_price": decision.ExecutionPrice, "proposed_notional": decision.ProposedNotional,
	})
	if err != nil {
		return failRun(err)
	}
	snapshot, err := pipelineSnapshotFromScope(scope, "generated_strategy_decision", snapshotPayload)
	if err != nil {
		return failRun(err)
	}
	if r.snapshotRepo == nil {
		return failRun(errors.New("generated runtime snapshot repository is required"))
	}
	if err := r.snapshotRepo.Create(ctx, snapshot); err != nil {
		return failRun(fmt.Errorf("generated runtime persist decision snapshot: %w", err))
	}
	if err := completeRun(domain.PipelineStatusCompleted, signal, ""); err != nil {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, err
	}
	plan := execution.TradingPlan{
		Action: signal, MarketType: domain.MarketTypeStock, Ticker: strategy.Ticker, EntryType: "market", EntryPrice: decision.ExecutionPrice,
		PositionSize: decision.ProposedNotional / decision.ExecutionPrice, StopLoss: decision.ExecutionPrice * 0.01,
		Confidence: binding.Confidence(), Rationale: "Approved typed strategy evaluated against the latest complete canonical OHLCV bar.",
		NetEV: binding.ExpectedEdge(), Depth: decision.LiquidityUSD, Spread: decision.SpreadPct,
	}
	finalSignal := execution.FinalSignal{Signal: signal, Confidence: binding.Confidence()}
	if err := r.recordPortfolioOpportunity(ctx, strategy, &run, finalSignal, plan, nil); err != nil {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, err
	}
	return &api.StrategyRunResult{Run: run, Signal: run.Signal}, nil
}

func (r *realStrategyRunner) generatedStrategyHolding(ctx context.Context, strategy domain.Strategy, executionVersionID uuid.UUID) (bool, error) {
	repo, ok := r.positionRepo.(repository.ExecutionScopedPositionRepository)
	if !ok || repo == nil {
		return false, errors.New("generated runtime requires execution-scoped position reads")
	}
	positions, err := repo.GetByExecutionScope(ctx, r.executionAccount.AccountID(), r.executionAccount.Environment(),
		string(ledger.ExecutionOriginStrategyVersion), executionVersionID.String(), repository.PositionFilter{Ticker: strategy.Ticker}, 100, 0)
	if err != nil {
		return false, fmt.Errorf("generated runtime load positions: %w", err)
	}
	holding := false
	for _, position := range positions {
		if position.ClosedAt != nil || position.Quantity <= 0 {
			continue
		}
		if position.StrategyID == nil || *position.StrategyID != strategy.ID || position.Ticker != strategy.Ticker || position.Side != domain.PositionSideLong {
			return false, errors.New("generated runtime position ownership is ambiguous")
		}
		if holding {
			return false, errors.New("generated runtime has multiple open target positions")
		}
		holding = true
	}
	return holding, nil
}

func (r *realStrategyRunner) buildAllocatorOptionsOpportunity(ctx context.Context, strategy domain.Strategy, signal execution.FinalSignal) (*domain.OptionSpread, error) {
	if signal.Signal != domain.PipelineSignalBuy {
		return nil, errors.New("options allocator: only new defined-risk opening packages are eligible")
	}
	if r.optionsProvider == nil {
		return nil, errors.New("options allocator: manifest-bound options provider is required")
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(strategy.Config, &sections); err != nil {
		return nil, fmt.Errorf("options allocator: parse strategy config: %w", err)
	}
	cfg, err := rules.ParseOptions(sections["options_rules"])
	if err != nil || cfg == nil || len(cfg.LegSelection) != 2 {
		return nil, errors.New("options allocator: exactly two vertical legs are required")
	}
	chain, err := r.optionsProvider.GetOptionsChain(ctx, cfg.Underlying, time.Time{}, "")
	if err != nil {
		return nil, fmt.Errorf("options allocator: load immutable option chain: %w", err)
	}
	spread, _, err := buildPaperDebitSpreadPlan(cfg, chain, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if spread.QuoteObservedAt.IsZero() {
		return nil, errors.New("options allocator: immutable quote observation time is required")
	}
	return spread, nil
}

func (r *realStrategyRunner) strategyRunGroup() *runcontrol.Group {
	r.runGroupMu.Lock()
	defer r.runGroupMu.Unlock()
	if r.runGroup == nil {
		r.runGroup = runcontrol.NewGroup()
	}
	return r.runGroup
}

func (r *realStrategyRunner) executeOptionsSignal(ctx context.Context, scope execution.ExecutionScope, strategy domain.Strategy, signal execution.FinalSignal) error {
	if signal.Signal == domain.PipelineSignalHold {
		return nil
	}
	if !strategy.IsPaper {
		return errors.New("options runtime: live options execution is disabled")
	}
	if r.optionsProvider == nil {
		return errors.New("options runtime: options data provider is required")
	}
	manager := execution.NewOptionsOrderManager(r.localPaperBroker, r.orderRepo, r.positionRepo, r.tradeRepo, r.riskEngine, r.logger).
		WithBrokerName("paper").WithLiveTrading(false)
	if r.economicWriter != nil {
		manager.WithAcceptedOptionFillWriter(r.economicWriter)
	}
	if signal.Signal == domain.PipelineSignalSell {
		scoped, ok := r.positionRepo.(repository.ExecutionScopedPositionRepository)
		if !ok {
			return errors.New("options runtime: execution-scoped position repository is required")
		}
		originType, originID := scope.Origin()
		positions, err := scoped.GetByExecutionScope(ctx, scope.AccountID(), scope.Environment(), string(originType), originID, repository.PositionFilter{}, 100, 0)
		if err != nil {
			return fmt.Errorf("options runtime: load positions for close: %w", err)
		}
		var open []*domain.Position
		for index := range positions {
			if positions[index].StrategyID != nil && *positions[index].StrategyID == strategy.ID && positions[index].AssetClass == domain.AssetClassOption && positions[index].ClosedAt == nil && positions[index].Quantity > 0 {
				open = append(open, &positions[index])
			}
		}
		if len(open) == 0 {
			return nil
		}
		position := open[0]
		if position.OptionType == nil || position.Expiry == nil {
			return errors.New("options runtime: persisted position lacks contract metadata")
		}
		chain, err := r.optionsProvider.GetOptionsChain(ctx, position.UnderlyingTicker, *position.Expiry, *position.OptionType)
		if err != nil {
			return fmt.Errorf("options runtime: load close quote: %w", err)
		}
		if len(open) > 1 {
			spread, quantity, err := buildPaperSpreadClosePlan(open, chain)
			if err != nil {
				return err
			}
			return manager.ProcessSpreadSignal(ctx, scope, spread, quantity)
		}
		closePrice, err := executableOptionClosePrice(position, chain)
		if err != nil {
			return err
		}
		return manager.CloseOptionPosition(ctx, scope, position, closePrice, "strategy sell signal")
	}
	if signal.Signal != domain.PipelineSignalBuy {
		return fmt.Errorf("options runtime: unsupported signal %q", signal.Signal)
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(strategy.Config, &sections); err != nil {
		return fmt.Errorf("options runtime: parse strategy config: %w", err)
	}
	cfg, err := rules.ParseOptions(sections["options_rules"])
	if err != nil {
		return fmt.Errorf("options runtime: %w", err)
	}
	if cfg == nil {
		return errors.New("options runtime: options_rules config is required")
	}
	chain, err := r.optionsProvider.GetOptionsChain(ctx, cfg.Underlying, time.Time{}, "")
	if err != nil {
		return fmt.Errorf("options runtime: load chain: %w", err)
	}
	if len(cfg.LegSelection) > 1 {
		spread, quantity, err := buildPaperDebitSpreadPlan(cfg, chain, time.Now().UTC())
		if err != nil {
			return err
		}
		legs := make([]risk.OptionLegExposure, 0, len(spread.Legs))
		for _, leg := range spread.Legs {
			legs = append(legs, risk.OptionLegExposure{Greeks: leg.Greeks, Side: leg.Side, Quantity: quantity * float64(leg.Ratio), Multiplier: leg.Contract.Multiplier})
		}
		if err := r.enforceOptionsGreekRisk(ctx, scope, cfg.Underlying, legs); err != nil {
			return err
		}
		return manager.ProcessSpreadSignal(ctx, scope, spread, quantity)
	}
	plan, err := buildPaperSingleLegPlan(cfg, chain, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := r.enforceOptionsGreekRisk(ctx, scope, cfg.Underlying, []risk.OptionLegExposure{{Greeks: *plan.OptionGreeks, Side: domain.OrderSideBuy, Quantity: plan.PositionSize, Multiplier: 100}}); err != nil {
		return err
	}
	return manager.ProcessOptionSignal(ctx, scope, signal, plan)
}

func buildPaperSpreadClosePlan(positions []*domain.Position, chain []domain.OptionSnapshot) (*domain.OptionSpread, float64, error) {
	if len(positions) != 2 || positions[0] == nil || positions[0].LegGroupID == nil {
		return nil, 0, errors.New("options runtime: exactly one persisted two-leg group is required for atomic close")
	}
	first, quantity := positions[0], positions[0].Quantity
	if first.OptionType == nil || first.Expiry == nil || quantity <= 0 {
		return nil, 0, errors.New("options runtime: spread position metadata is incomplete")
	}
	strategyType := domain.StrategyBullCallSpread
	if *first.OptionType == domain.OptionTypePut {
		strategyType = domain.StrategyBearPutSpread
	}
	spread := &domain.OptionSpread{StrategyType: strategyType, Underlying: first.UnderlyingTicker}
	for _, position := range positions {
		if position == nil || position.LegGroupID == nil || *position.LegGroupID != *first.LegGroupID || position.Quantity != quantity || position.UnderlyingTicker != first.UnderlyingTicker || position.OptionType == nil || *position.OptionType != *first.OptionType || position.Expiry == nil || !position.Expiry.Equal(*first.Expiry) || position.Strike == nil {
			return nil, 0, errors.New("options runtime: spread legs do not share durable group and contract semantics")
		}
		price, err := executableOptionClosePrice(position, chain)
		if err != nil {
			return nil, 0, err
		}
		contract := domain.OptionContract{OCCSymbol: position.Ticker, Underlying: position.UnderlyingTicker, OptionType: *position.OptionType, Strike: *position.Strike, Expiry: *position.Expiry, Multiplier: position.ContractMultiplier}
		leg := domain.SpreadLeg{Contract: contract, Ratio: 1, Quantity: quantity, ExecutablePrice: price}
		if position.Side == domain.PositionSideLong {
			leg.Side, leg.PositionIntent = domain.OrderSideSell, domain.PositionIntentSellToClose
		} else {
			leg.Side, leg.PositionIntent = domain.OrderSideBuy, domain.PositionIntentBuyToClose
		}
		if position.Delta != nil {
			leg.Greeks.Delta = *position.Delta
		}
		if position.Gamma != nil {
			leg.Greeks.Gamma = *position.Gamma
		}
		if position.Theta != nil {
			leg.Greeks.Theta = *position.Theta
		}
		if position.Vega != nil {
			leg.Greeks.Vega = *position.Vega
		}
		spread.Legs = append(spread.Legs, leg)
	}
	return spread, quantity, nil
}

func (r *realStrategyRunner) enforceOptionsGreekRisk(ctx context.Context, scope execution.ExecutionScope, underlying string, proposed []risk.OptionLegExposure) error {
	to := time.Now().UTC()
	bars, err := r.dataService.GetOHLCV(ctx, domain.MarketTypeStock, underlying, data.Timeframe1d, to.Add(-7*24*time.Hour), to)
	if err != nil || len(bars) == 0 || bars[len(bars)-1].Close <= 0 {
		return fmt.Errorf("options runtime: underlying price required for Greek risk: %w", err)
	}
	balance, err := r.localPaperBroker.GetAccountBalance(ctx)
	if err != nil {
		return fmt.Errorf("options runtime: paper balance for Greek risk: %w", err)
	}
	scoped, ok := r.positionRepo.(repository.AccountScopedPositionRepository)
	if !ok {
		return errors.New("options runtime: account-scoped position repository is required for Greek risk")
	}
	var positions []domain.Position
	for offset := 0; ; offset += 1000 {
		page, err := scoped.GetOpenByAccount(ctx, scope.AccountID(), scope.Environment(), repository.PositionFilter{}, 1000, offset)
		if err != nil {
			return fmt.Errorf("options runtime: open positions for Greek risk: %w", err)
		}
		positions = append(positions, page...)
		if len(page) < 1000 {
			break
		}
	}
	_, allowed, reason := risk.CheckOptionsExposure(risk.DefaultOptionsLimits(), balance.Equity, bars[len(bars)-1].Close, positions, proposed)
	if !allowed {
		return errors.New(reason)
	}
	return nil
}

func buildPaperDebitSpreadPlan(cfg *rules.OptionsRulesConfig, chain []domain.OptionSnapshot, now time.Time) (*domain.OptionSpread, float64, error) {
	if cfg == nil || len(cfg.LegSelection) != 2 {
		return nil, 0, errors.New("options runtime: paper debit vertical requires exactly two legs")
	}
	selected, err := rules.SelectSpreadLegs(chain, cfg.LegSelection, now)
	if err != nil {
		return nil, 0, fmt.Errorf("options runtime: select spread legs: %w", err)
	}
	spread, err := rules.BuildSpread(cfg.StrategyType, cfg.Underlying, selected, cfg.LegSelection)
	if err != nil {
		return nil, 0, fmt.Errorf("options runtime: build spread: %w", err)
	}
	var netDebit, minStrike, maxStrike float64
	minStrike = math.Inf(1)
	for index := range spread.Legs {
		leg := &spread.Legs[index]
		snapshot := selectedLegSnapshot(selected, leg.Contract.OCCSymbol)
		if snapshot == nil || snapshot.Bid <= 0 || snapshot.Ask <= 0 || snapshot.Ask < snapshot.Bid {
			return nil, 0, errors.New("options runtime: every spread leg requires a valid executable bid/ask")
		}
		if leg.Side == domain.OrderSideBuy {
			leg.ExecutablePrice = snapshot.Ask
			netDebit += snapshot.Ask * float64(leg.Ratio)
		} else {
			leg.ExecutablePrice = snapshot.Bid
			netDebit -= snapshot.Bid * float64(leg.Ratio)
		}
		leg.Bid = snapshot.Bid
		leg.Ask = snapshot.Ask
		leg.BidSize = snapshot.BidSize
		leg.AskSize = snapshot.AskSize
		leg.ContractPayloadID = snapshot.ContractPayloadID
		leg.ContractSHA256 = snapshot.ContractSHA256
		leg.QuotePayloadID = snapshot.QuotePayloadID
		leg.QuoteSHA256 = snapshot.QuoteSHA256
		leg.SnapshotPayloadID = snapshot.SnapshotPayloadID
		leg.SnapshotSHA256 = snapshot.SnapshotSHA256
		leg.QuoteObservedAt = snapshot.QuoteObservedAt
		if spread.QuoteObservedAt.IsZero() || (!snapshot.QuoteObservedAt.IsZero() && snapshot.QuoteObservedAt.Before(spread.QuoteObservedAt)) {
			spread.QuoteObservedAt = snapshot.QuoteObservedAt
		}
		leg.Greeks = snapshot.Greeks
		minStrike = math.Min(minStrike, leg.Contract.Strike)
		maxStrike = math.Max(maxStrike, leg.Contract.Strike)
	}
	if netDebit == 0 || len(spread.Legs) == 0 {
		return nil, 0, errors.New("options runtime: vertical package must have a finite nonzero debit or credit")
	}
	multiplier := spread.Legs[0].Contract.Multiplier
	width := maxStrike - minStrike
	if netDebit > 0 {
		spread.MaxRisk = netDebit * multiplier
		spread.MaxReward = (width - netDebit) * multiplier
	} else {
		credit := -netDebit
		spread.MaxRisk = (width - credit) * multiplier
		spread.MaxReward = credit * multiplier
	}
	if spread.MaxReward <= 0 {
		return nil, 0, errors.New("options runtime: debit vertical has no finite positive max reward")
	}
	spread.LiquidityUSD, spread.SpreadPct = optionSpreadLiquidity(spread)
	if spread.LiquidityUSD <= 0 || spread.SpreadPct <= 0 || math.IsNaN(spread.SpreadPct) || math.IsInf(spread.SpreadPct, 0) {
		return nil, 0, errors.New("options runtime: vertical lacks executable quote depth or spread evidence")
	}
	quantity := 0.0
	switch cfg.PositionSizing.Method {
	case "fixed_contracts":
		quantity = float64(cfg.PositionSizing.FixedContracts)
	case "premium_budget":
		quantity = math.Floor(cfg.PositionSizing.PremiumBudget / spread.MaxRisk)
	case "max_risk":
		quantity = math.Floor(cfg.PositionSizing.MaxRiskUSD / spread.MaxRisk)
	}
	if quantity < 1 {
		return nil, 0, errors.New("options runtime: sizing budget cannot purchase one spread")
	}
	return spread, quantity, nil
}

func optionSpreadLiquidity(spread *domain.OptionSpread) (float64, float64) {
	if spread == nil || len(spread.Legs) != 2 || spread.MaxRisk <= 0 {
		return 0, 0
	}
	availableContracts := math.Inf(1)
	var netMid, quoteWidth float64
	for _, leg := range spread.Legs {
		size := leg.AskSize
		sign := 1.0
		if leg.Side == domain.OrderSideSell {
			size, sign = leg.BidSize, -1
		}
		if size <= 0 || leg.Bid <= 0 || leg.Ask < leg.Bid {
			return 0, 0
		}
		availableContracts = math.Min(availableContracts, size/float64(leg.Ratio))
		netMid += sign * ((leg.Bid + leg.Ask) / 2) * float64(leg.Ratio)
		quoteWidth += (leg.Ask - leg.Bid) * float64(leg.Ratio)
	}
	if availableContracts <= 0 || math.IsInf(availableContracts, 1) || netMid == 0 || quoteWidth <= 0 {
		return 0, 0
	}
	return math.Floor(availableContracts) * spread.MaxRisk, quoteWidth / math.Abs(netMid)
}

func selectedLegSnapshot(selected map[string]*domain.OptionSnapshot, symbol string) *domain.OptionSnapshot {
	for _, snapshot := range selected {
		if snapshot != nil && snapshot.Contract.OCCSymbol == symbol {
			return snapshot
		}
	}
	return nil
}

func executableOptionClosePrice(position *domain.Position, chain []domain.OptionSnapshot) (float64, error) {
	if position == nil {
		return 0, errors.New("options runtime: position is required")
	}
	for _, snapshot := range chain {
		if snapshot.Contract.OCCSymbol != position.Ticker {
			continue
		}
		price := snapshot.Bid
		if position.Side == domain.PositionSideShort {
			price = snapshot.Ask
		}
		if price <= 0 || snapshot.Ask <= 0 || snapshot.Bid <= 0 || snapshot.Ask < snapshot.Bid {
			return 0, errors.New("options runtime: valid executable close bid/ask is required")
		}
		return price, nil
	}
	return 0, fmt.Errorf("options runtime: close quote for %s not found", position.Ticker)
}

func buildPaperSingleLegPlan(cfg *rules.OptionsRulesConfig, chain []domain.OptionSnapshot, now time.Time) (execution.TradingPlan, error) {
	if cfg == nil || len(cfg.LegSelection) != 1 {
		return execution.TradingPlan{}, errors.New("options runtime: only atomic single-leg plans are currently executable")
	}
	var selector rules.LegSelector
	for _, value := range cfg.LegSelection {
		selector = value
	}
	if selector.Side != domain.OrderSideBuy || selector.Intent != domain.PositionIntentBuyToOpen {
		return execution.TradingPlan{}, errors.New("options runtime: uncovered or non-opening short options are disabled")
	}
	snapshot, err := rules.SelectLeg(chain, selector, now)
	if err != nil {
		return execution.TradingPlan{}, fmt.Errorf("options runtime: select contract: %w", err)
	}
	if snapshot.Ask <= 0 || snapshot.Bid <= 0 || snapshot.Ask < snapshot.Bid {
		return execution.TradingPlan{}, errors.New("options runtime: valid executable bid/ask is required")
	}
	quantity := 0.0
	switch cfg.PositionSizing.Method {
	case "fixed_contracts":
		quantity = float64(cfg.PositionSizing.FixedContracts)
	case "premium_budget":
		quantity = float64(int(cfg.PositionSizing.PremiumBudget / (snapshot.Ask * snapshot.Contract.Multiplier)))
	case "max_risk":
		quantity = float64(int(cfg.PositionSizing.MaxRiskUSD / (snapshot.Ask * snapshot.Contract.Multiplier)))
	}
	if quantity < 1 {
		return execution.TradingPlan{}, errors.New("options runtime: sizing budget cannot purchase one contract")
	}
	greeks := snapshot.Greeks
	return execution.TradingPlan{Action: domain.PipelineSignalBuy, MarketType: domain.MarketTypeOptions, Ticker: snapshot.Contract.OCCSymbol, EntryType: "limit", EntryPrice: snapshot.Ask, PositionSize: quantity, OptionGreeks: &greeks}, nil
}

func (r *realStrategyRunner) runPolymarketNative(ctx context.Context, strategy domain.Strategy, executionVersionID uuid.UUID) (*api.StrategyRunResult, error) {
	executionStrategy := r.effectivePolymarketExecutionStrategy(strategy)

	strategyConfig, err := parseStrategyConfig(strategy.Config)
	if err != nil {
		return nil, recognizedRunControlError(ctx, err)
	}
	resolved := agent.ResolveConfig(strategyConfig, r.globals)
	now := time.Now().UTC()
	run := domain.PipelineRun{
		ID:             uuid.New(),
		StrategyID:     strategy.ID,
		Ticker:         strategy.Ticker,
		TradeDate:      now.Truncate(24 * time.Hour),
		Status:         domain.PipelineStatusRunning,
		StartedAt:      now,
		ConfigSnapshot: strategy.Config,
		AccountID:      r.executionAccount.AccountID(),
		Environment:    r.executionAccount.Environment(),
		OriginType:     "strategy_version",
		OriginID:       executionVersionID.String(),
	}
	if err := bindStrategyRunScope(&run, r.executionAccount, executionVersionID); err != nil {
		return nil, err
	}
	runCtx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	if r.runRegistry != nil {
		if err := r.runRegistry.Register(run.ID, run.TradeDate, cancelRun); err != nil {
			return nil, fmt.Errorf("polymarket native: register run context: %w", err)
		}
		defer r.runRegistry.Deregister(run.ID, run.TradeDate)
	}
	ctx = runCtx
	if err := r.startNativeRun(ctx, "polymarket", &run); err != nil {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, recognizedRunControlError(ctx, err)
	}

	completeRun := func(status domain.PipelineStatus, signal domain.PipelineSignal, errMsg string) error {
		return r.completeNativeRun(ctx, "polymarket", &run, status, signal, errMsg)
	}
	failRun := func(err error) (*api.StrategyRunResult, error) {
		status, message := nativeFailure(ctx, err)
		if updateErr := completeRun(status, domain.PipelineSignalHold, message); updateErr != nil {
			err = errors.Join(err, updateErr)
		}
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, recognizedRunControlError(ctx, err)
	}

	if r.polymarketMarketData == nil {
		return failRun(errors.New("polymarket native: market data client is required"))
	}
	marketData, err := r.polymarketMarketData.GetMarketData(ctx, strategy.Ticker)
	if err != nil {
		return failRun(fmt.Errorf("polymarket native: fetch market data for %s: %w", strategy.Ticker, err))
	}
	snapshot := polymarketexecution.SnapshotFromPredictionMarketData(marketData, time.Now().UTC())
	scope, err := executionScopeFromPersistedRun(&run, strategy)
	if err != nil {
		return failRun(err)
	}
	if err := r.persistPolymarketNativeSnapshot(ctx, scope, snapshot); err != nil {
		return failRun(err)
	}

	executor := polymarketexecution.NewDeterministicNativeExecutor()
	decision, err := executor.Execute(ctx, strategy, snapshot, scope)
	if err != nil {
		return failRun(fmt.Errorf("polymarket native: execute strategy %s: %w", strategy.Name, err))
	}
	signal := decision.Signal
	if signal == "" {
		signal = domain.PipelineSignalHold
	}
	if signal == domain.PipelineSignalBuy {
		sizingConfig := applyPolymarketSizingCap(strategy.MarketType, sizingConfigForStrategy(ctx, strategy, strategyConfig, resolved, r.positionRepo, r.logger, scope), r.cfg.Risk.Polymarket.MaxPositionUSDC)
		plannedNotional, err := r.plannedPolymarketNotional(ctx, executionStrategy, sizingConfig, decision)
		if err != nil {
			return failRun(err)
		}
		if err := r.checkPolymarketNativePreconditions(snapshot, decision, plannedNotional); err != nil {
			return failRun(err)
		}
	}

	orderManager, err := r.newOrderManager(ctx, executionStrategy, resolved, strategyConfig, scope)
	if err != nil {
		return failRun(err)
	}
	finalSignal := execution.FinalSignal{Signal: signal, Confidence: decision.Confidence}
	tradingPlan := execution.TradingPlan{
		Action:           signal,
		MarketType:       domain.MarketTypePolymarket,
		Ticker:           strategy.Ticker,
		EntryType:        decision.EntryType,
		EntryPrice:       decision.EntryPrice,
		StopLoss:         decision.StopLoss,
		TakeProfit:       decision.TakeProfit,
		TimeHorizon:      decision.TimeHorizon,
		Confidence:       decision.Confidence,
		Rationale:        decision.Rationale,
		RiskReward:       decision.RiskReward,
		Side:             decision.Side,
		ExternalMarketID: snapshot.ConditionID,
		FairValue:        decision.FairProbability, Spread: decision.Spread, Depth: decision.Depth,
		GrossEV: decision.GrossEdge, NetEV: decision.NetEdge,
		Evidence:   predictionNativeEvidence("polymarket", snapshot, decision),
		Features:   predictionNativeFeatures(decision),
		RegimeTags: []string{"event_market", "deterministic_execution", decision.Template},
	}
	if err := ctx.Err(); err != nil {
		return failRun(err)
	}
	if err := completeRun(domain.PipelineStatusCompleted, signal, ""); err != nil {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, err
	}
	if run.Status != domain.PipelineStatusCompleted {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, fmt.Errorf("polymarket native: completed terminal authority required: durable status=%s signal=%s", run.Status, run.Signal)
	}
	canonical := &api.StrategyRunResult{Run: run, Signal: run.Signal}
	if !r.portfolioAllocatorOwnsPaperExecution(strategy, signal) {
		if err := orderManager.ProcessSignal(ctx, scope, finalSignal, tradingPlan); err != nil {
			return canonical, err
		}
	}
	if err := r.recordPortfolioOpportunity(ctx, strategy, &run, finalSignal, tradingPlan, nil); err != nil {
		return canonical, err
	}
	orders, err := loadResultOrders(ctx, r.orderRepo, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})
	if err != nil {
		return canonical, err
	}
	if err = validateResultOrders(orders, r.executionAccount, executionVersionID, run.ID, run.TradeDate); err != nil {
		return canonical, err
	}
	positions, err := loadResultPositions(ctx, r.positionRepo, r.executionAccount, executionVersionID)
	if err != nil {
		return canonical, err
	}
	if !executionStrategy.IsPaper {
		if err := r.registerPolymarketPositions(positions); err != nil {
			return canonical, err
		}
	}
	return &api.StrategyRunResult{Run: run, Signal: run.Signal, Orders: orders, Positions: positions}, nil
}

func (r *realStrategyRunner) runKalshiNative(ctx context.Context, strategy domain.Strategy, executionVersionID uuid.UUID) (*api.StrategyRunResult, error) {
	now := time.Now().UTC()
	run := domain.PipelineRun{
		ID:             uuid.New(),
		StrategyID:     strategy.ID,
		Ticker:         strategy.Ticker,
		TradeDate:      now.Truncate(24 * time.Hour),
		Status:         domain.PipelineStatusRunning,
		StartedAt:      now,
		ConfigSnapshot: strategy.Config,
		AccountID:      r.executionAccount.AccountID(),
		Environment:    r.executionAccount.Environment(),
		OriginType:     "strategy_version",
		OriginID:       executionVersionID.String(),
	}
	if err := bindStrategyRunScope(&run, r.executionAccount, executionVersionID); err != nil {
		return nil, err
	}
	runCtx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	if r.runRegistry != nil {
		if err := r.runRegistry.Register(run.ID, run.TradeDate, cancelRun); err != nil {
			return nil, fmt.Errorf("kalshi native: register run context: %w", err)
		}
		defer r.runRegistry.Deregister(run.ID, run.TradeDate)
	}
	ctx = runCtx
	if err := r.startNativeRun(ctx, "kalshi", &run); err != nil {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, recognizedRunControlError(ctx, err)
	}

	completeRun := func(status domain.PipelineStatus, signal domain.PipelineSignal, errMsg string) error {
		return r.completeNativeRun(ctx, "kalshi", &run, status, signal, errMsg)
	}
	failRun := func(err error) (*api.StrategyRunResult, error) {
		status, message := nativeFailure(ctx, err)
		if updateErr := completeRun(status, domain.PipelineSignalHold, message); updateErr != nil {
			err = errors.Join(err, updateErr)
		}
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, recognizedRunControlError(ctx, err)
	}

	if r.kalshiMarketData == nil {
		r.kalshiMarketData = r.kalshiDataProvider
	}
	if r.kalshiMarketData == nil {
		return failRun(errors.New("kalshi native: market data client is required"))
	}
	if !strategy.IsPaper {
		if _, _, err := r.newBrokerForStrategy(strategy); err != nil {
			return failRun(err)
		}
	}
	snapshot, err := r.kalshiMarketData.LoadSnapshot(ctx, strategy.Ticker)
	if err != nil {
		return failRun(fmt.Errorf("kalshi native: fetch snapshot for %s: %w", strategy.Ticker, err))
	}
	scope, err := executionScopeFromPersistedRun(&run, strategy)
	if err != nil {
		return failRun(err)
	}
	if err := r.persistKalshiNativeSnapshot(ctx, scope, snapshot); err != nil {
		return failRun(err)
	}

	var openPositions []domain.Position
	if r.cfg.Brokers.Kalshi.AutoExitsEnabled && r.positionRepo != nil {
		scopedPositions, ok := r.positionRepo.(repository.ExecutionScopedPositionRepository)
		if !ok {
			return failRun(errors.New("kalshi native: canonical execution-scoped position repository is required"))
		}
		openPositions, err = scopedPositions.GetByExecutionScope(ctx, r.executionAccount.AccountID(), r.executionAccount.Environment(), string(ledger.ExecutionOriginStrategyVersion), executionVersionID.String(), repository.PositionFilter{}, 10_000, 0)
		if err != nil {
			return failRun(fmt.Errorf("kalshi native: load positions for exit evaluation: %w", err))
		}
	}

	decision, exit := kalshiexecution.EvaluateExit(strategy, snapshot, openPositions, now)
	if !exit {
		decision, err = kalshiexecution.DeterministicNativeExecutor{}.Execute(ctx, strategy, snapshot, scope)
	} else {
		decision.Scope = scope
	}
	if err != nil {
		return failRun(fmt.Errorf("kalshi native: execute strategy %s: %w", strategy.Name, err))
	}
	signal := decision.Signal
	if signal == "" {
		signal = domain.PipelineSignalHold
	}

	if signal == domain.PipelineSignalHold {
		if err := ctx.Err(); err != nil {
			return failRun(err)
		}
		if err := completeRun(domain.PipelineStatusCompleted, signal, ""); err != nil {
			return &api.StrategyRunResult{Run: run, Signal: run.Signal}, err
		}
		if run.Status != domain.PipelineStatusCompleted {
			return &api.StrategyRunResult{Run: run, Signal: run.Signal}, fmt.Errorf("kalshi native: completed terminal authority required: durable status=%s signal=%s", run.Status, run.Signal)
		}
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, nil
	}

	strategyConfig, err := parseStrategyConfig(strategy.Config)
	if err != nil {
		return failRun(fmt.Errorf("kalshi native: parse strategy config: %w", err))
	}
	resolved := agent.ResolveConfig(strategyConfig, r.globals)
	orderManager, err := r.newOrderManager(ctx, strategy, resolved, strategyConfig, scope)
	if err != nil {
		return failRun(err)
	}
	finalSignal := execution.FinalSignal{Signal: signal, Confidence: decision.Confidence}
	tradingPlan := kalshiTradingPlan(signal, snapshot, decision, strategy.Ticker)
	if err := ctx.Err(); err != nil {
		return failRun(err)
	}
	if err := completeRun(domain.PipelineStatusCompleted, signal, ""); err != nil {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, err
	}
	if run.Status != domain.PipelineStatusCompleted {
		return &api.StrategyRunResult{Run: run, Signal: run.Signal}, fmt.Errorf("kalshi native: completed terminal authority required: durable status=%s signal=%s", run.Status, run.Signal)
	}
	canonical := &api.StrategyRunResult{Run: run, Signal: run.Signal}
	if !r.portfolioAllocatorOwnsPaperExecution(strategy, signal) {
		if err := orderManager.ProcessSignal(ctx, scope, finalSignal, tradingPlan); err != nil {
			return canonical, err
		}
	}
	if err := r.recordPortfolioOpportunity(ctx, strategy, &run, finalSignal, tradingPlan, nil); err != nil {
		return canonical, err
	}

	var orders []domain.Order
	if r.orderRepo != nil {
		orders, err = loadResultOrders(ctx, r.orderRepo, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})
		if err != nil {
			return canonical, err
		}
		if err = validateResultOrders(orders, r.executionAccount, executionVersionID, run.ID, run.TradeDate); err != nil {
			return canonical, err
		}
	}
	var positions []domain.Position
	if r.positionRepo != nil {
		positions, err = loadResultPositions(ctx, r.positionRepo, r.executionAccount, executionVersionID)
		if err != nil {
			return canonical, err
		}
	}

	return &api.StrategyRunResult{Run: run, Signal: run.Signal, Orders: orders, Positions: positions}, nil
}

func nativeFailure(ctx context.Context, err error) (domain.PipelineStatus, string) {
	if runcontrol.IsCancelled(ctx) {
		return domain.PipelineStatusCancelled, context.Cause(ctx).Error()
	}
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return domain.PipelineStatusFailed, cause.Error()
	}
	return domain.PipelineStatusFailed, err.Error()
}

func recognizedRunControlError(ctx context.Context, err error) error {
	if cause, ok := runcontrol.TypedCause(ctx); ok && cause != runcontrol.Stale && !errors.Is(err, cause) {
		return errors.Join(err, context.Cause(ctx))
	}
	return err
}

func (r *realStrategyRunner) startNativeRun(ctx context.Context, source string, run *domain.PipelineRun) error {
	if run == nil {
		return fmt.Errorf("%s native: pipeline run is required", source)
	}
	if r.runRepo == nil {
		return fmt.Errorf("%s native: pipeline run repository is required", source)
	}
	if r.eventRepo == nil {
		return fmt.Errorf("%s native: agent event repository is required", source)
	}
	scope, err := persistenceScopeFromRun(run)
	if err != nil {
		return fmt.Errorf("%s native: validate run scope: %w", source, err)
	}
	event := &domain.AgentEvent{
		PipelineRunID:        &run.ID,
		PipelineRunTradeDate: &run.TradeDate,
		StrategyID:           &run.StrategyID,
		EventKind:            agent.AgentEventKindPipelineStarted.String(),
		Title:                "Pipeline started",
		Summary:              "Native deterministic pipeline admitted for evaluation.",
		Tags:                 []string{"pipeline", "native", source},
	}
	if err := applyEventPersistenceScope(event, scope); err != nil {
		return fmt.Errorf("%s native: scope start event: %w", source, err)
	}
	persistCtx, cancel := context.WithTimeout(ctx, nativeTerminalTimeout)
	if err := r.runRepo.Create(persistCtx, run); err != nil {
		cancel()
		return recognizedRunControlError(ctx, fmt.Errorf("%s native: create run: %w", source, err))
	}
	cancel()

	metadata, err := json.Marshal(map[string]string{"execution_path": source + "_native"})
	if err != nil {
		return fmt.Errorf("%s native: marshal start event: %w", source, err)
	}
	event.Metadata = metadata
	eventCtx, eventCancel := context.WithTimeout(ctx, nativeTerminalTimeout)
	eventErr := r.eventRepo.Create(eventCtx, event)
	eventCancel()
	if eventErr == nil {
		return nil
	}

	completedAt := time.Now().UTC()
	fallbackSignal := domain.PipelineSignalHold
	status, message := nativeFailure(ctx, eventErr)
	terminalEvent, terminalEventErr := nativeTerminalEvent(source, run, status, fallbackSignal)
	if terminalEventErr != nil {
		return recognizedRunControlError(ctx, errors.Join(fmt.Errorf("%s native: persist start event: %w", source, eventErr), terminalEventErr))
	}
	updateCtx, updateCancel := context.WithTimeout(context.WithoutCancel(ctx), nativeTerminalTimeout)
	receipt, updateErr := r.runRepo.Finalize(updateCtx, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, repository.PipelineRunFinalization{Status: status, Signal: &fallbackSignal, CompletedAt: completedAt, ErrorMessage: message, Event: terminalEvent})
	updateCancel()
	if updateErr != nil {
		return recognizedRunControlError(ctx, errors.Join(
			fmt.Errorf("%s native: persist start event: %w", source, eventErr),
			fmt.Errorf("%s native: persist start-event failure status: %w", source, updateErr),
		))
	}
	*run = receipt.Run
	if !receipt.Applied {
		lostErr := fmt.Errorf("%s native: %w: durable status=%s signal=%s", source, agent.ErrLostTerminalAuthority, receipt.Run.Status, receipt.Run.Signal)
		if receipt.Run.Status == domain.PipelineStatusCancelled {
			lostErr = runcontrol.JoinCauseFromErrorMessage(lostErr, receipt.Run.ErrorMessage)
		}
		return recognizedRunControlError(ctx, errors.Join(
			fmt.Errorf("%s native: persist start event: %w", source, eventErr),
			lostErr,
		))
	}
	return recognizedRunControlError(ctx, fmt.Errorf("%s native: persist start event: %w", source, eventErr))
}

func (r *realStrategyRunner) completeNativeRun(
	ctx context.Context,
	source string,
	run *domain.PipelineRun,
	status domain.PipelineStatus,
	signal domain.PipelineSignal,
	errMsg string,
) error {
	if run == nil {
		return fmt.Errorf("%s native: pipeline run is required", source)
	}
	if r.runRepo == nil {
		return fmt.Errorf("%s native: pipeline run repository is required", source)
	}

	completedAt := time.Now().UTC()
	event, err := nativeTerminalEvent(source, run, status, signal)
	if err != nil {
		return err
	}
	finalization := repository.PipelineRunFinalization{Status: status, Signal: &signal, CompletedAt: completedAt, ErrorMessage: errMsg, Event: event}
	persistParent := context.WithoutCancel(ctx)
	if status == domain.PipelineStatusCompleted {
		persistParent = ctx
	}
	persistCtx, cancel := context.WithTimeout(persistParent, nativeTerminalTimeout)
	receipt, err := r.runRepo.Finalize(persistCtx, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, finalization)
	cancel()
	if err != nil && status == domain.PipelineStatusCompleted && ctx.Err() != nil {
		fallbackStatus, fallbackMessage := nativeFailure(ctx, ctx.Err())
		fallbackSignal := domain.PipelineSignalHold
		fallbackEvent, eventErr := nativeTerminalEvent(source, run, fallbackStatus, fallbackSignal)
		if eventErr != nil {
			return recognizedRunControlError(ctx, errors.Join(fmt.Errorf("%s native: finalize run: %w", source, err), eventErr))
		}
		fallbackCtx, fallbackCancel := context.WithTimeout(context.WithoutCancel(ctx), nativeTerminalTimeout)
		receipt, err = r.runRepo.Finalize(fallbackCtx, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, repository.PipelineRunFinalization{Status: fallbackStatus, Signal: &fallbackSignal, CompletedAt: time.Now().UTC(), ErrorMessage: fallbackMessage, Event: fallbackEvent})
		fallbackCancel()
	}
	if err != nil {
		return recognizedRunControlError(ctx, fmt.Errorf("%s native: finalize run: %w", source, err))
	}
	*run = receipt.Run
	if !receipt.Applied {
		err := fmt.Errorf("%s native: terminal finalization lost: %w: durable status=%s signal=%s", source, agent.ErrLostTerminalAuthority, receipt.Run.Status, receipt.Run.Signal)
		if receipt.Run.Status == domain.PipelineStatusCancelled {
			err = runcontrol.JoinCauseFromErrorMessage(err, receipt.Run.ErrorMessage)
		}
		return recognizedRunControlError(ctx, err)
	}
	if status == domain.PipelineStatusCompleted && receipt.Run.Status == domain.PipelineStatusCompleted {
		return nil
	}
	if status == domain.PipelineStatusCompleted && receipt.Applied && ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return nil
}

func nativeTerminalEvent(
	source string,
	run *domain.PipelineRun,
	status domain.PipelineStatus,
	signal domain.PipelineSignal,
) (*domain.AgentEvent, error) {
	scope, err := persistenceScopeFromRun(run)
	if err != nil {
		return nil, fmt.Errorf("%s native: validate terminal scope: %w", source, err)
	}
	eventKind := agent.AgentEventKindPipelineFailed.String()
	title := "Pipeline failed"
	tags := []string{"pipeline", "failed", "native", source}
	switch status {
	case domain.PipelineStatusCompleted:
		eventKind = agent.AgentEventKindPipelineCompleted.String()
		title = "Pipeline completed"
		tags = []string{"pipeline", "completed", "native", source}
	case domain.PipelineStatusCancelled:
		eventKind = agent.AgentEventKindPipelineCancelled.String()
		title = "Pipeline cancelled"
		tags = []string{"pipeline", "cancelled", "native", source}
	}
	metadata, err := json.Marshal(map[string]string{
		"execution_path": source + "_native",
		"signal":         string(signal),
		"status":         string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("%s native: marshal terminal event: %w", source, err)
	}
	event := &domain.AgentEvent{
		PipelineRunID:        &run.ID,
		PipelineRunTradeDate: &run.TradeDate,
		StrategyID:           &run.StrategyID,
		EventKind:            eventKind,
		Title:                title,
		Summary:              "Native deterministic pipeline reached a terminal state.",
		Tags:                 tags,
		Metadata:             metadata,
	}
	if err := applyEventPersistenceScope(event, scope); err != nil {
		return nil, fmt.Errorf("%s native: scope terminal event: %w", source, err)
	}
	return event, nil
}

func predictionNativeEvidence(provider string, snapshot, decision any) json.RawMessage {
	raw, err := json.Marshal(map[string]any{
		"provider": provider, "snapshot": snapshot, "decision": decision,
		"llm_role": "advisory_discovery_only", "execution_authority": "deterministic_native_gates",
	})
	if err != nil {
		return json.RawMessage(`{"error":"prediction evidence serialization failed"}`)
	}
	return raw
}

func kalshiTradingPlan(signal domain.PipelineSignal, snapshot kalshiexecution.Snapshot, decision kalshiexecution.NativeDecision, ticker string) execution.TradingPlan {
	tradingPlan := execution.TradingPlan{
		Action:           signal,
		MarketType:       domain.MarketTypeKalshi,
		Ticker:           ticker,
		EntryType:        decision.EntryType,
		EntryPrice:       decision.EntryPrice,
		PositionSize:     decision.PositionSize,
		Confidence:       decision.Confidence,
		Rationale:        decision.Rationale,
		RiskReward:       decision.RiskReward,
		Side:             decision.Side,
		TimeHorizon:      decision.TimeHorizon,
		ExternalMarketID: ticker,
		FairValue:        decision.FairProbability,
		Spread:           decision.Spread,
		Depth:            decision.Depth,
		GrossEV:          decision.GrossEdge,
		NetEV:            decision.NetEdge,
		Evidence:         predictionNativeEvidence("kalshi", snapshot, decision),
		Features:         predictionNativeFeatures(decision),
		RegimeTags:       []string{"event_market", "deterministic_execution", decision.Template},
	}
	if decision.EntryPrice > 0 {
		tradingPlan.ReferencePrice = decision.EntryPrice
	}
	return tradingPlan
}

func predictionNativeFeatures(decision any) json.RawMessage {
	raw, err := json.Marshal(decision)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (r *realStrategyRunner) persistPolymarketNativeSnapshot(ctx context.Context, scope execution.ExecutionScope, snapshot polymarketexecution.Snapshot) error {
	if r.snapshotRepo == nil {
		return errors.New("polymarket native: snapshot repository is required")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("polymarket native: marshal snapshot: %w", err)
	}
	persistCtx, cancel := context.WithTimeout(ctx, nativeTerminalTimeout)
	defer cancel()
	item, err := pipelineSnapshotFromScope(scope, "polymarket_native_snapshot", payload)
	if err != nil {
		return err
	}
	if err := r.snapshotRepo.Create(persistCtx, item); err != nil {
		return fmt.Errorf("polymarket native: persist snapshot: %w", err)
	}
	return nil
}

func (r *realStrategyRunner) persistKalshiNativeSnapshot(ctx context.Context, scope execution.ExecutionScope, snapshot kalshiexecution.Snapshot) error {
	if r.snapshotRepo == nil {
		return errors.New("kalshi native: snapshot repository is required")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("kalshi native: marshal snapshot: %w", err)
	}
	persistCtx, cancel := context.WithTimeout(ctx, nativeTerminalTimeout)
	defer cancel()
	item, err := pipelineSnapshotFromScope(scope, "kalshi_native_snapshot", payload)
	if err != nil {
		return err
	}
	if err := r.snapshotRepo.Create(persistCtx, item); err != nil {
		return fmt.Errorf("kalshi native: persist snapshot: %w", err)
	}
	return nil
}

func (r *realStrategyRunner) checkPolymarketNativePreconditions(snapshot polymarketexecution.Snapshot, decision polymarketexecution.NativeDecision, plannedNotional float64) error {
	limits := r.cfg.Risk.Polymarket
	minLiquidity := limits.MinLiquidity
	if minLiquidity <= 0 {
		minLiquidity = 1000
	}
	now := time.Now().UTC()
	if err := snapshot.ValidateExecutableSide(decision.Side, minLiquidity, now); err != nil {
		return fmt.Errorf("polymarket native: %w", err)
	}
	bid, ask := snapshot.BidAskForSide(decision.Side)
	spread := ask - bid
	if spread <= 0 {
		return fmt.Errorf("polymarket native: executable %s bid/ask spread is required", decision.Side)
	}
	mid := ask - spread/2
	spreadMid := 0.0
	if mid > 0 {
		spreadMid = spread / mid
	}
	daysToResolution := int(snapshot.EndDate.Sub(now).Hours() / 24)
	if ok, reason := risk.CheckPolymarketPreConditions(risk.PolymarketLimits{
		MinLiquidity:        minLiquidity,
		MaxSpreadPct:        limits.MaxSpreadPct,
		MinDaysToResolution: limits.MinDaysToResolution,
		MaxPositionUSDC:     limits.MaxPositionUSDC,
	}, snapshot.Liquidity, spreadMid, daysToResolution, plannedNotional); !ok {
		return errors.New(reason)
	}
	return nil
}

func (r *realStrategyRunner) plannedPolymarketNotional(ctx context.Context, strategy domain.Strategy, sizingConfig execution.SizingConfig, decision polymarketexecution.NativeDecision) (float64, error) {
	if decision.EntryPrice <= 0 {
		return 0, nil
	}
	broker, _, err := r.newBrokerForStrategy(strategy)
	if err != nil {
		return 0, err
	}
	if broker == nil {
		return 0, errors.New("polymarket native: broker is required to estimate position size")
	}
	balance, err := broker.GetAccountBalance(ctx)
	if err != nil {
		return 0, fmt.Errorf("polymarket native: get account balance: %w", err)
	}
	quantity := execution.PolymarketPositionSize(execution.PolymarketSizingParams{
		AccountValue:    balance.Equity,
		FractionPct:     sizingConfig.FractionPct,
		MaxPositionUSDC: sizingConfig.MaxPositionUSDC,
		EntryPrice:      decision.EntryPrice,
	})
	return quantity * decision.EntryPrice, nil
}

func (r *realStrategyRunner) effectivePolymarketExecutionStrategy(strategy domain.Strategy) domain.Strategy {
	effective := strategy
	if strategy.IsPaper || r == nil || !r.cfg.Features.EnableLiveTrading {
		effective.IsPaper = true
		return effective
	}
	gate, err := r.liveGateForStrategy(strategy)
	if err != nil {
		effective.IsPaper = true
		return effective
	}
	if strategy.ExecutionStrategyVersionID == nil {
		effective.IsPaper = true
		return effective
	}
	if allowed, _ := gate.Allows(strategy.ExecutionStrategyVersionID, "polymarket"); !allowed {
		effective.IsPaper = true
	}
	return effective
}

func (r *realStrategyRunner) executionDecisionMetadata(ctx context.Context, ref domain.PipelineRunRef) *execution.DecisionMetadata {
	return executionDecisionMetadata(ctx, r.decisionRepo, r.logger, ref)
}

func executionDecisionMetadata(ctx context.Context, decisionRepo repository.AgentDecisionRepository, logger *slog.Logger, ref domain.PipelineRunRef) *execution.DecisionMetadata {
	if decisionRepo == nil || ref.ID == uuid.Nil || ref.TradeDate.IsZero() {
		return nil
	}

	decisions, err := decisionRepo.GetByRun(ctx, ref, repository.AgentDecisionFilter{
		AgentRole: domain.AgentRoleTrader,
		Phase:     domain.PhaseTrading,
	}, 1, 0)
	if err != nil || len(decisions) == 0 {
		if err != nil && logger != nil {
			logger.WarnContext(ctx, "load trader decision metadata", "error", err, "run_id", ref.ID)
		}
		return nil
	}

	decision := decisions[0]
	hasLLMProvenance := strings.TrimSpace(decision.PromptText) != "" ||
		strings.TrimSpace(decision.LLMProvider) != "" ||
		strings.TrimSpace(decision.LLMModel) != "" ||
		decision.PromptTokens > 0 || decision.CompletionTokens > 0 || decision.LatencyMS > 0
	if !hasLLMProvenance {
		return nil
	}
	metadata := &execution.DecisionMetadata{
		PromptText:  decision.PromptText,
		LLMProvider: decision.LLMProvider,
		LLMModel:    decision.LLMModel,
	}
	if decision.PromptTokens > 0 {
		value := decision.PromptTokens
		metadata.PromptTokens = &value
	}
	if decision.CompletionTokens > 0 {
		value := decision.CompletionTokens
		metadata.CompletionTokens = &value
	}
	if decision.LatencyMS > 0 {
		value := decision.LatencyMS
		metadata.LatencyMS = &value
	}
	value := decision.CostUSD
	metadata.CostUSD = &value

	return metadata
}

func (r *realStrategyRunner) registerPolymarketPositions(positions []domain.Position) error {
	if r == nil || r.polymarketStopGuard == nil {
		return nil
	}
	var firstErr error
	for _, position := range positions {
		if err := r.registerPolymarketPosition(position); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *realStrategyRunner) registerPolymarketPosition(position domain.Position) error {
	if r == nil || r.polymarketStopGuard == nil {
		return nil
	}
	if position.ClosedAt != nil || position.Quantity <= 0 {
		return nil
	}
	if position.StopLoss == nil && position.TakeProfit == nil {
		return nil
	}
	if err := r.polymarketStopGuard.RegisterPosition(position); err != nil {
		return err
	}
	slug, err := polymarketPositionSlugFromTicker(position.Ticker)
	if err != nil {
		return err
	}
	r.ensurePolymarketTickWorker(slug)
	return nil
}

func (r *realStrategyRunner) ensurePolymarketTickWorker(slug string) {
	if r == nil || r.polymarketStopGuard == nil || r.polymarketFeed == nil || r.polymarketWorkerCtx == nil {
		return
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return
	}
	if _, loaded := r.polymarketWorkers.LoadOrStore(slug, struct{}{}); loaded {
		return
	}
	r.polymarketWorkerWG.Add(1)
	go func() {
		defer r.polymarketWorkerWG.Done()
		ticks := r.polymarketFeed.Ticks(slug)
		for {
			select {
			case <-r.polymarketWorkerCtx.Done():
				return
			case tick, ok := <-ticks:
				if !ok {
					return
				}
				r.polymarketStopGuard.OnTick(r.polymarketWorkerCtx, tick)
			}
		}
	}()
}

func (r *realStrategyRunner) stopPolymarketTickWorkers() {
	if r == nil || r.polymarketWorkerStop == nil {
		return
	}
	r.polymarketWorkerStop()
	r.polymarketWorkerWG.Wait()
}

func polymarketPositionSlugFromTicker(ticker string) (string, error) {
	ticker = strings.TrimSpace(ticker)
	if ticker == "" {
		return "", errors.New("polymarket: ticker is required")
	}
	slug, _, found := strings.Cut(ticker, ":")
	if !found || strings.TrimSpace(slug) == "" {
		return "", fmt.Errorf("polymarket: ticker %q is not a polymarket position ticker", ticker)
	}
	return strings.TrimSpace(slug), nil
}

func normalizePolymarketStrategySide(side string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "YES":
		return "YES", nil
	case "NO":
		return "NO", nil
	case "UP":
		return "Up", nil
	case "DOWN":
		return "Down", nil
	case "OVER":
		return "Over", nil
	case "UNDER":
		return "Under", nil
	case "":
		return "", fmt.Errorf("trader did not specify Side (YES/NO/Up/Down/Over/Under)")
	default:
		return "", fmt.Errorf("invalid Side %q (want YES, NO, Up, Down, Over, or Under)", side)
	}
}

func (r *realStrategyRunner) recordStrategyPreparationFailure(ctx context.Context, strategy domain.Strategy, executionVersionID uuid.UUID, preparationErr error) error {
	if r.eventRepo == nil {
		return errors.New("record strategy preparation failure: agent event repository is required")
	}

	metadata, err := json.Marshal(map[string]string{
		"reason_code": strategyPreparationFailureReason(preparationErr),
	})
	if err != nil {
		return fmt.Errorf("record strategy preparation failure: marshal metadata: %w", err)
	}

	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	event := &domain.AgentEvent{
		AccountID:   r.executionAccount.AccountID(),
		Environment: r.executionAccount.Environment(),
		OriginType:  "strategy_version",
		OriginID:    executionVersionID.String(),
		StrategyID:  &strategy.ID,
		EventKind:   "strategy.preparation_rejected",
		Title:       "Strategy preparation rejected",
		Summary:     "Required strategy inputs or runtime preparation did not pass preflight.",
		Tags:        []string{"strategy", "preflight", "rejected"},
		Metadata:    metadata,
	}
	if err := r.eventRepo.Create(persistCtx, event); err != nil {
		return fmt.Errorf("record strategy preparation failure: persist event: %w", err)
	}
	return nil
}

func strategyPreparationFailureReason(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "direct news coverage below threshold"):
		return "news_coverage_insufficient"
	case strings.Contains(message, "newest direct news article is older"):
		return "news_stale"
	case strings.Contains(message, "fundamentals completeness below threshold"):
		return "fundamentals_incomplete"
	case strings.Contains(message, "fundamentals unavailable"),
		strings.Contains(message, "fundamentals snapshot is older"),
		strings.Contains(message, "fundamentals ticker"):
		return "fundamentals_invalid"
	case strings.Contains(message, "daily market data stale"),
		strings.Contains(message, "latest daily bar has a non-positive close"):
		return "market_data_stale"
	case strings.Contains(message, "market bars unavailable"):
		return "market_data_unavailable"
	case strings.Contains(message, "social sentiment"):
		return "social_data_invalid"
	case strings.Contains(message, "build llm provider"):
		return "llm_provider_unavailable"
	default:
		return "preparation_failed"
	}
}

func (r *realStrategyRunner) prepareStrategyRun(ctx context.Context, strategy domain.Strategy, executionVersionID uuid.UUID) (*agent.Runner, agent.PreparedRun, *agent.StrategyConfig, chan agent.PipelineEvent, error) {
	strategyConfig, err := parseStrategyConfig(strategy.Config)
	if err != nil {
		return nil, agent.PreparedRun{}, nil, nil, err
	}

	globals := r.globals
	if r.promptOverrides != nil {
		globals.PromptOverrides = mergePromptOverrides(globals.PromptOverrides, r.promptOverrides.Overrides())
	}
	resolved := agent.ResolveConfig(strategyConfig, globals)
	provider, err := runtimeLLMComposer.BuildProviderForSelection(r.cfg.LLM, resolved.LLMConfig.Provider, resolved.LLMConfig.QuickThinkModel, r.logger)
	if err != nil {
		return nil, agent.PreparedRun{}, nil, nil, fmt.Errorf("build llm provider for strategy %s: %w", strategy.Name, err)
	}
	provider = runtimeLLMComposer.WrapProviderChain(provider, r.cfg.LLM, r.metrics, r.logger, r.llmBudget)

	definition, err := buildRunnerDefinition(provider, resolved.LLMConfig.Provider, resolved, r.cfg.LLM.Timeout, r.metrics, r.logger)
	if err != nil {
		return nil, agent.PreparedRun{}, nil, nil, err
	}

	var eventsCh chan agent.PipelineEvent
	if r.hub != nil {
		eventsCh = make(chan agent.PipelineEvent, 64)
	}
	lineage, err := optionalActivePromotionLineage(strategy.Config, r.executionAccount.AccountID())
	if err != nil {
		return nil, agent.PreparedRun{}, nil, nil, err
	}
	persister := &strategyVersionPersister{delegate: agent.NewRepoPersister(r.runRepo, r.snapshotRepo, r.decisionRepo, r.eventRepo, r.logger), executionAccount: r.executionAccount, versionID: executionVersionID, promotionLineage: lineage}
	runner := agent.NewRunner(definition, agent.Dependencies{
		Persister:   persister,
		Events:      eventsCh,
		Logger:      r.logger,
		RunRegistry: r.runRegistry,
	})

	prepared, err := runner.Prepare(strategy, r.globals)
	if err != nil {
		return nil, agent.PreparedRun{}, nil, nil, err
	}
	if err := r.configurePreparedStockCapture(&prepared, strategy); err != nil {
		return nil, agent.PreparedRun{}, nil, nil, err
	}

	prepared.InitialState, err = r.loadInitialState(ctx, strategy, resolved)
	if err != nil {
		return nil, agent.PreparedRun{}, nil, nil, err
	}

	r.logger.Debug("prepareStrategyRun returning successfully")
	return runner, prepared, strategyConfig, eventsCh, nil
}

type strategyVersionPersister struct {
	delegate         agent.DecisionPersister
	executionAccount domain.ExecutionAccountBinding
	versionID        uuid.UUID
	promotionLineage *domain.PromotionExecutionLineage
	mu               sync.RWMutex
	scopes           map[pipelineRunScopeKey]persistedRunScope
}

type pipelineRunScopeKey struct {
	id        uuid.UUID
	tradeDate string
}

type persistedRunScope struct {
	scope       agent.PersistenceScope
	finalizedAt time.Time
}

func (p *strategyVersionPersister) RecordRunStart(ctx context.Context, run *domain.PipelineRun) error {
	if p.versionID != uuid.Nil {
		if err := bindStrategyRunScope(run, p.executionAccount, p.versionID); err != nil {
			return err
		}
		if p.promotionLineage != nil {
			bindPromotionRunLineage(run, p.versionID, *p.promotionLineage)
		}
	}
	scope, err := persistenceScopeFromRun(run)
	if err != nil {
		return err
	}
	key := scopeKey(scope.Run)
	p.mu.Lock()
	if p.scopes == nil {
		p.scopes = make(map[pipelineRunScopeKey]persistedRunScope)
	}
	cutoff := time.Now().Add(-time.Minute)
	for stale, stored := range p.scopes {
		if !stored.finalizedAt.IsZero() && stored.finalizedAt.Before(cutoff) {
			delete(p.scopes, stale)
		}
	}
	p.scopes[key] = persistedRunScope{scope: scope}
	p.mu.Unlock()
	if err := p.delegate.RecordRunStart(ctx, run); err != nil {
		p.mu.Lock()
		delete(p.scopes, key)
		p.mu.Unlock()
		return err
	}
	return nil
}

func optionalActivePromotionLineage(config json.RawMessage, accountID uuid.UUID) (*domain.PromotionExecutionLineage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(config, &envelope); err != nil {
		return nil, fmt.Errorf("parse strategy config for promotion lineage: %w", err)
	}
	if _, exists := envelope["research_lifecycle"]; !exists {
		return nil, nil
	}
	lineage, err := domain.ParseActivePromotionExecutionLineage(config, accountID)
	if err != nil {
		return nil, err
	}
	return &lineage, nil
}

func bindPromotionRunLineage(run *domain.PipelineRun, versionID uuid.UUID, lineage domain.PromotionExecutionLineage) {
	run.ExecutionVersionID = versionID
	run.EvaluationScopeID = lineage.EvaluationScopeID
	run.ManifestID = lineage.ManifestID
	run.QualityResultID = lineage.QualityResultID
	run.DeploymentID = lineage.DeploymentID
	run.PromotionDecisionID = lineage.PromotionDecisionID
	run.CapitalBindingID = lineage.CapitalBindingID
	run.RiskPolicyVersion = lineage.RiskPolicyVersion
}

func (p *strategyVersionPersister) FinalizeRun(ctx context.Context, runID uuid.UUID, tradeDate time.Time, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	scope, err := p.scope(domain.PipelineRunRef{ID: runID, TradeDate: tradeDate})
	if err != nil {
		return repository.PipelineRunFinalizationReceipt{}, err
	}
	if finalization.Event != nil {
		if err := applyEventPersistenceScope(finalization.Event, scope); err != nil {
			return repository.PipelineRunFinalizationReceipt{}, err
		}
	}
	receipt, err := p.delegate.FinalizeRun(ctx, runID, tradeDate, finalization)
	if err == nil {
		p.mu.Lock()
		stored := p.scopes[scopeKey(scope.Run)]
		stored.finalizedAt = time.Now()
		p.scopes[scopeKey(scope.Run)] = stored
		p.mu.Unlock()
	}
	return receipt, err
}

func (p *strategyVersionPersister) SupportsSnapshots() bool { return p.delegate.SupportsSnapshots() }

func (p *strategyVersionPersister) PersistSnapshot(ctx context.Context, snapshot *domain.PipelineRunSnapshot) error {
	scope, err := p.scope(domain.PipelineRunRef{ID: snapshot.PipelineRunID, TradeDate: snapshot.PipelineRunTradeDate})
	if err != nil {
		return err
	}
	if err := rejectPersistenceScopeConflict(snapshot.AccountID, snapshot.Environment, snapshot.OriginType, snapshot.OriginID, scope); err != nil {
		return fmt.Errorf("strategy version snapshot: %w", err)
	}
	snapshot.AccountID = scope.AccountID
	snapshot.Environment = scope.Environment
	snapshot.OriginType = scope.OriginType
	snapshot.OriginID = scope.OriginID
	snapshot.PipelineRunID = scope.Run.ID
	snapshot.PipelineRunTradeDate = scope.Run.TradeDate
	return p.delegate.PersistSnapshot(ctx, snapshot)
}

func (p *strategyVersionPersister) PersistDecision(ctx context.Context, ref domain.PipelineRunRef, node agent.Node, roundNumber *int, output string, response *agent.DecisionLLMResponse) error {
	scope, err := p.scope(ref)
	if err != nil {
		return err
	}
	delegate, ok := p.delegate.(agent.ScopedDecisionPersister)
	if !ok {
		return errors.New("strategy version persister requires scoped decision persistence")
	}
	return delegate.PersistDecisionScoped(ctx, scope, node, roundNumber, output, response)
}

func (p *strategyVersionPersister) PersistEvent(ctx context.Context, event *domain.AgentEvent) error {
	if event.PipelineRunID == nil || event.PipelineRunTradeDate == nil {
		return errors.New("strategy version event requires complete pipeline run identity")
	}
	scope, err := p.scope(domain.PipelineRunRef{ID: *event.PipelineRunID, TradeDate: *event.PipelineRunTradeDate})
	if err != nil {
		return err
	}
	if err := applyEventPersistenceScope(event, scope); err != nil {
		return err
	}
	return p.delegate.PersistEvent(ctx, event)
}

func persistenceScopeFromRun(run *domain.PipelineRun) (agent.PersistenceScope, error) {
	if run == nil || run.AccountID == uuid.Nil || run.Environment == "" || run.OriginType != "strategy_version" || strings.TrimSpace(run.OriginID) == "" {
		return agent.PersistenceScope{}, errors.New("pipeline run has incomplete strategy ownership")
	}
	versionID, err := uuid.Parse(run.OriginID)
	if err != nil || versionID == uuid.Nil {
		return agent.PersistenceScope{}, errors.New("pipeline run has invalid strategy version origin")
	}
	executionScope, err := execution.NewStrategyExecutionScope(run.AccountID, run.Environment, versionID, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})
	if err != nil {
		return agent.PersistenceScope{}, err
	}
	originType, originID := executionScope.Origin()
	return agent.PersistenceScope{AccountID: executionScope.AccountID(), Environment: executionScope.Environment(), OriginType: string(originType), OriginID: originID, Run: domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}}, nil
}

func scopeKey(ref domain.PipelineRunRef) pipelineRunScopeKey {
	return pipelineRunScopeKey{id: ref.ID, tradeDate: ref.TradeDate.Format("2006-01-02")}
}

func (p *strategyVersionPersister) scope(ref domain.PipelineRunRef) (agent.PersistenceScope, error) {
	p.mu.RLock()
	stored, ok := p.scopes[scopeKey(ref)]
	p.mu.RUnlock()
	scope := stored.scope
	if !ok || scope.Run.ID != ref.ID || !scope.Run.TradeDate.Equal(ref.TradeDate) {
		return agent.PersistenceScope{}, errors.New("untrusted or incomplete pipeline run scope")
	}
	return scope, nil
}

func applyEventPersistenceScope(event *domain.AgentEvent, scope agent.PersistenceScope) error {
	if err := rejectPersistenceScopeConflict(event.AccountID, event.Environment, event.OriginType, event.OriginID, scope); err != nil {
		return fmt.Errorf("strategy version event: %w", err)
	}
	if event.PipelineRunID != nil && *event.PipelineRunID != scope.Run.ID {
		return errors.New("strategy version event has conflicting pipeline run ID")
	}
	if event.PipelineRunTradeDate != nil && !event.PipelineRunTradeDate.Equal(scope.Run.TradeDate) {
		return errors.New("strategy version event has conflicting pipeline run trade date")
	}
	event.AccountID = scope.AccountID
	event.Environment = scope.Environment
	event.OriginType = scope.OriginType
	event.OriginID = scope.OriginID
	runID := scope.Run.ID
	tradeDate := scope.Run.TradeDate
	event.PipelineRunID = &runID
	event.PipelineRunTradeDate = &tradeDate
	return nil
}

func rejectPersistenceScopeConflict(accountID uuid.UUID, environment domain.AccountEnvironment, originType, originID string, scope agent.PersistenceScope) error {
	if accountID != uuid.Nil && accountID != scope.AccountID {
		return errors.New("conflicting account ownership")
	}
	if environment != "" && environment != scope.Environment {
		return errors.New("conflicting environment ownership")
	}
	if originType != "" && originType != scope.OriginType {
		return errors.New("conflicting origin type ownership")
	}
	if originID != "" && originID != scope.OriginID {
		return errors.New("conflicting origin ID ownership")
	}
	return nil
}

func bindStrategyRunScope(run *domain.PipelineRun, executionAccount domain.ExecutionAccountBinding, versionID uuid.UUID) error {
	if err := executionAccount.Validate(); err != nil {
		return err
	}
	scope, err := execution.NewStrategyExecutionScope(executionAccount.AccountID(), executionAccount.Environment(), versionID, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})
	if err != nil {
		return err
	}
	originType, originID := scope.Origin()
	run.AccountID = scope.AccountID()
	run.Environment = scope.Environment()
	run.OriginType = string(originType)
	run.OriginID = originID
	return nil
}

func executionScopeFromPersistedRun(run *domain.PipelineRun, strategy domain.Strategy) (execution.ExecutionScope, error) {
	if run == nil {
		return execution.ExecutionScope{}, errors.New("persisted run is required")
	}
	versionID, err := uuid.Parse(run.OriginID)
	if err != nil || versionID == uuid.Nil || run.StrategyID != strategy.ID || run.OriginType != string(ledger.ExecutionOriginStrategyVersion) {
		return execution.ExecutionScope{}, errors.New("persisted run ownership does not match strategy")
	}
	if strategy.ExecutionStrategyVersionID != nil && *strategy.ExecutionStrategyVersionID != versionID {
		return execution.ExecutionScope{}, errors.New("persisted run version does not match strategy binding")
	}
	if strategy.ID == uuid.Nil {
		return execution.NewStrategyExecutionScope(run.AccountID, run.Environment, versionID, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate})
	}
	return execution.NewStrategyExecutionScope(run.AccountID, run.Environment, versionID, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, strategy.ID)
}

func pipelineSnapshotFromScope(scope execution.ExecutionScope, dataType string, payload json.RawMessage) (*domain.PipelineRunSnapshot, error) {
	run, ok := scope.PipelineRun()
	if !ok {
		return nil, errors.New("pipeline snapshot requires run-scoped execution ownership")
	}
	originType, originID := scope.Origin()
	return &domain.PipelineRunSnapshot{
		ID: uuid.New(), AccountID: scope.AccountID(), Environment: scope.Environment(),
		OriginType: string(originType), OriginID: originID, PipelineRunID: run.ID,
		PipelineRunTradeDate: run.TradeDate, DataType: dataType, Payload: payload, CreatedAt: time.Now().UTC(),
	}, nil
}

func (r *realStrategyRunner) loadInitialState(ctx context.Context, strategy domain.Strategy, resolved agent.ResolvedConfig) (agent.InitialStateSeed, error) {
	if r.dataService == nil {
		return agent.InitialStateSeed{}, errors.New("market data service is required")
	}

	to := time.Now().UTC()

	seed := agent.InitialStateSeed{}
	if usesStockOHLCVAnalysis(strategy) {
		from := to.Add(-strategyMarketLookback)
		bars, err := r.dataService.GetOHLCV(ctx, strategy.MarketType, strategy.Ticker, data.Timeframe1d, from, to)
		if err != nil {
			return agent.InitialStateSeed{}, fmt.Errorf("load ohlcv for %s: %w", strategy.Ticker, err)
		}
		if len(bars) == 0 {
			return agent.InitialStateSeed{}, fmt.Errorf("load ohlcv for %s: no bars returned", strategy.Ticker)
		}
		r.logger.Debug("loadInitialState after OHLCV", slog.Int("bars", len(bars)))
		seed.Market = &agent.MarketData{
			Bars:       bars,
			Indicators: data.IndicatorSnapshotFromBars(bars),
		}
	}

	if fundamentals, err := r.dataService.GetFundamentals(ctx, strategy.MarketType, strategy.Ticker); err == nil {
		seed.Fundamentals = &fundamentals
	} else if ctxErr := contextErr(err); ctxErr != nil {
		return agent.InitialStateSeed{}, ctxErr
	} else {
		r.logger.Warn("prod strategy runner: fundamentals unavailable",
			slog.String("ticker", strategy.Ticker),
			slog.Any("error", err),
		)
	}

	r.logger.Debug("loadInitialState after fundamentals")
	newsFrom := to.Add(-strategyNewsLookback)
	if articles, err := r.dataService.GetNews(ctx, strategy.MarketType, strategy.Ticker, newsFrom, to); err == nil {
		seed.News = data.RankRelevantNews(strategy.Ticker, articles, 10)
	} else if ctxErr := contextErr(err); ctxErr != nil {
		return agent.InitialStateSeed{}, ctxErr
	} else {
		r.logger.Warn("prod strategy runner: news unavailable",
			slog.String("ticker", strategy.Ticker),
			slog.Any("error", err),
		)
	}

	r.logger.Debug("loadInitialState after news")
	socialFrom := to.Add(-strategySocialLookback)
	if snapshots, err := r.dataService.GetSocialSentiment(ctx, strategy.MarketType, strategy.Ticker, socialFrom, to); err == nil {
		seed.Social = latestSocialSnapshot(snapshots)
		if seed.Social == nil {
			r.logger.Info("prod strategy runner: social sentiment empty for ticker",
				slog.String("ticker", strategy.Ticker),
			)
		}
	} else if ctxErr := contextErr(err); ctxErr != nil {
		return agent.InitialStateSeed{}, ctxErr
	} else {
		r.logger.Warn("prod strategy runner: social sentiment unavailable",
			slog.String("ticker", strategy.Ticker),
			slog.Any("error", err),
		)
	}

	r.logger.Debug("loadInitialState after social sentiment")
	if err := validateRequiredAnalysisInputs(strategy, resolved.RequiredAnalystRoles, seed, to); err != nil {
		return agent.InitialStateSeed{}, err
	}
	// Polymarket: load prediction market metadata for the market slug.
	if strategy.MarketType.Normalize() == domain.MarketTypePolymarket && r.polymarketClient != nil {
		pm, err := r.polymarketClient.GetMarketData(ctx, strategy.Ticker)
		if err != nil {
			r.logger.Warn("prod strategy runner: polymarket market data unavailable",
				slog.String("slug", strategy.Ticker),
				slog.Any("error", err),
			)
		} else {
			seed.PredictionMarket = pm
		}
	}

	r.logger.Debug("loadInitialState returning seed")
	return seed, nil
}

func validateRequiredAnalysisInputs(strategy domain.Strategy, required []agent.AgentRole, seed agent.InitialStateSeed, now time.Time) error {
	for _, role := range required {
		switch role {
		case agent.AgentRoleMarketAnalyst:
			if seed.Market == nil || len(seed.Market.Bars) == 0 {
				return fmt.Errorf("required analyst role %s: market bars unavailable", role)
			}
			if err := validateDailyBarFreshness(strategy.MarketType, now, seed.Market.Bars); err != nil {
				return fmt.Errorf("required analyst role %s: %w", role, err)
			}
		case agent.AgentRoleFundamentalsAnalyst:
			if err := validateFundamentalsInput(strategy.Ticker, seed.Fundamentals, now); err != nil {
				return fmt.Errorf("required analyst role %s: %w", role, err)
			}
		case agent.AgentRoleNewsAnalyst:
			if err := validateNewsInput(seed.News, now); err != nil {
				return fmt.Errorf("required analyst role %s: %w", role, err)
			}
		case agent.AgentRoleSocialMediaAnalyst:
			if seed.Social == nil || seed.Social.PostCount+seed.Social.CommentCount == 0 {
				return fmt.Errorf("required analyst role %s: social sentiment unavailable or empty", role)
			}
			if seed.Social.MeasuredAt.IsZero() || now.Sub(seed.Social.MeasuredAt) > 24*time.Hour {
				return fmt.Errorf("required analyst role %s: social sentiment is older than 24h", role)
			}
		}
	}
	return nil
}

func validateDailyBarFreshness(marketType domain.MarketType, now time.Time, bars []domain.OHLCV) error {
	if marketType.Normalize() != domain.MarketTypeStock || len(bars) == 0 {
		return nil
	}
	latest := bars[0]
	for _, bar := range bars[1:] {
		if bar.Timestamp.After(latest.Timestamp) {
			latest = bar
		}
	}
	if latest.Close <= 0 {
		return errors.New("latest daily bar has a non-positive close")
	}
	et, err := time.LoadLocation("America/New_York")
	if err != nil {
		return fmt.Errorf("load exchange timezone: %w", err)
	}
	localNow := now.In(et)
	refreshDeadline := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 16, 0, 0, 0, et).Add(postCloseDataGrace)
	if !scheduler.IsNYSETradingDay(now) || localNow.Before(refreshDeadline) {
		return nil
	}
	localBar := latest.Timestamp.In(et)
	if localBar.Year() != localNow.Year() || localBar.YearDay() != localNow.YearDay() {
		return fmt.Errorf("daily market data stale after 4:30 PM ET: latest bar %s", localBar.Format("2006-01-02"))
	}
	return nil
}

func validateFundamentalsInput(ticker string, fundamentals *data.Fundamentals, now time.Time) error {
	if fundamentals == nil {
		return errors.New("fundamentals unavailable")
	}
	if !strings.EqualFold(strings.TrimSpace(fundamentals.Ticker), strings.TrimSpace(ticker)) {
		return fmt.Errorf("fundamentals ticker %q does not match strategy ticker %q", fundamentals.Ticker, ticker)
	}
	if fundamentals.FetchedAt.IsZero() || now.Sub(fundamentals.FetchedAt) > 24*time.Hour {
		return errors.New("fundamentals snapshot is older than 24h")
	}
	coreMetrics := []struct {
		name  string
		value float64
	}{
		{name: data.FundamentalFieldMarketCap, value: fundamentals.MarketCap},
		{name: data.FundamentalFieldPERatio, value: fundamentals.PERatio},
		{name: data.FundamentalFieldRevenueGrowthYoY, value: fundamentals.RevenueGrowthYoY},
		{name: data.FundamentalFieldGrossMargin, value: fundamentals.GrossMargin},
		{name: data.FundamentalFieldDebtToEquity, value: fundamentals.DebtToEquity},
	}
	available := 0
	for _, metric := range coreMetrics {
		if math.IsNaN(metric.value) || math.IsInf(metric.value, 0) {
			continue
		}
		// Current providers explicitly identify missing fields, which lets valid
		// zero and negative observations count. Preserve compatibility with older
		// cached snapshots by treating zero as missing only when no field metadata
		// exists at all.
		if len(fundamentals.MissingFields) > 0 {
			if !data.IsFundamentalFieldMissing(*fundamentals, metric.name) {
				available++
			}
		} else if metric.value != 0 {
			available++
		}
	}
	if available < 3 {
		return fmt.Errorf("fundamentals completeness below threshold: %d of 5 core metrics available", available)
	}
	return nil
}

func validateNewsInput(articles []data.NewsArticle, now time.Time) error {
	direct := 0
	newest := time.Time{}
	for _, article := range articles {
		if article.Relevance < 0.85 {
			continue
		}
		direct++
		if article.PublishedAt.After(newest) {
			newest = article.PublishedAt
		}
	}
	if direct < requiredNewsMinDirect {
		return fmt.Errorf("direct news coverage below threshold: %d articles, require %d", direct, requiredNewsMinDirect)
	}
	if newest.IsZero() || now.Sub(newest) > requiredNewsMaxAge {
		return fmt.Errorf("newest direct news article is older than %s", requiredNewsMaxAge)
	}
	return nil
}

func usesStockOHLCVAnalysis(strategy domain.Strategy) bool {
	return !eventmarkets.IsEventMarket(strategy.MarketType) && strategy.MarketType.Normalize() != domain.MarketTypeOptions
}

func buildRunnerDefinition(provider llm.Provider, providerName string, resolved agent.ResolvedConfig, llmTimeout time.Duration, appMetrics *metrics.Metrics, logger *slog.Logger) (agent.Definition, error) {
	analysisAgents, err := buildAnalysisAgents(provider, providerName, resolved, appMetrics, logger)
	if err != nil {
		return agent.Definition{}, err
	}

	deepModel := strings.TrimSpace(resolved.LLMConfig.DeepThinkModel)
	quickModel := strings.TrimSpace(resolved.LLMConfig.QuickThinkModel)
	debateProvider := newDebateTimeoutFallbackProvider(provider, quickModel, effectiveDebateCallTimeout(llmTimeout, resolved), logger)

	return agent.Definition{
		Analysis: analysisAgents,
		Research: agent.ResearchDebateStage{
			Debaters: []agent.DebateAgent{
				agentdebate.NewBullResearcherWithPrompt(newLLMMetricsProvider(debateProvider, providerName, agent.AgentRoleBullResearcher.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleBullResearcher, agentdebate.BullResearcherSystemPrompt), logger),
				agentdebate.NewBearResearcherWithPrompt(newLLMMetricsProvider(debateProvider, providerName, agent.AgentRoleBearResearcher.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleBearResearcher, agentdebate.BearResearcherSystemPrompt), logger),
			},
			Judge: agentdebate.NewResearchManagerWithPrompt(newLLMMetricsProvider(debateProvider, providerName, agent.AgentRoleInvestJudge.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleInvestJudge, agentdebate.ResearchManagerSystemPrompt), logger),
		},
		Trader: agenttrader.NewTraderWithPrompt(newLLMMetricsProvider(provider, providerName, agent.AgentRoleTrader.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleTrader, agenttrader.TraderSystemPrompt), logger),
		Risk: agent.RiskDebateStage{
			Debaters: []agent.DebateAgent{
				agentrisk.NewAggressiveRiskWithPrompt(newLLMMetricsProvider(debateProvider, providerName, agent.AgentRoleAggressiveAnalyst.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleAggressiveAnalyst, agentrisk.AggressiveRiskSystemPrompt), logger),
				agentrisk.NewConservativeRiskWithPrompt(newLLMMetricsProvider(debateProvider, providerName, agent.AgentRoleConservativeAnalyst.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleConservativeAnalyst, agentrisk.ConservativeRiskSystemPrompt), logger),
				agentrisk.NewNeutralRiskWithPrompt(newLLMMetricsProvider(debateProvider, providerName, agent.AgentRoleNeutralAnalyst.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleNeutralAnalyst, agentrisk.NeutralRiskSystemPrompt), logger),
			},
			Judge: agentrisk.NewRiskManagerWithPrompt(newLLMMetricsProvider(debateProvider, providerName, agent.AgentRoleRiskManager.String(), appMetrics), providerName, deepModel, promptOverride(resolved.PromptOverrides, agent.AgentRoleRiskManager, agentrisk.RiskManagerSystemPrompt), logger),
		},
	}, nil
}

func effectiveDebateCallTimeout(llmTimeout time.Duration, resolved agent.ResolvedConfig) time.Duration {
	callTimeout := llmTimeout
	if timeout := globalDebateCallTimeout(); timeout > 0 {
		callTimeout = timeout
	}

	roundTimeout := time.Duration(resolved.PipelineConfig.DebateTimeoutSeconds) * time.Second
	if roundTimeout <= 0 {
		return callTimeout
	}

	maximumCallTimeout := roundTimeout / 2
	if maximumCallTimeout <= 0 {
		return callTimeout
	}
	if callTimeout <= 0 || callTimeout > maximumCallTimeout {
		return maximumCallTimeout
	}
	return callTimeout
}

func globalDebateCallTimeout() time.Duration {
	if t := os.Getenv("LLM_DEBATE_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			return d
		}
	}
	return 0
}

func promptOverride(overrides map[agent.AgentRole]string, role agent.AgentRole, fallback string) string {
	prompt := strings.TrimSpace(overrides[role])
	if prompt == "" {
		return fallback
	}
	return prompt
}

func mergePromptOverrides(base, overrides map[agent.AgentRole]string) map[agent.AgentRole]string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}
	merged := make(map[agent.AgentRole]string, len(base)+len(overrides))
	for role, prompt := range base {
		if strings.TrimSpace(prompt) != "" {
			merged[role] = prompt
		}
	}
	for role, prompt := range overrides {
		if strings.TrimSpace(prompt) != "" {
			merged[role] = prompt
		}
	}
	return merged
}

func buildAnalysisAgents(provider llm.Provider, providerName string, resolved agent.ResolvedConfig, appMetrics *metrics.Metrics, logger *slog.Logger) ([]agent.AnalysisAgent, error) {
	roles, err := selectedAnalysisRoles(resolved.AnalystSelection)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(resolved.LLMConfig.QuickThinkModel)
	agents := make([]agent.AnalysisAgent, 0, len(roles))
	for _, role := range roles {
		agentImpl, err := newAnalysisAgent(provider, providerName, model, role, resolved.PromptOverrides[role], appMetrics, logger)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agentImpl)
	}

	return agents, nil
}

func selectedAnalysisRoles(selection []agent.AgentRole) ([]agent.AgentRole, error) {
	if selection == nil {
		roles := make([]agent.AgentRole, len(defaultAnalysisRoles))
		copy(roles, defaultAnalysisRoles)
		return roles, nil
	}

	requested := make(map[agent.AgentRole]struct{}, len(selection))
	for _, role := range selection {
		if !isAnalysisRole(role) {
			return nil, fmt.Errorf("analyst_selection includes non-analysis role %q", role)
		}
		requested[role] = struct{}{}
	}

	roles := make([]agent.AgentRole, 0, len(requested))
	for _, role := range defaultAnalysisRoles {
		if _, ok := requested[role]; ok {
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		return nil, errors.New("analyst_selection must enable at least one analysis role")
	}

	return roles, nil
}

func isAnalysisRole(role agent.AgentRole) bool {
	switch role {
	case agent.AgentRoleMarketAnalyst,
		agent.AgentRoleFundamentalsAnalyst,
		agent.AgentRoleNewsAnalyst,
		agent.AgentRoleSocialMediaAnalyst:
		return true
	default:
		return false
	}
}

func newAnalysisAgent(provider llm.Provider, providerName, model string, role agent.AgentRole, promptOverride string, appMetrics *metrics.Metrics, logger *slog.Logger) (agent.AnalysisAgent, error) {
	if logger == nil {
		logger = slog.Default()
	}

	provider = newLLMMetricsProvider(provider, providerName, role.String(), appMetrics)

	prompt := strings.TrimSpace(promptOverride)
	switch role {
	case agent.AgentRoleMarketAnalyst:
		if prompt == "" {
			prompt = agentanalysts.MarketAnalystSystemPrompt
		}
		base := agentanalysts.NewBaseAnalyst(agentanalysts.BaseAnalystConfig{
			Provider:     provider,
			ProviderName: providerName,
			Model:        model,
			Logger:       logger,
			Role:         role,
			Name:         "market_analyst",
			SystemPrompt: prompt,
			BuildPrompt: func(input agent.AnalysisInput) (string, bool) {
				var bars []domain.OHLCV
				var indicators []domain.Indicator
				if input.Market != nil {
					bars = input.Market.Bars
					indicators = input.Market.Indicators
				}
				return agentanalysts.FormatMarketAnalystUserPrompt(input.Ticker, bars, indicators), true
			},
		})
		return &agentanalysts.MarketAnalyst{BaseAnalyst: base}, nil
	case agent.AgentRoleFundamentalsAnalyst:
		if prompt == "" {
			prompt = agentanalysts.FundamentalsAnalystSystemPrompt
		}
		base := agentanalysts.NewBaseAnalyst(agentanalysts.BaseAnalystConfig{
			Provider:     provider,
			ProviderName: providerName,
			Model:        model,
			Logger:       logger,
			Role:         role,
			Name:         "fundamentals_analyst",
			SystemPrompt: prompt,
			SkipMessage:  "No fundamentals available for this asset type.",
			BuildPrompt: func(input agent.AnalysisInput) (string, bool) {
				if input.Fundamentals == nil {
					return "", false
				}
				return agentanalysts.FormatFundamentalsAnalystUserPrompt(input.Ticker, input.Fundamentals), true
			},
		})
		return &agentanalysts.FundamentalsAnalyst{BaseAnalyst: base}, nil
	case agent.AgentRoleNewsAnalyst:
		if prompt == "" {
			prompt = agentanalysts.NewsAnalystSystemPrompt
		}
		base := agentanalysts.NewBaseAnalyst(agentanalysts.BaseAnalystConfig{
			Provider:     provider,
			ProviderName: providerName,
			Model:        model,
			Logger:       logger,
			Role:         role,
			Name:         "news_analyst",
			SystemPrompt: prompt,
			SkipMessage:  "No news articles available. Unable to perform news analysis.",
			BuildPrompt: func(input agent.AnalysisInput) (string, bool) {
				if len(input.News) == 0 {
					return "", false
				}
				return agentanalysts.FormatNewsAnalystUserPrompt(input.Ticker, input.News), true
			},
		})
		return &agentanalysts.NewsAnalyst{BaseAnalyst: base}, nil
	case agent.AgentRoleSocialMediaAnalyst:
		if prompt == "" {
			prompt = agentanalysts.SocialAnalystSystemPrompt
		}
		base := agentanalysts.NewBaseAnalyst(agentanalysts.BaseAnalystConfig{
			Provider:     provider,
			ProviderName: providerName,
			Model:        model,
			Logger:       logger,
			Role:         role,
			Name:         "social_media_analyst",
			SystemPrompt: prompt,
			SkipMessage:  "Social sentiment data unavailable for this ticker. Analysis skipped to conserve resources.",
			BuildPrompt: func(input agent.AnalysisInput) (string, bool) {
				return agentanalysts.FormatSocialAnalystUserPrompt(input.Ticker, input.Social), input.Social != nil
			},
		})
		return &agentanalysts.SocialMediaAnalyst{BaseAnalyst: base}, nil
	default:
		return nil, fmt.Errorf("unsupported analysis role %q", role)
	}
}

func parseStrategyConfig(raw domain.StrategyConfig) (*agent.StrategyConfig, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var cfg agent.StrategyConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse strategy config: %w", err)
	}
	if err := agent.ValidateStrategyConfig(cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func globalSettingsFromConfig(cfg config.Config) agent.GlobalSettings {
	var llmConfig *agent.StrategyLLMConfig
	provider := strings.TrimSpace(cfg.LLM.DefaultProvider)
	deep := strings.TrimSpace(cfg.LLM.DeepThinkModel)
	quick := strings.TrimSpace(cfg.LLM.QuickThinkModel)
	if provider != "" || deep != "" || quick != "" {
		llmConfig = &agent.StrategyLLMConfig{}
		if provider != "" {
			llmConfig.Provider = &provider
		}
		if deep != "" {
			llmConfig.DeepThinkModel = &deep
		}
		if quick != "" {
			llmConfig.QuickThinkModel = &quick
		}
	}

	var riskConfig *agent.StrategyRiskConfig
	if cfg.Risk.MaxPositionSizePct > 0 {
		positionSizePct := cfg.Risk.MaxPositionSizePct * 100
		riskConfig = &agent.StrategyRiskConfig{PositionSizePct: &positionSizePct}
	}

	return agent.GlobalSettings{
		LLMConfig:  llmConfig,
		RiskConfig: riskConfig,
	}
}

func (r *realStrategyRunner) newOrderManager(ctx context.Context, strategy domain.Strategy, resolved agent.ResolvedConfig, strategyConfig *agent.StrategyConfig, scope execution.ExecutionScope) (*execution.OrderManager, error) {
	gate, err := r.liveGateForStrategy(strategy)
	if err != nil {
		return nil, err
	}
	broker, brokerName, err := r.newBrokerForStrategy(strategy)
	if err != nil {
		return nil, err
	}
	r.setRiskPortfolioSnapshotSource(broker)

	return execution.NewOrderManager(
		broker,
		brokerName,
		r.riskEngine,
		r.positionRepo,
		r.orderRepo,
		r.tradeRepo,
		r.auditLogRepo,
		r.eventRepo,
		applyPolymarketSizingCap(strategy.MarketType, sizingConfigForStrategy(ctx, strategy, strategyConfig, resolved, r.positionRepo, r.logger, scope), r.cfg.Risk.Polymarket.MaxPositionUSDC),
		r.logger,
	).WithMetrics(r.metrics).WithDecisionRecorder(r.tradeDecisionRecorder).WithLiveGate(gate).WithLiveTrading(!strategy.IsPaper).WithAcceptedOrderFillWriter(r.economicWriter), nil
}

func (r *realStrategyRunner) recordPortfolioOpportunity(ctx context.Context, strategy domain.Strategy, run *domain.PipelineRun, finalSignal execution.FinalSignal, plan execution.TradingPlan, optionSpread *domain.OptionSpread) error {
	if r == nil {
		return nil
	}
	if finalSignal.Signal != domain.PipelineSignalBuy && finalSignal.Signal != domain.PipelineSignalSell {
		return nil
	}
	if r.opportunityRepo == nil {
		if r.portfolioAllocatorMode == portfolio.AllocatorModePaper {
			return fmt.Errorf("portfolio opportunity: paper allocator requires opportunity repository")
		}
		return nil
	}
	if run == nil || run.ID == uuid.Nil || run.Status != domain.PipelineStatusCompleted || run.Signal != finalSignal.Signal {
		return fmt.Errorf("portfolio opportunity: source run is not durably completed with matching signal")
	}
	maxLossPct := opportunityMaxLossPct(finalSignal.Signal, plan.EntryPrice, plan.StopLoss)
	proposedNotional := plan.PositionSize * plan.EntryPrice
	scope, err := executionScopeFromPersistedRun(run, strategy)
	if err != nil {
		return fmt.Errorf("portfolio opportunity: derive source scope: %w", err)
	}
	opportunity, reason, err := portfolio.BuildOpportunity(portfolio.OpportunityBuildInput{
		Scope:             scope,
		Strategy:          strategy,
		Run:               run,
		Signal:            finalSignal.Signal,
		PredictionSide:    plan.Side,
		Confidence:        firstPositive(finalSignal.Confidence, plan.Confidence),
		EdgePct:           firstPositive(plan.NetEV, positiveEdgeFromRiskReward(plan.RiskReward)),
		ExpectedReturnPct: firstPositive(plan.NetEV, positiveEdgeFromRiskReward(plan.RiskReward)),
		MaxLossPct:        maxLossPct,
		EntryPrice:        plan.EntryPrice,
		LiquidityUSD:      plan.Depth,
		SpreadPct:         plan.Spread,
		ProposedNotional:  proposedNotional,
		Reason:            plan.Rationale,
		Evidence:          opportunityEvidence(plan),
		OptionSpread:      optionSpread,
		QuoteObservedAt: func() *time.Time {
			if optionSpread == nil || optionSpread.QuoteObservedAt.IsZero() {
				return nil
			}
			value := optionSpread.QuoteObservedAt
			return &value
		}(),
	}, portfolio.OpportunityBuilderConfig{})
	if err != nil {
		return fmt.Errorf("portfolio opportunity: build (%s): %w", reason, err)
	}
	if opportunity == nil {
		return nil
	}
	if err := r.opportunityRepo.UpsertQueuedByDedupeKey(ctx, opportunity); err != nil {
		return fmt.Errorf("portfolio opportunity: persist: %w", err)
	}
	return nil
}

func (r *realStrategyRunner) portfolioAllocatorOwnsPaperExecution(strategy domain.Strategy, signal domain.PipelineSignal) bool {
	if r == nil || !strategy.IsPaper || r.portfolioAllocatorMode != portfolio.AllocatorModePaper && r.portfolioAllocatorMode != portfolio.AllocatorModeShadow {
		return false
	}
	// Promoted paper strategies always route through the allocator. In shadow
	// mode that authority records replayable decisions and makes no broker call;
	// in paper mode it owns the one approved submission path.
	if _, err := domain.ParseActivePromotionExecutionLineage(strategy.Config, r.executionAccount.AccountID()); err != nil {
		return false
	}
	if strategy.MarketType.Normalize() == domain.MarketTypeOptions {
		return signal == domain.PipelineSignalBuy
	}
	return signal == domain.PipelineSignalBuy || signal == domain.PipelineSignalSell
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func positiveEdgeFromRiskReward(riskReward float64) float64 {
	if riskReward <= 0 {
		return 0
	}
	return riskReward / 100
}

func opportunityMaxLossPct(signal domain.PipelineSignal, entryPrice, stopLoss float64) float64 {
	if entryPrice <= 0 || stopLoss <= 0 {
		return 0
	}
	switch signal {
	case domain.PipelineSignalSell:
		if stopLoss <= entryPrice {
			return 0
		}
		return (stopLoss - entryPrice) / entryPrice
	default:
		if stopLoss >= entryPrice {
			return 0
		}
		return (entryPrice - stopLoss) / entryPrice
	}
}

func opportunityEvidence(plan execution.TradingPlan) json.RawMessage {
	payload, err := json.Marshal(map[string]any{
		"entry_type":   plan.EntryType,
		"entry_price":  plan.EntryPrice,
		"stop_loss":    plan.StopLoss,
		"take_profit":  plan.TakeProfit,
		"time_horizon": plan.TimeHorizon,
		"risk_reward":  plan.RiskReward,
		"side":         plan.Side,
	})
	if err != nil {
		return json.RawMessage("{}")
	}
	return payload
}

func (r *realStrategyRunner) liveGateForStrategy(strategy domain.Strategy) (execution.LiveGateConfig, error) {
	if r == nil || strategy.IsPaper || !r.cfg.Features.EnableLiveTrading {
		return execution.LiveGateConfig{}, nil
	}
	if strategy.ExecutionStrategyVersionID == nil || *strategy.ExecutionStrategyVersionID == uuid.Nil {
		return execution.LiveGateConfig{}, fmt.Errorf("live trading requires an immutable strategy-version binding")
	}

	allowedStrategies := make(map[uuid.UUID]bool, len(r.cfg.LiveTradingAllowedStrategies))
	for _, raw := range r.cfg.LiveTradingAllowedStrategies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		strategyID, err := uuid.Parse(raw)
		if err != nil {
			return execution.LiveGateConfig{}, fmt.Errorf("parse LIVE_TRADING_ALLOWED_STRATEGIES value %q: %w", raw, err)
		}
		if strategyID == strategy.ID {
			return execution.LiveGateConfig{}, fmt.Errorf("LIVE_TRADING_ALLOWED_STRATEGIES must contain immutable strategy-version UUIDs, not legacy strategy ID %s", strategyID)
		}
		allowedStrategies[strategyID] = true
	}

	allowedBrokers := make(map[string]bool, len(r.cfg.LiveTradingAllowedBrokers))
	for _, raw := range r.cfg.LiveTradingAllowedBrokers {
		broker := strings.ToLower(strings.TrimSpace(raw))
		if broker == "" {
			continue
		}
		allowedBrokers[broker] = true
	}

	return execution.LiveGateConfig{EnableLiveTrading: true, AllowedStrategies: allowedStrategies, AllowedBrokers: allowedBrokers}, nil
}

func validatePositionOrigins(positions []domain.Position, binding domain.ExecutionAccountBinding, versionID uuid.UUID) error {
	for _, position := range positions {
		if position.AccountID != binding.AccountID() || position.Environment != binding.Environment() || position.OriginType != "strategy_version" || position.OriginID != versionID.String() {
			return fmt.Errorf("position %s has mismatched execution origin", position.ID)
		}
	}
	return nil
}

func loadResultPositions(ctx context.Context, repo repository.PositionRepository, binding domain.ExecutionAccountBinding, versionID uuid.UUID) ([]domain.Position, error) {
	scoped, ok := repo.(repository.ExecutionScopedPositionRepository)
	if !ok {
		return nil, fmt.Errorf("canonical execution-scoped position repository is required")
	}
	const pageSize = 100
	positions := make([]domain.Position, 0)
	for offset := 0; ; offset += pageSize {
		page, err := scoped.GetByExecutionScope(ctx, binding.AccountID(), binding.Environment(), string(ledger.ExecutionOriginStrategyVersion), versionID.String(), repository.PositionFilter{}, pageSize, offset)
		if err != nil {
			return nil, err
		}
		positions = append(positions, page...)
		if len(page) < pageSize {
			break
		}
	}
	return positions, validatePositionOrigins(positions, binding, versionID)
}

func loadResultOrders(ctx context.Context, repo repository.OrderRepository, ref domain.PipelineRunRef) ([]domain.Order, error) {
	const pageSize = 100
	orders := make([]domain.Order, 0)
	for offset := 0; ; offset += pageSize {
		page, err := repo.GetByRun(ctx, ref, repository.OrderFilter{}, pageSize, offset)
		if err != nil {
			return nil, err
		}
		orders = append(orders, page...)
		if len(page) < pageSize {
			return orders, nil
		}
	}
}

func validateResultOrders(orders []domain.Order, binding domain.ExecutionAccountBinding, versionID, runID uuid.UUID, tradeDate time.Time) error {
	for _, order := range orders {
		if order.AccountID != binding.AccountID() || order.Environment != binding.Environment() || order.OriginType != string(ledger.ExecutionOriginStrategyVersion) || order.OriginID != versionID.String() || order.PipelineRunID == nil || *order.PipelineRunID != runID || order.PipelineRunTradeDate == nil || !order.PipelineRunTradeDate.Equal(tradeDate) {
			return fmt.Errorf("order %s has mismatched account, environment, origin, or pipeline identity", order.ID)
		}
	}
	return nil
}

func (r *realStrategyRunner) recordPipelineMetrics(run domain.PipelineRun) {
	if r == nil || r.metrics == nil {
		return
	}
	signal := run.Signal.String()
	if signal == "" {
		signal = string(domain.PipelineSignalHold)
	}
	r.metrics.RecordPipelineRun(run.Ticker, signal, run.Status.String())
	if run.CompletedAt != nil {
		r.metrics.ObservePipelineDuration(run.Ticker, run.CompletedAt.Sub(run.StartedAt).Seconds())
	}
}

func (r *realStrategyRunner) recordAgentTerminalMetrics(result *agent.RunResult) {
	if result == nil || !result.TerminalApplied {
		return
	}
	r.recordPipelineMetrics(result.Run)
	r.refreshExecutionMetrics(context.Background())
}

func (r *realStrategyRunner) refreshExecutionMetrics(ctx context.Context) {
	if r == nil || r.metrics == nil {
		return
	}
	if count, err := r.positionRepo.CountOpen(ctx, repository.PositionFilter{}); err == nil {
		r.metrics.SetPositionsOpen(float64(count))
	}
	if status, err := r.riskEngine.GetStatus(ctx); err == nil {
		r.metrics.SetCircuitBreakerState(status.CircuitBreaker.State == risk.CircuitBreakerPhaseTripped)
		r.metrics.SetKillSwitchActive(status.KillSwitch.Active)
	}
}

func (r *realStrategyRunner) newBrokerForStrategy(strategy domain.Strategy) (execution.Broker, string, error) {
	marketType := strategy.MarketType.Normalize()
	if strategy.IsPaper {
		switch marketType {
		case domain.MarketTypeStock:
			if hasBrokerCredentials(r.cfg.Brokers.Alpaca) && r.cfg.Brokers.Alpaca.PaperMode {
				return alpacaexecution.NewBroker(alpacaexecution.NewClient(
					r.cfg.Brokers.Alpaca.APIKey,
					r.cfg.Brokers.Alpaca.APISecret,
					true,
					r.logger,
				)), "alpaca", nil
			}
		case domain.MarketTypeCrypto:
			if hasBrokerCredentials(r.cfg.Brokers.Binance) && r.cfg.Brokers.Binance.PaperMode {
				return binanceexecution.NewBroker(binanceexecution.NewClient(
					r.cfg.Brokers.Binance.APIKey,
					r.cfg.Brokers.Binance.APISecret,
					true,
					r.logger,
				)), "binance", nil
			}
		case domain.MarketTypePolymarket:
			// Polymarket has no separate paper-trading mode; use local paper broker.
		}

		return r.fallbackPaperBroker(), "paper", nil
	}

	switch marketType {
	case domain.MarketTypeStock:
		if !r.cfg.Features.EnableLiveTrading {
			return nil, "", fmt.Errorf("live trading is disabled for strategy %s", strategy.Name)
		}
		if !hasBrokerCredentials(r.cfg.Brokers.Alpaca) {
			return nil, "", errors.New("alpaca broker credentials are required for live stock trading")
		}
		return alpacaexecution.NewBroker(alpacaexecution.NewClient(
			r.cfg.Brokers.Alpaca.APIKey,
			r.cfg.Brokers.Alpaca.APISecret,
			false,
			r.logger,
		)), "alpaca", nil
	case domain.MarketTypeCrypto:
		if !r.cfg.Features.EnableLiveTrading {
			return nil, "", fmt.Errorf("live trading is disabled for strategy %s", strategy.Name)
		}
		if !hasBrokerCredentials(r.cfg.Brokers.Binance) {
			return nil, "", errors.New("binance broker credentials are required for live crypto trading")
		}
		return binanceexecution.NewBroker(binanceexecution.NewClient(
			r.cfg.Brokers.Binance.APIKey,
			r.cfg.Brokers.Binance.APISecret,
			false,
			r.logger,
		)), "binance", nil
	case domain.MarketTypePolymarket:
		if !r.cfg.Features.EnableLiveTrading {
			return nil, "", fmt.Errorf("live trading is disabled for strategy %s", strategy.Name)
		}
		pm := r.cfg.Brokers.Polymarket
		if strings.TrimSpace(pm.KeyID) == "" || strings.TrimSpace(pm.SecretKey) == "" {
			return nil, "", errors.New("polymarket credentials (POLYMARKET_KEY_ID and POLYMARKET_SECRET_KEY) are required for live polymarket trading")
		}
		if r.polymarketClient == nil {
			return nil, "", errors.New("polymarket client not initialised")
		}
		return polymarketexecution.NewBroker(r.polymarketClient), "polymarket", nil
	case domain.MarketTypeKalshi:
		if !r.cfg.Features.EnableLiveTrading {
			return nil, "", fmt.Errorf("live trading is disabled for strategy %s", strategy.Name)
		}
		kc := r.cfg.Brokers.Kalshi
		if strings.TrimSpace(kc.APIKeyID) == "" || strings.TrimSpace(kc.PrivateKeyPEMB64) == "" {
			return nil, "", errors.New("kalshi credentials (KALSHI_API_KEY_ID and KALSHI_PRIVATE_KEY_PEM_B64) are required for live kalshi trading")
		}
		if r.kalshiLiveClient == nil {
			return nil, "", errors.New("kalshi live client is not initialised")
		}
		return kalshiexecution.NewBroker(r.kalshiLiveClient), "kalshi", nil
	default:
		return nil, "", fmt.Errorf("live trading is not supported for market type %q", strategy.MarketType)
	}
}

func brokerNameForStrategy(strategy domain.Strategy) string {
	switch strategy.MarketType.Normalize() {
	case domain.MarketTypeStock:
		return "alpaca"
	case domain.MarketTypeCrypto:
		return "binance"
	case domain.MarketTypePolymarket:
		return "polymarket"
	case domain.MarketTypeKalshi:
		return "kalshi"
	default:
		return ""
	}
}

func (r *realStrategyRunner) fallbackPaperBroker() *paper.PaperBroker {
	if r == nil {
		return newConfiguredPaperBroker(config.PaperConfig{}, slog.Default())
	}

	r.localPaperMu.Lock()
	defer r.localPaperMu.Unlock()

	if r.localPaperBroker == nil {
		r.localPaperBroker = newConfiguredPaperBroker(r.cfg.Paper, r.logger)
	}

	return r.localPaperBroker
}

func newConfiguredPaperBroker(cfg config.PaperConfig, logger *slog.Logger) *paper.PaperBroker {
	profile, err := cfg.EvaluationProfile()
	if err != nil {
		profile, _ = domain.NewPaperEvaluationProfile(
			domain.PaperEvaluationModeScored,
			config.DefaultPaperInitialCapital,
			config.DefaultPaperBuyingPowerMultiplier,
			config.DefaultPaperSlippageBPS,
			config.DefaultPaperFeePct,
		)
		if logger != nil {
			logger.Warn("paper evaluation config invalid; using scored defaults", slog.Any("error", err))
		}
	}
	broker, brokerErr := paper.NewPaperBrokerWithProfile(profile)
	if brokerErr != nil {
		panic(fmt.Sprintf("validated paper profile rejected: %v", brokerErr))
	}
	return broker
}

func (r *realStrategyRunner) setRiskPortfolioSnapshotSource(broker execution.Broker) {
	if broker == nil || r == nil || r.positionRepo == nil {
		return
	}

	engineImpl, ok := r.riskEngine.(*risk.RiskEngineImpl)
	if !ok {
		return
	}

	engineImpl.SetPortfolioSnapshotFunc(func(ctx context.Context) (risk.Portfolio, error) {
		return execution.BuildRiskPortfolioSnapshot(ctx, r.executionAccount, broker, r.positionRepo)
	})
}

func hasBrokerCredentials(cfg config.BrokerConfig) bool {
	return strings.TrimSpace(cfg.APIKey) != "" && strings.TrimSpace(cfg.APISecret) != ""
}

func latestSocialSnapshot(snapshots []data.SocialSentiment) *data.SocialSentiment {
	if len(snapshots) == 0 {
		return nil
	}

	latest := snapshots[0]
	for _, snapshot := range snapshots[1:] {
		if snapshot.MeasuredAt.After(latest.MeasuredAt) {
			latest = snapshot
		}
	}

	return &latest
}

// drainPipelineEvents reads phase events emitted by the agent runner and
// broadcasts them to the WebSocket hub. It exits when the channel is closed.
func (r *realStrategyRunner) drainPipelineEvents(events <-chan agent.PipelineEvent) {
	for e := range events {
		msg := pipelineEventToWSMessage(e, r.executionAccount.AccountID())
		if msg.Type == "" {
			continue // unmapped event type — skip
		}
		r.hub.Broadcast(msg)
	}
}

// pipelineEventToWSMessage converts an agent.PipelineEvent to an api.WSMessage
// using the event-type vocabulary defined in internal/api/hub.go.
func pipelineEventToWSMessage(e agent.PipelineEvent, accountID uuid.UUID) api.WSMessage {
	switch e.Type {
	case agent.PipelineStarted:
		return api.WSMessage{
			Type:       api.EventPipelineStart,
			AccountID:  accountID,
			Scope:      "account",
			StrategyID: e.StrategyID,
			RunID:      e.PipelineRunID,
			Data:       map[string]any{"phase": e.Phase, "ticker": e.Ticker},
			Timestamp:  e.OccurredAt,
		}
	case agent.AgentDecisionMade:
		return api.WSMessage{
			Type:       api.EventAgentDecision,
			AccountID:  accountID,
			Scope:      "account",
			StrategyID: e.StrategyID,
			RunID:      e.PipelineRunID,
			Data:       map[string]any{"agent_role": e.AgentRole, "phase": e.Phase},
			Timestamp:  e.OccurredAt,
		}
	case agent.DebateRoundCompleted:
		return api.WSMessage{
			Type:       api.EventDebateRound,
			AccountID:  accountID,
			Scope:      "account",
			StrategyID: e.StrategyID,
			RunID:      e.PipelineRunID,
			Data:       map[string]any{"phase": e.Phase, "round": e.Round},
			Timestamp:  e.OccurredAt,
		}
	case agent.PipelineError:
		return api.WSMessage{
			Type:       api.EventError,
			AccountID:  accountID,
			Scope:      "account",
			StrategyID: e.StrategyID,
			RunID:      e.PipelineRunID,
			Data:       map[string]any{"error": e.Error, "timed_out": e.TimedOut, "used_fallback": e.UsedFallback},
			Timestamp:  e.OccurredAt,
		}
	case agent.PipelineCompleted:
		if !e.UsedFallback && !e.TimedOut {
			return api.WSMessage{}
		}
		return api.WSMessage{
			Type:       api.EventPipelineHealth,
			AccountID:  accountID,
			Scope:      "account",
			StrategyID: e.StrategyID,
			RunID:      e.PipelineRunID,
			Data:       map[string]any{"timed_out": e.TimedOut, "used_fallback": e.UsedFallback},
			Timestamp:  e.OccurredAt,
		}
	default:
		// LLMCacheStatsReported — no WS mapping needed.
		return api.WSMessage{}
	}
}

func contextErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func (r *realStrategyRunner) dispatchNotifications(ctx context.Context, strategy domain.Strategy, run *domain.PipelineRun, state *agent.PipelineState) error {
	if r.notificationManager == nil || run == nil || state == nil {
		return nil
	}

	signal := state.FinalSignal.Signal
	if signal == "" {
		signal = state.TradingPlan.Action
	}
	if signal == "" {
		signal = domain.PipelineSignalHold
	}

	occurredAt := time.Time{}
	if run.CompletedAt != nil {
		occurredAt = *run.CompletedAt
	}

	reasoning := state.TradingPlan.Rationale
	if reasoning == "" {
		reasoning = state.RiskDebate.FinalSignal
	}

	if err := r.notificationManager.RecordSignal(ctx, notification.SignalEvent{
		StrategyID:   strategy.ID,
		StrategyName: strategy.Name,
		RunID:        run.ID,
		Ticker:       strategy.Ticker,
		Signal:       signal,
		Confidence:   state.FinalSignal.Confidence,
		Reasoning:    reasoning,
		OccurredAt:   occurredAt,
	}); err != nil {
		return fmt.Errorf("dispatch signal notification: %w", err)
	}

	decisions, err := r.decisionRepo.GetByRun(ctx, domain.PipelineRunRef{ID: run.ID, TradeDate: run.TradeDate}, repository.AgentDecisionFilter{}, 100, 0)
	if err != nil {
		return fmt.Errorf("load run decisions: %w", err)
	}
	for _, decision := range decisions {
		if err := r.notificationManager.RecordDecision(ctx, notification.DecisionEvent{
			StrategyID:    strategy.ID,
			RunID:         run.ID,
			AgentRole:     decision.AgentRole,
			Phase:         decision.Phase,
			OutputSummary: decision.OutputText,
			LLMProvider:   decision.LLMProvider,
			LLMModel:      decision.LLMModel,
			LatencyMS:     decision.LatencyMS,
			OccurredAt:    decision.CreatedAt,
		}); err != nil {
			return fmt.Errorf("dispatch decision notification: %w", err)
		}
	}

	return nil
}

func (r *realStrategyRunner) findRun(ctx context.Context, ref domain.PipelineRunRef) (*domain.PipelineRun, error) {
	run, err := r.runRepo.Get(ctx, ref)
	if err == nil {
		return run, nil
	}
	if errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("run %s: %w", ref.ID, repository.ErrNotFound)
	}
	return nil, err
}

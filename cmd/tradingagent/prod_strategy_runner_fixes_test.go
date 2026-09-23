package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/PatrickFanella/get-rich-quick/internal/config"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/metrics"
	"github.com/PatrickFanella/get-rich-quick/internal/notification"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type capturingSignalNotifier struct {
	events []notification.SignalEvent
	alerts []notification.Alert
}

func (n *capturingSignalNotifier) Notify(_ context.Context, alert notification.Alert) error {
	n.alerts = append(n.alerts, alert)
	return nil
}

func (n *capturingSignalNotifier) NotifySignal(_ context.Context, event notification.SignalEvent) error {
	n.events = append(n.events, event)
	return nil
}

func TestRecordStrategyPreparationFailureIsVisibleBeyondAgentEvents(t *testing.T) {
	var logs bytes.Buffer
	notifier := &capturingSignalNotifier{}
	appMetrics := metrics.New()
	runner := &realStrategyRunner{
		executionAccount:    testExecutionAccountBinding,
		eventRepo:           &recordingStrategyPreparationEventRepo{},
		metrics:             appMetrics,
		notificationManager: notification.NewManager(config.AlertRulesConfig{PipelineFailure: config.PipelineFailureAlertRuleConfig{Channels: []string{notification.ChannelDiscord}}}, map[string]notification.Notifier{notification.ChannelDiscord: notifier}),
		logger:              slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
	strategy := domain.Strategy{ID: uuid.New(), Name: "paper-spy", Ticker: "SPY", MarketType: domain.MarketTypeStock}
	err := runner.recordStrategyPreparationFailure(context.Background(), strategy, uuid.New(), fmt.Errorf("fundamentals completeness below threshold: 2 of 5 core metrics available"))
	if err != nil {
		t.Fatalf("recordStrategyPreparationFailure() error = %v", err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "reason_code=fundamentals_incomplete") || !strings.Contains(logs.String(), "ticker=SPY") {
		t.Fatalf("warn log missing: %s", logs.String())
	}
	if got := testutil.ToFloat64(appMetrics.StrategyPreparationRejectedTotal.WithLabelValues("SPY", "fundamentals_incomplete")); got != 1 {
		t.Fatalf("preparation_rejected counter = %v, want 1", got)
	}
	if len(notifier.alerts) != 1 || notifier.alerts[0].Metadata["strategy_id"] != strategy.ID.String() || notifier.alerts[0].Metadata["reason_code"] != "fundamentals_incomplete" {
		t.Fatalf("notification alerts = %+v, want one rejection alert", notifier.alerts)
	}
}

func TestPreparationRejectionErrorCarriesReasonCode(t *testing.T) {
	t.Parallel()
	cause := errors.New("fundamentals completeness below threshold: 2 of 5 core metrics available")
	wrapped := preparationRejectionError(cause)
	if !strings.HasPrefix(wrapped.Error(), "preparation rejected [fundamentals_incomplete]:") || !errors.Is(wrapped, cause) {
		t.Fatalf("wrapped = %v", wrapped)
	}
	if again := preparationRejectionError(wrapped); again.Error() != wrapped.Error() {
		t.Fatalf("double wrap = %v", again)
	}
	if got := preparationRejectionError(context.Canceled); got != context.Canceled {
		t.Fatalf("cancellation was relabelled: %v", got)
	}
	if got := preparationRejectionError(nil); got != nil {
		t.Fatalf("nil error became %v", got)
	}
}

func TestNormalizePlanPositionSizeTreatsOversizedLLMSizeAsDollars(t *testing.T) {
	t.Parallel()
	llm := &execution.DecisionMetadata{LLMProvider: "test"}
	plan := execution.TradingPlan{Ticker: "SPY", EntryPrice: 500, PositionSize: 25_000, DecisionMetadata: llm}
	normalized, converted := normalizePlanPositionSize(plan, 100_000)
	if !converted || normalized.PositionSize != 50 {
		t.Fatalf("25000 'shares' at $500 on $100k equity -> %v (converted=%v), want 50 units", normalized.PositionSize, converted)
	}
	// Within ten times equity the value is trusted as shares.
	if _, converted := normalizePlanPositionSize(execution.TradingPlan{EntryPrice: 500, PositionSize: 1_000, DecisionMetadata: llm}, 100_000); converted {
		t.Fatal("plausible share count was converted")
	}
	// Deterministic plans are never rewritten.
	if _, converted := normalizePlanPositionSize(execution.TradingPlan{EntryPrice: 500, PositionSize: 25_000}, 100_000); converted {
		t.Fatal("non-LLM plan was converted")
	}
	if _, converted := normalizePlanPositionSize(plan, 0); converted {
		t.Fatal("unknown equity must not convert")
	}
}

func TestRecordPortfolioOpportunitySkipsStrategiesWithoutPromotionLineage(t *testing.T) {
	t.Parallel()
	repo := &recordingOpportunityRepo{}
	runner := &realStrategyRunner{opportunityRepo: repo, logger: slogDiscardLogger()}
	versionID := uuid.New()
	strategy := domain.Strategy{ID: uuid.New(), Ticker: "SAFE", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true, ExecutionStrategyVersionID: &versionID, Config: json.RawMessage(`{}`)}
	run := &domain.PipelineRun{ID: uuid.New(), AccountID: uuid.New(), Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: versionID.String(), StrategyID: strategy.ID, TradeDate: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC), Status: domain.PipelineStatusCompleted, Signal: domain.PipelineSignalBuy}
	plan := execution.TradingPlan{EntryPrice: 100, PositionSize: 2, RiskReward: 3, Confidence: 0.8}
	if err := runner.recordPortfolioOpportunity(context.Background(), strategy, run, execution.FinalSignal{Signal: domain.PipelineSignalBuy, Confidence: 0.8}, plan, nil); err != nil {
		t.Fatalf("unpromoted strategy must skip silently, got %v", err)
	}
	if len(repo.queued) != 0 {
		t.Fatalf("queued = %d, want none", len(repo.queued))
	}
}

func TestRecordPortfolioOpportunityNonFatalLogsInsteadOfFailing(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	runner := &realStrategyRunner{opportunityRepo: &recordingOpportunityRepo{}, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	strategy := domain.Strategy{ID: uuid.New(), Ticker: "SAFE", MarketType: domain.MarketTypeStock}
	// A failed source run is a bookkeeping error; it must not propagate.
	run := &domain.PipelineRun{ID: uuid.New(), Status: domain.PipelineStatusFailed}
	runner.recordPortfolioOpportunityNonFatal(context.Background(), strategy, run, execution.FinalSignal{Signal: domain.PipelineSignalBuy}, execution.TradingPlan{EntryPrice: 1, PositionSize: 1}, nil)
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "bookkeeping failed") {
		t.Fatalf("expected warn log, got %s", logs.String())
	}
}

func TestNewBrokerForStrategyReusesCachedClients(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	cfg.Brokers.Alpaca = config.BrokerConfig{APIKey: "key", APISecret: "secret", PaperMode: true}
	cfg.Brokers.Binance = config.BrokerConfig{APIKey: "key", APISecret: "secret", PaperMode: true}
	runner := &realStrategyRunner{cfg: cfg, logger: slogDiscardLogger()}
	stock := domain.Strategy{MarketType: domain.MarketTypeStock, IsPaper: true}
	first, name, err := runner.newBrokerForStrategy(stock)
	if err != nil || name != "alpaca" {
		t.Fatalf("first broker = %v, %s, %v", first, name, err)
	}
	second, _, err := runner.newBrokerForStrategy(stock)
	if err != nil || first != second {
		t.Fatalf("alpaca paper broker was rebuilt: %p vs %p (%v)", first, second, err)
	}
	crypto, name, err := runner.newBrokerForStrategy(domain.Strategy{MarketType: domain.MarketTypeCrypto, IsPaper: true})
	if err != nil || name != "binance" || crypto == first {
		t.Fatalf("crypto broker = %v, %s, %v", crypto, name, err)
	}
	if again, _, _ := runner.newBrokerForStrategy(domain.Strategy{MarketType: domain.MarketTypeCrypto, IsPaper: true}); again != crypto {
		t.Fatal("binance paper broker was rebuilt")
	}
	if len(runner.brokerCache) != 2 {
		t.Fatalf("cache entries = %d, want 2", len(runner.brokerCache))
	}
}

type polymarketPositionRepoStub struct {
	stubPositionRepo
	open map[string]bool
	err  error
}

func (s polymarketPositionRepoStub) GetOpenByAccount(_ context.Context, _ uuid.UUID, _ domain.AccountEnvironment, filter repository.PositionFilter, _, _ int) ([]domain.Position, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.open[filter.Ticker] {
		return []domain.Position{{Ticker: filter.Ticker, Quantity: 3}}, nil
	}
	return nil, nil
}

func TestPolymarketWorkerRetireReason(t *testing.T) {
	t.Parallel()
	runner := &realStrategyRunner{executionAccount: testExecutionAccountBinding, positionRepo: polymarketPositionRepoStub{open: map[string]bool{"market-a:YES": true}}}
	if reason := runner.polymarketWorkerRetireReason(context.Background(), "market-a", time.Now()); reason != "" {
		t.Fatalf("open position retired worker: %q", reason)
	}
	if reason := runner.polymarketWorkerRetireReason(context.Background(), "market-b", time.Now()); reason != "position_closed" {
		t.Fatalf("closed position reason = %q", reason)
	}
	if reason := runner.polymarketWorkerRetireReason(context.Background(), "market-a", time.Now().Add(-2*polymarketWorkerIdleTimeout)); reason != "feed_idle" {
		t.Fatalf("idle reason = %q", reason)
	}
	// When the repository cannot answer, only idleness retires the worker.
	failing := &realStrategyRunner{executionAccount: testExecutionAccountBinding, positionRepo: polymarketPositionRepoStub{err: errors.New("db down")}}
	if reason := failing.polymarketWorkerRetireReason(context.Background(), "market-b", time.Now()); reason != "" {
		t.Fatalf("unknown repo state retired worker: %q", reason)
	}
}

func TestOrderManagerForOrderRejectsBrokerMismatch(t *testing.T) {
	t.Parallel()
	runner := &realStrategyRunner{executionAccount: testExecutionAccountBinding, positionRepo: stubPositionRepo{}, logger: slogDiscardLogger()}
	order := domain.Order{ID: uuid.New(), Ticker: "SPY", MarketType: domain.MarketTypeStock, Broker: "alpaca"}
	if _, err := runner.OrderManagerForOrder(context.Background(), order); err == nil || !strings.Contains(err.Error(), `"alpaca"`) {
		t.Fatalf("mismatch error = %v", err)
	}
	order.Broker = "paper"
	manager, err := runner.OrderManagerForOrder(context.Background(), order)
	if err != nil || manager == nil {
		t.Fatalf("paper manager = %v, %v", manager, err)
	}
}

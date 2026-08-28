package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/risk"
)

// Order event kinds emitted by the OrderManager.
const (
	OrderEventSubmitted = "order_submitted"
	OrderEventFilled    = "order_filled"
	OrderEventCancelled = "order_cancelled"
	OrderEventRejected  = "order_rejected"
)

// FinalSignal stores the extracted pipeline signal and confidence.
type FinalSignal struct {
	Signal     domain.PipelineSignal `json:"signal,omitempty"`
	Confidence float64               `json:"confidence,omitempty"`
}

// TradingPlan stores the structured output produced by the trader phase.
type TradingPlan struct {
	Action           domain.PipelineSignal `json:"action,omitempty"`
	MarketType       domain.MarketType     `json:"market_type,omitempty"`
	Ticker           string                `json:"ticker,omitempty"`
	EntryType        string                `json:"entry_type,omitempty"`
	EntryPrice       float64               `json:"entry_price,omitempty"`
	ReferencePrice   float64               `json:"reference_price,omitempty"`
	PositionSize     float64               `json:"position_size,omitempty"`
	StopLoss         float64               `json:"stop_loss,omitempty"`
	TakeProfit       float64               `json:"take_profit,omitempty"`
	TimeHorizon      string                `json:"time_horizon,omitempty"`
	Confidence       float64               `json:"confidence,omitempty"`
	Rationale        string                `json:"rationale,omitempty"`
	RiskReward       float64               `json:"risk_reward,omitempty"`
	Side             string                `json:"side,omitempty"`
	DecisionMetadata *DecisionMetadata     `json:"decision_metadata,omitempty"`
	OptionGreeks     *domain.OptionGreeks  `json:"option_greeks,omitempty"`
	ExternalMarketID string                `json:"external_market_id,omitempty"`
	FairValue        float64               `json:"fair_value,omitempty"`
	Spread           float64               `json:"spread,omitempty"`
	Depth            float64               `json:"depth,omitempty"`
	GrossEV          float64               `json:"gross_ev,omitempty"`
	NetEV            float64               `json:"net_ev,omitempty"`
	Evidence         json.RawMessage       `json:"evidence,omitempty"`
	Features         json.RawMessage       `json:"features,omitempty"`
	RegimeTags       []string              `json:"regime_tags,omitempty"`
}

// DecisionMetadata captures prompt and LLM usage details for a trading decision.
type DecisionMetadata struct {
	PromptText       string   `json:"prompt_text,omitempty"`
	LLMProvider      string   `json:"llm_provider,omitempty"`
	LLMModel         string   `json:"llm_model,omitempty"`
	PromptTokens     *int     `json:"prompt_tokens,omitempty"`
	CompletionTokens *int     `json:"completion_tokens,omitempty"`
	LatencyMS        *int     `json:"latency_ms,omitempty"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
}

// SizingConfig holds the parameters used to size positions.
type SizingConfig struct {
	Method          PositionSizingMethod
	RiskPct         float64
	ATRMultiplier   float64
	WinRate         float64
	WinLossRatio    float64
	FractionPct     float64
	MaxPositionUSDC float64
	HalfKelly       bool
}

// OrderManager orchestrates the full order lifecycle:
// Signal → Risk Check → Size → Create → Submit → Track → Update Position → Audit.
type OrderManager struct {
	broker           Broker
	brokerName       string
	riskEngine       risk.RiskEngine
	positionRepo     repository.PositionRepository
	orderRepo        repository.OrderRepository
	tradeRepo        repository.TradeRepository
	financialRepo    repository.FinancialLifecycleRepository
	auditLogRepo     repository.AuditLogRepository
	agentEventRepo   repository.AgentEventRepository
	decisionRecorder DecisionRecorder
	sizingConfig     SizingConfig
	liveTrading      bool
	liveGate         LiveGateConfig
	logger           *slog.Logger
	nowMu            sync.RWMutex
	nowFunc          func() time.Time
	metrics          OrderMetricsRecorder
	effectFence      func(context.Context) error
	accountLocker    repository.ExecutionAccountLocker
}

// WithEffectFence requires an ownership check immediately before execution effects.
func (m *OrderManager) WithEffectFence(fence func(context.Context) error) *OrderManager {
	if m != nil {
		m.effectFence = fence
	}
	return m
}

func (m *OrderManager) fenceEffect(ctx context.Context) error {
	if m == nil || m.effectFence == nil {
		return nil
	}
	if err := m.effectFence(ctx); err != nil {
		return fmt.Errorf("order_manager: execution ownership fence: %w", err)
	}
	return nil
}

// OrderMetricsRecorder records order lifecycle metrics.
type OrderMetricsRecorder interface {
	RecordOrder(broker, side, status string)
}

// NewOrderManager constructs an OrderManager with the given dependencies.
func NewOrderManager(
	broker Broker,
	brokerName string,
	riskEngine risk.RiskEngine,
	positionRepo repository.PositionRepository,
	orderRepo repository.OrderRepository,
	tradeRepo repository.TradeRepository,
	auditLogRepo repository.AuditLogRepository,
	agentEventRepo repository.AgentEventRepository,
	sizingConfig SizingConfig,
	logger *slog.Logger,
) *OrderManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &OrderManager{
		broker:         broker,
		brokerName:     brokerName,
		riskEngine:     riskEngine,
		positionRepo:   positionRepo,
		orderRepo:      orderRepo,
		tradeRepo:      tradeRepo,
		auditLogRepo:   auditLogRepo,
		agentEventRepo: agentEventRepo,
		sizingConfig:   sizingConfig,
		logger:         logger,
		nowFunc:        time.Now,
		accountLocker:  executionAccountLocker(orderRepo),
	}
}

// WithMetrics wires an optional metrics recorder into the manager.
func (m *OrderManager) WithMetrics(metrics OrderMetricsRecorder) *OrderManager {
	if m == nil {
		return nil
	}
	m.metrics = metrics
	return m
}

// WithDecisionRecorder wires an optional decision journal recorder into the manager.
func (m *OrderManager) WithDecisionRecorder(recorder DecisionRecorder) *OrderManager {
	if m == nil {
		return nil
	}
	m.decisionRecorder = recorder
	return m
}

// WithFinancialLifecycleRepo wires the atomic financial lifecycle repository.
func (m *OrderManager) WithFinancialLifecycleRepo(repo repository.FinancialLifecycleRepository) *OrderManager {
	if m == nil {
		return nil
	}
	m.financialRepo = repo
	return m
}

// WithLiveTrading toggles the live-execution path. Paper/default paths should
// leave this disabled.
func (m *OrderManager) WithLiveTrading(enabled bool) *OrderManager {
	if m == nil {
		return nil
	}
	m.liveTrading = enabled
	return m
}

// WithLiveGate configures the explicit live-trading gate.
func (m *OrderManager) WithLiveGate(gate LiveGateConfig) *OrderManager {
	if m == nil {
		return nil
	}
	m.liveGate = gate
	return m
}

// SetNowFunc overrides the order manager time source, allowing callers to
// drive all execution timestamps from a simulated backtest clock.
func (m *OrderManager) SetNowFunc(now func() time.Time) {
	if m == nil || now == nil {
		return
	}

	m.nowMu.Lock()
	defer m.nowMu.Unlock()

	m.nowFunc = now
}

func (m *OrderManager) currentTime() time.Time {
	if m == nil {
		return time.Now()
	}

	m.nowMu.RLock()
	defer m.nowMu.RUnlock()

	if m.nowFunc == nil {
		return time.Now()
	}

	return m.nowFunc()
}

// ProcessSignal executes the full order lifecycle for a trading signal.
func (m *OrderManager) ProcessSignal(
	ctx context.Context,
	scope ExecutionScope,
	signal FinalSignal,
	plan TradingPlan,
) error {
	if planMarketType(plan).Normalize() == domain.MarketTypeKalshi {
		if m.accountLocker == nil {
			return fmt.Errorf("order_manager: PostgreSQL execution account locker is required for Kalshi")
		}
		return m.accountLocker.WithExecutionAccountLock(ctx, scope.AccountID(), func() error {
			return m.processSignal(ctx, scope, signal, plan)
		})
	}
	return m.processSignal(ctx, scope, signal, plan)
}

func (m *OrderManager) processSignal(
	ctx context.Context,
	scope ExecutionScope,
	signal FinalSignal,
	plan TradingPlan,
) error {
	strategyID, runID, hasRun, err := scopeOriginIDs(scope)
	if err != nil {
		return fmt.Errorf("order_manager: execution scope: %w", err)
	}
	originType, originID := scope.Origin()
	marketType := planMarketType(plan)
	predictionExitMaxQuantity := 0.0
	stockExitMaxQuantity := 0.0
	riskReducingExit := false

	// Ignore hold signals — nothing to execute.
	if signal.Signal == domain.PipelineSignalHold {
		m.logger.InfoContext(ctx, "hold signal received, skipping order", "ticker", plan.Ticker)
		return nil
	}
	normalizedTicker, normalizedSide, err := NormalizePredictionOrderTicker(marketType, plan.Ticker, plan.Side)
	if err != nil {
		return fmt.Errorf("order_manager: normalize prediction order ticker: %w", err)
	}
	plan.Ticker = normalizedTicker
	plan.Side = normalizedSide
	if m.liveTrading {
		allowed, denial := m.liveGate.Allows(&strategyID, m.brokerName)
		if !allowed {
			m.logger.WarnContext(ctx, "live execution denied", "ticker", plan.Ticker, "strategy_version_id", strategyID, "broker", m.brokerName, "code", denial.Code, "reason", denial.Message)
			if err := m.recordTradeDecision(ctx, scope, m.newTradeDecision(scope, plan, marketType, strings.ToUpper(strings.TrimSpace(plan.Side)), 0, 0, domain.RiskDecisionRejected, []string{denial.Code + ": " + denial.Message}, domain.TradeDecisionStatusRejected)); err != nil {
				return err
			}
			return fmt.Errorf("order_manager: live execution denied for %s: %s", plan.Ticker, denial.Message)
		}
	}

	// A stock SELL signal only makes sense as an exit for a position this
	// strategy already owns. Do not turn discovery sell signals for unowned stock
	// symbols into broker orders; Alpaca will reject them and they are not
	// actionable trades. Non-stock markets have different SELL semantics and are
	// intentionally left to their market-specific execution/risk paths.
	if signal.Signal == domain.PipelineSignalSell && marketType == domain.MarketTypeStock {
		ownedQuantity, err := m.openLongPositionQuantity(ctx, scope, plan.Ticker)
		if err != nil {
			return err
		}
		if ownedQuantity <= 0 {
			m.logger.InfoContext(ctx, "sell signal has no open long position, skipping order", "ticker", plan.Ticker, "strategy_id", strategyID)

			decision := m.newTradeDecision(
				scope,
				plan,
				marketType,
				string(domain.OrderSideSell),
				0,
				0,
				domain.RiskDecisionRejected,
				[]string{"unowned_sell_no_open_long"},
				domain.TradeDecisionStatusRejected,
			)
			decision.Evidence, _ = json.Marshal(map[string]any{
				"reason":          "unowned_sell_no_open_long",
				"has_open_long":   false,
				"ticker":          plan.Ticker,
				"strategy_id":     strategyID.String(),
				"pipeline_run_id": runID.String(),
				"pipeline_signal": signal.Signal,
			})
			if err := m.recordTradeDecision(ctx, scope, decision); err != nil {
				return err
			}

			if auditErr := m.audit(ctx, "sell_without_position_skipped", "order", nil, map[string]any{
				"ticker":      plan.Ticker,
				"strategy_id": strategyID,
				"run_id":      runID,
				"signal":      signal.Signal,
			}); auditErr != nil {
				m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
			}

			return nil
		}
		stockExitMaxQuantity = ownedQuantity
		riskReducingExit = true
	}

	if signal.Signal == domain.PipelineSignalSell && isPredictionMarket(marketType) {
		ownedQuantity, err := m.openPredictionPositionQuantity(ctx, scope, marketType, plan.Ticker, plan.Side)
		if err != nil {
			return err
		}
		if ownedQuantity <= 0 {
			m.logger.InfoContext(ctx, "prediction-market sell signal has no open side-qualified position, skipping order", "ticker", plan.Ticker, "prediction_side", plan.Side, "market_type", marketType, "strategy_id", strategyID)

			rejectionReason := "unowned_polymarket_exit_no_open_position"
			if marketType.Normalize() == domain.MarketTypeKalshi {
				rejectionReason = "unowned_kalshi_exit_no_open_position"
			}
			decision := m.newTradeDecision(
				scope,
				plan,
				marketType,
				string(domain.OrderSideSell),
				0,
				0,
				domain.RiskDecisionRejected,
				[]string{rejectionReason},
				domain.TradeDecisionStatusRejected,
			)
			decision.Evidence, _ = json.Marshal(map[string]any{
				"reason":            rejectionReason,
				"has_open_position": false,
				"ticker":            plan.Ticker,
				"prediction_side":   plan.Side,
				"strategy_id":       strategyID.String(),
				"pipeline_run_id":   runID.String(),
				"pipeline_signal":   signal.Signal,
			})
			if err := m.recordTradeDecision(ctx, scope, decision); err != nil {
				return err
			}

			if auditErr := m.audit(ctx, "sell_without_position_skipped", "order", nil, map[string]any{
				"ticker":          plan.Ticker,
				"prediction_side": plan.Side,
				"strategy_id":     strategyID,
				"run_id":          runID,
				"signal":          signal.Signal,
				"market_type":     marketType,
			}); auditErr != nil {
				m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
			}

			return nil
		}
		predictionExitMaxQuantity = ownedQuantity
		riskReducingExit = true
	}

	// 1. Check kill switch via risk engine.
	active, err := m.riskEngine.IsKillSwitchActive(ctx)
	if err != nil {
		return fmt.Errorf("order_manager: kill switch check: %w", err)
	}

	if active && !riskReducingExit {
		m.logger.WarnContext(ctx, "kill switch active, order blocked", "ticker", plan.Ticker)

		if auditErr := m.audit(ctx, "kill_switch_blocked", "order", nil, map[string]any{
			"ticker":      plan.Ticker,
			"strategy_id": strategyID,
			"run_id":      runID,
			"signal":      signal.Signal,
		}); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}

		return fmt.Errorf("order_manager: kill switch active, order blocked for %s", plan.Ticker)
	}
	if active {
		m.logger.WarnContext(ctx, "kill switch active; verified reduce-only exit admitted", "ticker", plan.Ticker, "strategy_id", strategyID)
		if auditErr := m.audit(ctx, "kill_switch_reduce_only_admitted", "order", nil, map[string]any{
			"ticker":      plan.Ticker,
			"strategy_id": strategyID,
			"run_id":      runID,
			"signal":      signal.Signal,
		}); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}
	}

	// 2. Calculate position size.
	balance, err := m.broker.GetAccountBalance(ctx)
	if err != nil {
		return fmt.Errorf("order_manager: get account balance: %w", err)
	}

	quantity := CalculatePositionSize(m.sizingConfig.Method, PositionSizingParams{
		AccountValue:  balance.Equity,
		RiskPct:       m.sizingConfig.RiskPct,
		ATR:           math.Abs(plan.EntryPrice - plan.StopLoss),
		Multiplier:    m.sizingConfig.ATRMultiplier,
		WinRate:       m.sizingConfig.WinRate,
		WinLossRatio:  m.sizingConfig.WinLossRatio,
		FractionPct:   m.sizingConfig.FractionPct,
		PricePerShare: plan.EntryPrice,
		HalfKelly:     m.sizingConfig.HalfKelly,
	})
	if isPredictionMarket(marketType) {
		quantity = PolymarketPositionSize(PolymarketSizingParams{
			AccountValue:    balance.Equity,
			FractionPct:     m.sizingConfig.FractionPct,
			MaxPositionUSDC: m.sizingConfig.MaxPositionUSDC,
			EntryPrice:      plan.EntryPrice,
		})
		if signal.Signal == domain.PipelineSignalSell && predictionExitMaxQuantity > 0 && quantity > predictionExitMaxQuantity {
			quantity = predictionExitMaxQuantity
		}
		if signal.Signal == domain.PipelineSignalSell && plan.PositionSize > 0 {
			quantity = math.Min(plan.PositionSize, predictionExitMaxQuantity)
		}
		if marketType.Normalize() == domain.MarketTypeKalshi {
			quantity = quantizeKalshiContracts(quantity)
		}
	}
	if signal.Signal == domain.PipelineSignalSell && marketType == domain.MarketTypeStock && stockExitMaxQuantity > 0 {
		quantity = math.Min(quantity, stockExitMaxQuantity)
		if plan.PositionSize > 0 {
			quantity = math.Min(plan.PositionSize, stockExitMaxQuantity)
		}
	}

	if quantity <= 0 {
		m.logger.WarnContext(ctx, "calculated position size is zero", "ticker", plan.Ticker)
		return fmt.Errorf("order_manager: calculated position size is zero for %s", plan.Ticker)
	}

	// 3. Check position limits via risk engine.
	// Convert the position size (in units) into additional portfolio exposure (0–1 fraction)
	// for the risk engine. This aligns with RiskEngine.CheckPositionLimits expectations.
	if balance.Equity <= 0 {
		return fmt.Errorf("order_manager: account equity is zero or negative for %s", plan.Ticker)
	}

	additionalExposurePct := (quantity * plan.EntryPrice) / balance.Equity

	portfolio, err := m.buildRiskPortfolioSnapshot(ctx, balance, scope)
	if err != nil {
		return fmt.Errorf("order_manager: build risk portfolio: %w", err)
	}
	if marketType != "" && signal.Signal != domain.PipelineSignalSell {
		if portfolio.MarketExposurePct == nil {
			portfolio.MarketExposurePct = make(map[domain.MarketType]float64)
		}
		portfolio.MarketExposurePct[marketType] += additionalExposurePct
	}
	approved, reason := true, ""
	if signal.Signal != domain.PipelineSignalSell {
		approved, reason, err = m.riskEngine.CheckPositionLimits(ctx, plan.Ticker, additionalExposurePct, portfolio)
		if err != nil {
			return fmt.Errorf("order_manager: check position limits: %w", err)
		}
	}

	if !approved {
		m.logger.WarnContext(ctx, "position limits rejected", "ticker", plan.Ticker, "reason", reason)
		if err := m.recordTradeDecision(ctx, scope, m.newTradeDecision(
			scope,
			plan,
			marketType,
			strings.ToUpper(strings.TrimSpace(plan.Side)),
			quantity,
			0,
			domain.RiskDecisionRejected,
			[]string{reason},
			domain.TradeDecisionStatusRejected,
		)); err != nil {
			return err
		}

		if auditErr := m.audit(ctx, "risk_check_rejected", "order", nil, map[string]any{
			"ticker":      plan.Ticker,
			"strategy_id": strategyID,
			"run_id":      runID,
			"reason":      reason,
			"quantity":    quantity,
		}); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}

		return fmt.Errorf("order_manager: risk check rejected for %s: %s", plan.Ticker, reason)
	}

	// 4. Create order (status = pending).
	now := m.currentTime()
	side := m.signalToSide(signal.Signal)
	orderType := m.entryTypeToOrderType(plan.EntryType)

	order := &domain.Order{
		ID:                       uuid.New(),
		AccountID:                scope.AccountID(),
		Environment:              scope.Environment(),
		OriginType:               string(originType),
		OriginID:                 originID,
		CopyOriginRebalanceRunID: scope.CopyOriginRunID(),
		Ticker:                   plan.Ticker,
		MarketType:               marketType,
		Side:                     side,
		OrderType:                orderType,
		Quantity:                 quantity,
		Status:                   domain.OrderStatusPending,
		Broker:                   m.brokerName,
		CreatedAt:                now,
		PredictionSide:           plan.Side,
	}
	order.ClientOrderID = "augr-" + order.ID.String()
	if originType == ledger.ExecutionOriginStrategyVersion {
		order.StrategyID = scope.LegacyStrategyID()
	}
	if hasRun {
		run, _ := scope.PipelineRun()
		order.PipelineRunID = &runID
		tradeDate := run.TradeDate
		order.PipelineRunTradeDate = &tradeDate
	}
	if riskReducingExit {
		intent := domain.PositionIntentSellToClose
		order.PositionIntent = &intent
	}

	if plan.EntryPrice > 0 {
		order.LimitPrice = &plan.EntryPrice
	}
	if plan.ReferencePrice > 0 {
		referencePrice := plan.ReferencePrice
		order.ReferencePrice = &referencePrice
	}

	if plan.StopLoss > 0 {
		order.StopPrice = &plan.StopLoss
	}

	if err := m.orderRepo.Create(ctx, order); err != nil {
		return fmt.Errorf("order_manager: create order: %w", err)
	}
	m.recordOrderMetric(order.Side, order.Status)

	if auditErr := m.audit(ctx, "order_created", "order", &order.ID, map[string]any{
		"ticker":      plan.Ticker,
		"side":        side,
		"order_type":  orderType,
		"quantity":    quantity,
		"strategy_id": strategyID,
		"run_id":      runID,
	}); auditErr != nil {
		m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
	}

	// 5. Pre-trade risk check (circuit breaker + order validation).
	approved, reason, err = m.riskEngine.CheckPreTrade(ctx, order, portfolio)
	if err != nil {
		return fmt.Errorf("order_manager: pre-trade check: %w", err)
	}

	if !approved {
		order.Status = domain.OrderStatusRejected
		if updateErr := m.orderRepo.Update(ctx, order); updateErr != nil {
			m.logger.ErrorContext(ctx, "failed to update rejected order", "error", updateErr)
		}
		m.recordOrderMetric(order.Side, order.Status)
		if err := m.recordTradeDecision(ctx, scope, m.newTradeDecision(
			scope,
			plan,
			order.MarketType,
			string(order.Side),
			quantity,
			0,
			domain.RiskDecisionRejected,
			[]string{reason},
			domain.TradeDecisionStatusRejected,
		)); err != nil {
			return err
		}

		if auditErr := m.audit(ctx, "pre_trade_rejected", "order", &order.ID, map[string]any{
			"reason": reason,
		}); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}

		return fmt.Errorf("order_manager: pre-trade check rejected for %s: %s", plan.Ticker, reason)
	}

	decision := m.newTradeDecision(
		scope,
		plan,
		order.MarketType,
		string(order.Side),
		quantity,
		quantity,
		domain.RiskDecisionApproved,
		nil,
		domain.TradeDecisionStatusCandidate,
	)
	decision.ID = recoveryTradeDecisionID(order.ID)
	if err := m.recordTradeDecision(ctx, scope, decision); err != nil {
		return err
	}
	if err := m.fenceEffect(ctx); err != nil {
		return err
	}
	if err := m.attachTradeDecisionOrder(ctx, scope, decision.ID, order.ID, m.liveTrading); err != nil {
		return err
	}

	// 6. Submit to broker (status = submitted).
	if err := m.fenceEffect(ctx); err != nil {
		return err
	}
	externalID, err := m.broker.SubmitOrder(ctx, order)
	if err != nil {
		order.Status = domain.OrderStatusRejected
		if updateErr := m.orderRepo.Update(ctx, order); updateErr != nil {
			m.logger.ErrorContext(ctx, "failed to update rejected order", "error", updateErr)
		}
		m.recordOrderMetric(order.Side, order.Status)
		if auditErr := m.audit(ctx, "order_rejected", "order", &order.ID, map[string]any{
			"error": err.Error(),
		}); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}

		m.emitOrderEvent(ctx, OrderEventRejected, order, scope)

		return fmt.Errorf("order_manager: submit order: %w", err)
	}

	submittedAt := m.currentTime()
	if err := m.orderRepo.Update(ctx, SanitizedSubmittedOrder(order, externalID, submittedAt)); err != nil {
		return fmt.Errorf("order_manager: update submitted order: %w", err)
	}
	order.ExternalID = externalID
	order.Status = domain.OrderStatusSubmitted
	order.SubmittedAt = &submittedAt
	m.recordOrderMetric(order.Side, order.Status)
	if auditErr := m.audit(ctx, "order_submitted", "order", &order.ID, map[string]any{
		"external_id": externalID,
	}); auditErr != nil {
		m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
	}

	m.emitOrderEvent(ctx, OrderEventSubmitted, order, scope)

	// 7. Check order status and handle fill.
	status, err := m.broker.GetOrderStatus(ctx, externalID)
	if err != nil {
		return fmt.Errorf("order_manager: get order status: %w", err)
	}

	order.Status = status
	if err := m.fenceEffect(ctx); err != nil {
		return err
	}

	switch status {
	case domain.OrderStatusFilled:
		return m.handleFill(ctx, order, plan, scope, decision.ID)
	case domain.OrderStatusCancelled:
		if err := m.orderRepo.Update(ctx, order); err != nil {
			return fmt.Errorf("order_manager: update %s order: %w", status, err)
		}
		m.recordOrderMetric(order.Side, order.Status)

		if auditErr := m.audit(ctx, "order_"+string(status), "order", &order.ID, nil); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}

		m.emitOrderEvent(ctx, OrderEventCancelled, order, scope)

		return nil
	case domain.OrderStatusRejected:
		if err := m.orderRepo.Update(ctx, order); err != nil {
			return fmt.Errorf("order_manager: update %s order: %w", status, err)
		}
		m.recordOrderMetric(order.Side, order.Status)

		if auditErr := m.audit(ctx, "order_"+string(status), "order", &order.ID, nil); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}

		m.emitOrderEvent(ctx, OrderEventRejected, order, scope)

		return nil
	default:
		// Partially filled or still submitted — persist the latest status.
		if err := m.orderRepo.Update(ctx, order); err != nil {
			return fmt.Errorf("order_manager: update order status: %w", err)
		}
		m.recordOrderMetric(order.Side, order.Status)

		return nil
	}
}

func executionAccountLocker(repo repository.OrderRepository) repository.ExecutionAccountLocker {
	locker, _ := repo.(repository.ExecutionAccountLocker)
	return locker
}

func quantizeKalshiContracts(quantity float64) float64 {
	if quantity <= 0 {
		return 0
	}
	return math.Floor((quantity+1e-9)*100) / 100
}

func planMarketType(plan TradingPlan) domain.MarketType {
	marketType := plan.MarketType.Normalize()
	if marketType == "" {
		return domain.MarketTypeStock
	}
	return marketType
}

func scopeOriginIDs(scope ExecutionScope) (uuid.UUID, uuid.UUID, bool, error) {
	if scope.AccountID() == uuid.Nil || !scope.Environment().IsValid() {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("account binding is required")
	}
	originType, originID := scope.Origin()
	originUUID := uuid.Nil
	if originType == ledger.ExecutionOriginStrategyVersion || originType == ledger.ExecutionOriginCopySubscription {
		var err error
		originUUID, err = uuid.Parse(originID)
		if err != nil {
			return uuid.Nil, uuid.Nil, false, fmt.Errorf("UUID execution origin is required for %s: %w", originType, err)
		}
	}
	run, hasRun := scope.PipelineRun()
	return originUUID, run.ID, hasRun, nil
}

func (m *OrderManager) newTradeDecision(
	scope ExecutionScope,
	plan TradingPlan,
	marketType domain.MarketType,
	side string,
	proposedSize, approvedSize float64,
	riskStatus domain.RiskDecisionStatus,
	riskReasons []string,
	status domain.TradeDecisionStatus,
) *domain.TradeDecision {
	decision := &domain.TradeDecision{
		ID:               uuid.New(),
		AccountID:        scope.AccountID(),
		Environment:      scope.Environment(),
		MarketType:       marketType.Normalize(),
		InstrumentKey:    strings.TrimSpace(plan.Ticker),
		Side:             domain.OrderSide(strings.ToLower(strings.TrimSpace(side))),
		ExecutablePrice:  plan.EntryPrice,
		ExternalMarketID: strings.TrimSpace(plan.ExternalMarketID),
		Outcome:          strings.ToUpper(strings.TrimSpace(plan.Side)),
		FairValue:        plan.FairValue,
		Spread:           plan.Spread,
		Depth:            plan.Depth,
		GrossEV:          plan.GrossEV,
		NetEV:            plan.NetEV,
		Evidence:         append(json.RawMessage(nil), plan.Evidence...),
		Features:         append(json.RawMessage(nil), plan.Features...),
		RegimeTags:       append([]string(nil), plan.RegimeTags...),
		ProposedSize:     proposedSize,
		ApprovedSize:     approvedSize,
		RiskStatus:       riskStatus,
		RiskReasons:      append([]string(nil), riskReasons...),
		Status:           status,
		CreatedAt:        m.currentTime(),
		UpdatedAt:        m.currentTime(),
	}
	originType, originID := scope.Origin()
	decision.OriginType, decision.OriginID = string(originType), originID
	if originType == ledger.ExecutionOriginStrategyVersion {
		decision.StrategyID = scope.LegacyStrategyID()
	}
	if run, ok := scope.PipelineRun(); ok {
		decision.PipelineRunID = &run.ID
		tradeDate := run.TradeDate
		decision.PipelineRunTradeDate = &tradeDate
	}
	if plan.DecisionMetadata != nil {
		decision.PromptText = plan.DecisionMetadata.PromptText
		decision.LLMProvider = strings.TrimSpace(plan.DecisionMetadata.LLMProvider)
		decision.LLMModel = strings.TrimSpace(plan.DecisionMetadata.LLMModel)
		decision.PromptTokens = cloneIntPtr(plan.DecisionMetadata.PromptTokens)
		decision.CompletionTokens = cloneIntPtr(plan.DecisionMetadata.CompletionTokens)
		decision.LatencyMS = cloneIntPtr(plan.DecisionMetadata.LatencyMS)
		decision.CostUSD = cloneFloatPtr(plan.DecisionMetadata.CostUSD)
	}
	return decision
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloatPtr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (m *OrderManager) recordTradeDecision(ctx context.Context, scope ExecutionScope, decision *domain.TradeDecision) error {
	if m == nil || m.decisionRecorder == nil || decision == nil {
		return nil
	}
	var err error
	if recorder, ok := m.decisionRecorder.(ScopedDecisionRecorder); ok {
		err = recorder.RecordDecisionScoped(ctx, scope, decision)
	} else {
		err = m.decisionRecorder.RecordDecision(ctx, decision)
	}
	if err != nil {
		return fmt.Errorf("order_manager: record trade decision: %w", err)
	}
	return nil
}

func (m *OrderManager) attachTradeDecisionOrder(ctx context.Context, scope ExecutionScope, decisionID, orderID uuid.UUID, live bool) error {
	if m == nil || m.decisionRecorder == nil || decisionID == uuid.Nil || orderID == uuid.Nil {
		return nil
	}
	var err error
	if recorder, ok := m.decisionRecorder.(ScopedDecisionRecorder); ok && live {
		err = recorder.AttachLiveOrderScoped(ctx, scope, decisionID, orderID)
	} else if recorder, ok := m.decisionRecorder.(ScopedDecisionRecorder); ok {
		err = recorder.AttachPaperOrderScoped(ctx, scope, decisionID, orderID)
	} else if live {
		err = m.decisionRecorder.AttachLiveOrder(ctx, decisionID, orderID)
	} else {
		err = m.decisionRecorder.AttachPaperOrder(ctx, decisionID, orderID)
	}
	if err != nil {
		return fmt.Errorf("order_manager: attach trade decision order: %w", err)
	}
	return nil
}

func (m *OrderManager) recordTradeDecisionReplay(ctx context.Context, scope ExecutionScope, decisionID uuid.UUID, eventType domain.ReplayEventType, payload any) error {
	if m == nil || decisionID == uuid.Nil {
		return fmt.Errorf("order_manager: replay event requires attached decision")
	}
	recorder, ok := m.decisionRecorder.(ReplayDecisionRecorder)
	if !ok {
		return nil
	}
	var err error
	if scoped, ok := recorder.(ScopedDecisionRecorder); ok {
		err = scoped.RecordReplayEventScoped(ctx, scope, decisionID, eventType, "order_manager", payload, m.currentTime())
	} else {
		err = recorder.RecordReplayEvent(ctx, decisionID, eventType, "order_manager", payload, m.currentTime())
	}
	if err != nil {
		return fmt.Errorf("order_manager: record %s replay event: %w", eventType, err)
	}
	return nil
}

func (m *OrderManager) resolveAttachedOrderDecision(ctx context.Context, scope ExecutionScope, orderID uuid.UUID) (uuid.UUID, error) {
	resolver, ok := m.decisionRecorder.(AttachedOrderDecisionRecorder)
	if !ok {
		return uuid.Nil, fmt.Errorf("order_manager: attached order decision resolver is required for fill recovery")
	}
	decisionID, err := resolver.ResolveAttachedOrderDecision(ctx, scope, orderID, m.liveTrading)
	if err != nil {
		return uuid.Nil, fmt.Errorf("order_manager: resolve recovered fill decision: %w", err)
	}
	if decisionID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("order_manager: recovered fill has no attached decision")
	}
	return decisionID, nil
}

func recoveryTradeDecisionID(orderID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("augr:order-decision:v1:"+orderID.String()))
}

func (m *OrderManager) ensureAttachedOrderDecision(ctx context.Context, scope ExecutionScope, order *domain.Order) (uuid.UUID, error) {
	if order == nil {
		return uuid.Nil, fmt.Errorf("order_manager: persisted order is required")
	}
	if decisionID, err := m.resolveAttachedOrderDecision(ctx, scope, order.ID); err == nil {
		return decisionID, nil
	}
	recoverer, ok := m.decisionRecorder.(RecoverableOrderDecisionRecorder)
	if !ok {
		return uuid.Nil, fmt.Errorf("order_manager: recoverable decision recorder is required before broker recovery")
	}
	createdAt := order.CreatedAt
	if createdAt.IsZero() {
		createdAt = m.currentTime()
	}
	executablePrice := 0.0
	if order.LimitPrice != nil {
		executablePrice = *order.LimitPrice
	}
	decision := &domain.TradeDecision{
		ID: recoveryTradeDecisionID(order.ID), AccountID: scope.AccountID(), Environment: scope.Environment(),
		MarketType: order.MarketType.Normalize(), InstrumentKey: strings.TrimSpace(order.Ticker), Side: order.Side,
		Outcome: strings.ToUpper(strings.TrimSpace(order.PredictionSide)), ExecutablePrice: executablePrice,
		ProposedSize: order.Quantity, ApprovedSize: order.Quantity, RiskStatus: domain.RiskDecisionApproved,
		Status: domain.TradeDecisionStatusCandidate, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	originType, originID := scope.Origin()
	decision.OriginType, decision.OriginID = string(originType), originID
	decision.StrategyID = order.StrategyID
	decision.PipelineRunID = order.PipelineRunID
	decision.PipelineRunTradeDate = order.PipelineRunTradeDate
	decisionID, err := recoverer.EnsureOrderDecisionAttachment(ctx, scope, decision, order.ID, m.liveTrading)
	if err != nil {
		return uuid.Nil, fmt.Errorf("order_manager: repair recovered order decision attachment: %w", err)
	}
	if decisionID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("order_manager: repaired order decision attachment is missing")
	}
	return decisionID, nil
}

func (m *OrderManager) openLongPositionQuantity(ctx context.Context, scope ExecutionScope, ticker string) (float64, error) {
	ticker = strings.TrimSpace(ticker)
	if ticker == "" {
		return 0, fmt.Errorf("order_manager: open long ownership check requires ticker")
	}

	positions, err := m.positionsByScope(ctx, scope, repository.PositionFilter{
		Ticker: ticker,
		Side:   domain.PositionSideLong,
	})
	if err != nil {
		return 0, fmt.Errorf("order_manager: get open long position for %s: %w", ticker, err)
	}

	total := 0.0
	for _, position := range positions {
		if position.ClosedAt == nil && position.Quantity > 0 {
			total += position.Quantity
		}
	}
	return total, nil
}

func (m *OrderManager) openPredictionPositionQuantity(ctx context.Context, scope ExecutionScope, marketType domain.MarketType, slug, side string) (float64, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return 0, fmt.Errorf("order_manager: prediction exit ownership check requires ticker")
	}

	positions, err := m.positionsByScope(ctx, scope, repository.PositionFilter{
		Ticker: polymarketPositionTicker(slug, side),
		Side:   domain.PositionSideLong,
	})
	if err != nil {
		return 0, fmt.Errorf("order_manager: get open %s position for %s:%s: %w", marketType.Normalize(), slug, strings.ToUpper(strings.TrimSpace(side)), err)
	}

	total := 0.0
	for _, position := range positions {
		if position.ClosedAt == nil && position.Quantity > 0 {
			total += position.Quantity
		}
	}

	return total, nil
}

func (m *OrderManager) positionsByScope(ctx context.Context, scope ExecutionScope, filter repository.PositionFilter) ([]domain.Position, error) {
	repo, ok := m.positionRepo.(repository.ExecutionScopedPositionRepository)
	if !ok {
		return nil, fmt.Errorf("canonical execution-scoped position repository is required")
	}
	originType, originID := scope.Origin()
	return repo.GetByExecutionScope(ctx, scope.AccountID(), scope.Environment(), string(originType), originID, filter, riskSnapshotPositionLimit, 0)
}

func (m *OrderManager) buildRiskPortfolioSnapshot(ctx context.Context, balance Balance, scope ExecutionScope) (risk.Portfolio, error) {
	repo, ok := m.positionRepo.(repository.AccountScopedPositionRepository)
	if !ok {
		return risk.Portfolio{}, fmt.Errorf("canonical account-scoped position repository is required")
	}
	positions, err := repo.GetByAccount(ctx, scope.AccountID(), scope.Environment(), repository.PositionFilter{}, riskSnapshotPositionLimit, 0)
	if err != nil {
		return risk.Portfolio{}, err
	}
	portfolio := risk.Portfolio{ConcurrentPositions: len(positions), PositionExposureBySymbol: make(map[string]float64, len(positions)), MarketExposurePct: make(map[domain.MarketType]float64, len(positions))}
	if len(positions) == 0 {
		return portfolio, nil
	}
	if balance.Equity <= 0 {
		return risk.Portfolio{}, fmt.Errorf("account equity must be positive")
	}
	for _, position := range positions {
		notional, err := positionNotional(position)
		if err != nil {
			return risk.Portfolio{}, err
		}
		exposure := notional / balance.Equity
		portfolio.TotalExposurePct += exposure
		portfolio.PositionExposureBySymbol[position.Ticker] += exposure
		if position.MarketType != "" {
			portfolio.MarketExposurePct[position.MarketType] += exposure
		}
	}
	return portfolio, nil
}

func isPredictionMarket(marketType domain.MarketType) bool {
	normalized := marketType.Normalize()
	return normalized == domain.MarketTypePolymarket || normalized == domain.MarketTypeKalshi
}

func polymarketPositionTicker(slug, side string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}

	side = strings.ToUpper(strings.TrimSpace(side))
	if side == "" {
		return slug
	}
	if existingSlug, existingSide, ok := strings.Cut(slug, ":"); ok {
		existingSlug = strings.TrimSpace(existingSlug)
		existingSide = strings.ToUpper(strings.TrimSpace(existingSide))
		if existingSlug != "" && (existingSide == "YES" || existingSide == "NO") {
			return existingSlug + ":" + existingSide
		}
	}

	return slug + ":" + side
}

func NormalizePredictionOrderTicker(marketType domain.MarketType, ticker, predictionSide string) (string, string, error) {
	trimmedTicker := strings.TrimSpace(ticker)
	normalizedSide := strings.ToUpper(strings.TrimSpace(predictionSide))
	if marketType.Normalize() != domain.MarketTypePolymarket && marketType.Normalize() != domain.MarketTypeKalshi {
		return trimmedTicker, normalizedSide, nil
	}
	if slug, tickerSide, ok := strings.Cut(trimmedTicker, ":"); ok {
		slug = strings.TrimSpace(slug)
		tickerSide = strings.ToUpper(strings.TrimSpace(tickerSide))
		if slug != "" && (tickerSide == "YES" || tickerSide == "NO") {
			if normalizedSide != "YES" && normalizedSide != "NO" {
				return strings.ToUpper(slug), tickerSide, nil
			}
			if tickerSide != normalizedSide {
				return "", "", fmt.Errorf("order_manager: prediction ticker suffix %q conflicts with prediction side %q", tickerSide, normalizedSide)
			}
			return strings.ToUpper(slug), normalizedSide, nil
		}
	}
	if normalizedSide != "YES" && normalizedSide != "NO" {
		return "", "", fmt.Errorf("order_manager: prediction order requires valid side YES or NO")
	}
	return trimmedTicker, normalizedSide, nil
}

func realizedPnL(side domain.PositionSide, avgEntry, fillPrice, quantity float64) float64 {
	if side == domain.PositionSideLong {
		return (fillPrice - avgEntry) * quantity
	}
	return (avgEntry - fillPrice) * quantity
}

func SanitizedSubmittedOrder(order *domain.Order, externalID string, submittedAt time.Time) *domain.Order {
	if order == nil {
		return nil
	}

	cp := *order
	cp.ExternalID = externalID
	cp.Status = domain.OrderStatusSubmitted
	cp.SubmittedAt = &submittedAt
	cp.FilledQuantity = 0
	cp.FilledAvgPrice = nil
	cp.FilledAt = nil
	return &cp
}

// ReconcilePersistedOrder resolves an allocator-owned paper order without
// submitting a replacement. Resting paper orders are cancelled so restart
// recovery always reaches a durable terminal state.
func (m *OrderManager) ReconcilePersistedOrder(ctx context.Context, scope ExecutionScope, order *domain.Order) (domain.OrderStatus, error) {
	if order == nil {
		return "", fmt.Errorf("order_manager: persisted order is required")
	}
	if m.accountLocker == nil {
		return "", fmt.Errorf("order_manager: PostgreSQL execution account locker is required for restart reconciliation")
	}
	var status domain.OrderStatus
	err := m.accountLocker.WithExecutionAccountLock(ctx, scope.AccountID(), func() error {
		var innerErr error
		status, innerErr = m.reconcilePersistedOrderLocked(ctx, scope, order.ID)
		return innerErr
	})
	return status, err
}

func (m *OrderManager) reconcilePersistedOrderLocked(ctx context.Context, scope ExecutionScope, orderID uuid.UUID) (domain.OrderStatus, error) {
	if _, _, _, err := scopeOriginIDs(scope); err != nil {
		return "", fmt.Errorf("order_manager: reconcile execution scope: %w", err)
	}
	persisted, err := m.orderRepo.Get(ctx, orderID)
	if err != nil {
		return "", fmt.Errorf("order_manager: lock persisted order ownership: %w", err)
	}
	if err := validateOrderScope(persisted, scope); err != nil {
		return "", err
	}
	order := persisted
	decisionID, err := m.ensureAttachedOrderDecision(ctx, scope, order)
	if err != nil {
		return "", err
	}
	if order.Status == domain.OrderStatusFilled {
		if err := validateRecoveredFillEvidence(order); err != nil {
			return "", err
		}
		plan := recoveredOrderPlan(order)
		if err := m.handleFill(ctx, order, plan, scope, decisionID); err != nil {
			return "", err
		}
		return domain.OrderStatusFilled, nil
	}
	brokerOrderID := strings.TrimSpace(order.ExternalID)
	if brokerOrderID == "" {
		brokerOrderID = strings.TrimSpace(order.ClientOrderID)
	}
	if brokerOrderID == "" {
		if err := m.fenceEffect(ctx); err != nil {
			return "", err
		}
		order.Status = domain.OrderStatusRejected
		if err := m.orderRepo.Update(ctx, order); err != nil {
			return "", fmt.Errorf("order_manager: reject unsubmitted persisted order: %w", err)
		}
		return order.Status, nil
	}
	brokerResult, err := m.recoveryBrokerOrderStatus(ctx, brokerOrderID)
	status := brokerResult.Status
	if err != nil {
		if order.Status != domain.OrderStatusPending || strings.TrimSpace(order.ExternalID) != "" || !errors.Is(err, ErrBrokerOrderNotFound) {
			return "", fmt.Errorf("order_manager: reconcile broker status: %w", err)
		}
		if err := m.fenceEffect(ctx); err != nil {
			return "", err
		}
		externalID, submitErr := m.broker.SubmitOrder(ctx, order)
		if submitErr != nil {
			return "", fmt.Errorf("order_manager: resubmit persisted order: %w", submitErr)
		}
		if strings.TrimSpace(externalID) != brokerOrderID {
			return "", fmt.Errorf("order_manager: resubmitted client order identity mismatch")
		}
		brokerResult, err = m.recoveryBrokerOrderStatus(ctx, brokerOrderID)
		status = brokerResult.Status
		if err != nil {
			return "", fmt.Errorf("order_manager: verify resubmitted order: %w", err)
		}
	}
	if order.Status == domain.OrderStatusPending {
		if err := m.fenceEffect(ctx); err != nil {
			return "", err
		}
		submittedAt := m.currentTime()
		if err := m.orderRepo.Update(ctx, SanitizedSubmittedOrder(order, brokerOrderID, submittedAt)); err != nil {
			return "", fmt.Errorf("order_manager: recover broker submission evidence: %w", err)
		}
		order.ExternalID, order.Status, order.SubmittedAt = brokerOrderID, domain.OrderStatusSubmitted, &submittedAt
	}
	switch status {
	case domain.OrderStatusPending, domain.OrderStatusSubmitted, domain.OrderStatusPartial:
		if err := m.fenceEffect(ctx); err != nil {
			return "", err
		}
		if err := m.broker.CancelOrder(ctx, brokerOrderID); err != nil {
			return "", fmt.Errorf("order_manager: cancel recovered paper order: %w", err)
		}
		if err := m.fenceEffect(ctx); err != nil {
			return "", err
		}
		brokerResult, err = m.recoveryBrokerOrderStatus(ctx, brokerOrderID)
		status = brokerResult.Status
		if err != nil {
			return "", fmt.Errorf("order_manager: verify recovered paper cancellation: %w", err)
		}
	}
	order.Status = status
	if err := m.fenceEffect(ctx); err != nil {
		return "", err
	}
	switch status {
	case domain.OrderStatusFilled:
		order.FilledQuantity, order.FilledAvgPrice, order.FilledAt = brokerResult.FilledQuantity, cloneFloatPtr(brokerResult.FilledAvgPrice), cloneTimePtr(brokerResult.FilledAt)
		if err := validateRecoveredFillEvidence(order); err != nil {
			return "", err
		}
		plan := recoveredOrderPlan(order)
		if err := m.handleFill(ctx, order, plan, scope, decisionID); err != nil {
			return "", err
		}
	case domain.OrderStatusCancelled, domain.OrderStatusRejected:
		if err := m.orderRepo.Update(ctx, order); err != nil {
			return "", fmt.Errorf("order_manager: persist reconciled %s order: %w", status, err)
		}
	default:
		return "", fmt.Errorf("order_manager: recovered paper order remained nonterminal: %s", status)
	}
	return status, nil
}

func validateOrderScope(order *domain.Order, scope ExecutionScope) error {
	if order == nil {
		return fmt.Errorf("order_manager: persisted order is required")
	}
	originType, originID := scope.Origin()
	if order.AccountID != scope.AccountID() || order.Environment != scope.Environment() || order.OriginType != string(originType) || order.OriginID != originID {
		return fmt.Errorf("order_manager: persisted order ownership does not match execution scope")
	}
	if run, ok := scope.PipelineRun(); ok {
		if order.PipelineRunID == nil || order.PipelineRunTradeDate == nil || *order.PipelineRunID != run.ID || !order.PipelineRunTradeDate.Equal(run.TradeDate) {
			return fmt.Errorf("order_manager: persisted order run ownership does not match execution scope")
		}
	} else if order.PipelineRunID != nil || order.PipelineRunTradeDate != nil {
		return fmt.Errorf("order_manager: persisted order unexpectedly belongs to a pipeline run")
	}
	if order.CopyOriginRebalanceRunID != scope.CopyOriginRunID() {
		return fmt.Errorf("order_manager: persisted order copy run ownership does not match execution scope")
	}
	return nil
}

func (m *OrderManager) recoveryBrokerOrderStatus(ctx context.Context, brokerOrderID string) (BrokerOrderStatus, error) {
	if provider, ok := m.broker.(BrokerOrderStatusProvider); ok {
		return provider.GetOrderStatusResult(ctx, brokerOrderID)
	}
	status, err := m.broker.GetOrderStatus(ctx, brokerOrderID)
	if err != nil {
		return BrokerOrderStatus{}, err
	}
	if status == domain.OrderStatusFilled {
		return BrokerOrderStatus{}, fmt.Errorf("order_manager: broker fill evidence provider is required for recovery")
	}
	return BrokerOrderStatus{Status: status}, nil
}

func validateRecoveredFillEvidence(order *domain.Order) error {
	if order == nil || order.FilledAvgPrice == nil || *order.FilledAvgPrice <= 0 || order.FilledQuantity <= 0 || order.FilledAt == nil || order.FilledAt.IsZero() {
		return fmt.Errorf("order_manager: recovered filled order lacks authoritative fill evidence")
	}
	return nil
}

func recoveredOrderPlan(order *domain.Order) TradingPlan {
	plan := TradingPlan{Ticker: order.Ticker, MarketType: order.MarketType, Side: order.PredictionSide, EntryPrice: *order.FilledAvgPrice}
	if order.StopPrice != nil {
		plan.StopLoss = *order.StopPrice
	}
	return plan
}

// handleFill creates a Trade and creates or updates the Position.
func (m *OrderManager) handleFill(
	ctx context.Context,
	order *domain.Order,
	plan TradingPlan,
	scope ExecutionScope,
	decisionID uuid.UUID,
) error {
	_, _, _, err := scopeOriginIDs(scope)
	if err != nil {
		return fmt.Errorf("order_manager: fill execution scope: %w", err)
	}
	now := m.currentTime()
	if order.FilledQuantity <= 0 {
		order.FilledQuantity = order.Quantity
	}
	if order.FilledAt == nil || order.FilledAt.IsZero() {
		order.FilledAt = &now
	} else {
		now = order.FilledAt.UTC()
	}

	// Determine fill price.
	fillPrice := plan.EntryPrice
	if order.FilledAvgPrice != nil {
		fillPrice = *order.FilledAvgPrice
	}
	if fillPrice <= 0 {
		return fmt.Errorf("order_manager: fill price must be positive")
	}

	marketType := order.MarketType.Normalize()
	originType, originID := scope.Origin()
	if m.financialRepo == nil {
		if err := m.orderRepo.Update(ctx, order); err != nil {
			return fmt.Errorf("order_manager: update filled order: %w", err)
		}
		m.recordOrderMetric(order.Side, order.Status)
	}
	var position *domain.Position
	if m.financialRepo != nil {
		trade := &domain.Trade{ID: uuid.New(), AccountID: scope.AccountID(), Environment: scope.Environment(), OriginType: string(originType), OriginID: originID, OrderID: &order.ID, Ticker: order.Ticker, Side: order.Side, Quantity: order.FilledQuantity, Price: fillPrice, ExecutedAt: now}
		var stopLoss, takeProfit *float64
		if plan.StopLoss > 0 {
			stopLoss = &plan.StopLoss
		}
		if plan.TakeProfit > 0 {
			takeProfit = &plan.TakeProfit
		}
		result, err := m.financialRepo.ApplyOrderFill(ctx, repository.OrderFillInput{IdempotencyKey: "paper_fill:v1:" + order.ID.String() + ":full", Order: order, FillIntent: repository.OrderFillIntent{Side: order.Side, Quantity: order.FilledQuantity, ExecutionPrice: fillPrice}, Now: now, StopLoss: stopLoss, TakeProfit: takeProfit, Trade: trade})
		if err != nil {
			return fmt.Errorf("order_manager: persist fill: %w", err)
		}
		if err := validateOrderFillResult(result, order, scope); err != nil {
			return fmt.Errorf("order_manager: invalid persisted fill result: %w", err)
		}
		if !result.Replayed {
			m.recordOrderMetric(order.Side, order.Status)
		}
		position := result.Position
		if position == nil && result.PositionID != nil {
			position = &domain.Position{ID: *result.PositionID}
		}
		if err := m.recordTradeDecisionReplay(ctx, scope, decisionID, domain.ReplayEventTypeFillObserved, map[string]any{"order_id": order.ID, "trade_id": result.TradeID, "price": fillPrice, "quantity": order.FilledQuantity, "prediction_side": order.PredictionSide}); err != nil {
			return err
		}
		if position != nil {
			if err := m.recordTradeDecisionReplay(ctx, scope, decisionID, domain.ReplayEventTypePositionUpdated, map[string]any{"position_id": position.ID, "ticker": position.Ticker, "quantity": position.Quantity, "realized_pnl": position.RealizedPnL, "closed_at": position.ClosedAt}); err != nil {
				return err
			}
		}
		if !result.Replayed {
			if auditErr := m.audit(ctx, "order_filled", "order", &order.ID, map[string]any{"fill_price": fillPrice, "quantity": order.FilledQuantity, "trade_id": result.TradeID, "position_id": result.PositionID}); auditErr != nil {
				m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
			}
		}
		if !result.Replayed {
			m.emitOrderEvent(ctx, OrderEventFilled, order, scope)
		}
		return nil
	}
	if isPredictionMarket(marketType) && order.Side == domain.OrderSideSell {
		positionTicker := polymarketPositionTicker(order.Ticker, order.PredictionSide)
		positions, err := m.positionsByScope(ctx, scope, repository.PositionFilter{
			Ticker: positionTicker,
			Side:   domain.PositionSideLong,
		})
		if err != nil {
			return fmt.Errorf("order_manager: get prediction exit position for %s: %w", positionTicker, err)
		}

		for i := range positions {
			if positions[i].ClosedAt == nil && positions[i].Quantity > 0 {
				position = &positions[i]
				break
			}
		}
		if position == nil {
			return fmt.Errorf("order_manager: prediction sell fill has no open position for %s", positionTicker)
		}

		closedQuantity := math.Min(position.Quantity, order.FilledQuantity)
		currentPrice := fillPrice
		position.CurrentPrice = &currentPrice
		position.RealizedPnL += realizedPnL(position.Side, position.AvgEntry, fillPrice, closedQuantity)
		if position.Quantity > order.FilledQuantity {
			position.Quantity -= order.FilledQuantity
		} else {
			position.Quantity = 0
			closedAt := now
			position.ClosedAt = &closedAt
		}

		if err := m.positionRepo.Update(ctx, position); err != nil {
			return fmt.Errorf("order_manager: update prediction position: %w", err)
		}
	} else {
		positionSide := domain.PositionSideLong

		positionTicker := order.Ticker
		if marketType == domain.MarketTypePolymarket || marketType == domain.MarketTypeKalshi {
			positionTicker = polymarketPositionTicker(order.Ticker, order.PredictionSide)
		}

		position = &domain.Position{
			ID:          uuid.New(),
			AccountID:   scope.AccountID(),
			Environment: scope.Environment(),
			OriginType:  string(originType),
			OriginID:    originID,
			MarketType:  marketType,
			Ticker:      positionTicker,
			Side:        positionSide,
			Quantity:    order.FilledQuantity,
			AvgEntry:    fillPrice,
			OpenedAt:    now,
		}
		position.StrategyID = scope.LegacyStrategyID()

		if plan.StopLoss > 0 {
			position.StopLoss = &plan.StopLoss
		}

		if plan.TakeProfit > 0 {
			position.TakeProfit = &plan.TakeProfit
		}

		if err := m.positionRepo.Create(ctx, position); err != nil {
			return fmt.Errorf("order_manager: create position: %w", err)
		}
	}

	trade := &domain.Trade{
		ID:          uuid.New(),
		AccountID:   scope.AccountID(),
		Environment: scope.Environment(),
		OriginType:  string(originType),
		OriginID:    originID,
		OrderID:     &order.ID,
		PositionID:  &position.ID,
		Ticker:      order.Ticker,
		Side:        order.Side,
		Quantity:    order.FilledQuantity,
		Price:       fillPrice,
		ExecutedAt:  now,
		CreatedAt:   now,
	}

	if err := m.tradeRepo.Create(ctx, trade); err != nil {
		// Audit the incomplete fill so it can be reconciled later.
		if auditErr := m.audit(ctx, "order_fill_incomplete", "order", &order.ID, map[string]any{
			"fill_price":  fillPrice,
			"quantity":    order.FilledQuantity,
			"position_id": position.ID,
			"error":       err.Error(),
		}); auditErr != nil {
			m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
		}

		return fmt.Errorf("order_manager: create trade: %w", err)
	}

	if err := m.recordTradeDecisionReplay(ctx, scope, decisionID, domain.ReplayEventTypeFillObserved, map[string]any{
		"order_id": order.ID, "trade_id": trade.ID, "price": fillPrice,
		"quantity": order.FilledQuantity, "prediction_side": order.PredictionSide,
	}); err != nil {
		return err
	}
	if err := m.recordTradeDecisionReplay(ctx, scope, decisionID, domain.ReplayEventTypePositionUpdated, map[string]any{
		"position_id": position.ID, "ticker": position.Ticker, "quantity": position.Quantity,
		"realized_pnl": position.RealizedPnL, "closed_at": position.ClosedAt,
	}); err != nil {
		return err
	}

	if auditErr := m.audit(ctx, "order_filled", "order", &order.ID, map[string]any{
		"fill_price":  fillPrice,
		"quantity":    order.FilledQuantity,
		"trade_id":    trade.ID,
		"position_id": position.ID,
	}); auditErr != nil {
		m.logger.ErrorContext(ctx, "audit log failed", "error", auditErr)
	}

	m.emitOrderEvent(ctx, OrderEventFilled, order, scope)

	return nil
}

func validateOrderFillResult(result repository.OrderFillResult, order *domain.Order, scope ExecutionScope) error {
	if order == nil || result.OrderID != order.ID || result.TradeID == uuid.Nil || result.Trade == nil {
		return fmt.Errorf("order and trade identities are incomplete or mismatched")
	}
	if order.Side == domain.OrderSideBuy && result.PositionID == nil {
		return fmt.Errorf("opening fill has no position identity")
	}
	originType, originID := scope.Origin()
	trade := result.Trade
	if trade.ID != result.TradeID || trade.AccountID != scope.AccountID() || trade.Environment != scope.Environment() ||
		trade.OriginType != string(originType) || trade.OriginID != originID || trade.OrderID == nil || *trade.OrderID != order.ID ||
		trade.Ticker != order.Ticker || trade.Side != order.Side {
		return fmt.Errorf("trade identity or canonical scope is inconsistent")
	}
	if result.PositionID == nil {
		if result.Position != nil || trade.PositionID != nil {
			return fmt.Errorf("position identity is inconsistent")
		}
		return nil
	}
	if result.Position == nil {
		return fmt.Errorf("persisted position is required")
	}
	position := result.Position
	if result.PositionID == nil || *result.PositionID != position.ID || position.ID == uuid.Nil ||
		position.AccountID != scope.AccountID() || position.Environment != scope.Environment() ||
		position.OriginType != string(originType) || position.OriginID != originID ||
		position.Ticker != fillPositionTicker(order) || trade.PositionID == nil || *trade.PositionID != position.ID {
		return fmt.Errorf("position identity or canonical scope is inconsistent")
	}
	return nil
}

func fillPositionTicker(order *domain.Order) string {
	if isPredictionMarket(order.MarketType) {
		return polymarketPositionTicker(order.Ticker, order.PredictionSide)
	}
	return order.Ticker
}

// HandleFillForTest exposes handleFill for focused unit coverage.
func (m *OrderManager) HandleFillForTest(ctx context.Context, order *domain.Order, plan TradingPlan, scope ExecutionScope, decisionID uuid.UUID) error {
	return m.handleFill(ctx, order, plan, scope, decisionID)
}

// signalToSide maps a pipeline signal to an order side.
func (m *OrderManager) signalToSide(signal domain.PipelineSignal) domain.OrderSide {
	switch signal {
	case domain.PipelineSignalBuy:
		return domain.OrderSideBuy
	default:
		return domain.OrderSideSell
	}
}

// entryTypeToOrderType converts a trading plan entry type to an order type.
func (m *OrderManager) entryTypeToOrderType(entryType string) domain.OrderType {
	switch entryType {
	case "limit":
		return domain.OrderTypeLimit
	case "stop":
		return domain.OrderTypeStop
	case "stop_limit":
		return domain.OrderTypeStopLimit
	default:
		return domain.OrderTypeMarket
	}
}

// audit is a helper that creates an AuditLogEntry.
func (m *OrderManager) audit(
	ctx context.Context,
	eventType, entityType string,
	entityID *uuid.UUID,
	details map[string]any,
) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}

	entry := &domain.AuditLogEntry{
		ID:         uuid.New(),
		EventType:  eventType,
		EntityType: entityType,
		EntityID:   entityID,
		Actor:      "order_manager",
		Details:    raw,
		CreatedAt:  m.currentTime(),
	}

	return m.auditLogRepo.Create(ctx, entry)
}

// emitOrderEvent persists an AgentEvent for order lifecycle transitions.
// If the agentEventRepo is nil or persistence fails, the error is logged
// but does not propagate — order flow must not break on event emission.
func (m *OrderManager) emitOrderEvent(
	ctx context.Context,
	eventKind string,
	order *domain.Order,
	scope ExecutionScope,
) {
	if m.agentEventRepo == nil {
		return
	}

	meta, err := json.Marshal(map[string]any{
		"ticker":   order.Ticker,
		"side":     order.Side,
		"quantity": order.Quantity,
		"price":    order.LimitPrice,
		"broker":   order.Broker,
		"order_id": order.ID,
	})
	if err != nil {
		m.logger.ErrorContext(ctx, "order_manager: marshal event metadata", "error", err)
		return
	}

	title := fmt.Sprintf("Order %s: %s %.4g %s", eventKind, order.Side, order.Quantity, order.Ticker)

	event := &domain.AgentEvent{
		ID:          uuid.New(),
		AccountID:   scope.AccountID(),
		Environment: scope.Environment(),
		AgentRole:   domain.AgentRoleTrader,
		EventKind:   eventKind,
		Title:       title,
		Tags:        []string{"order", eventKind},
		Metadata:    meta,
		CreatedAt:   m.currentTime(),
	}
	originType, originID := scope.Origin()
	event.OriginType = string(originType)
	event.OriginID = originID
	if originType == ledger.ExecutionOriginStrategyVersion {
		event.StrategyID = scope.LegacyStrategyID()
	}
	if run, ok := scope.PipelineRun(); ok {
		event.PipelineRunID = &run.ID
		tradeDate := run.TradeDate
		event.PipelineRunTradeDate = &tradeDate
	}

	if err := m.agentEventRepo.Create(ctx, event); err != nil {
		m.logger.ErrorContext(ctx, "order_manager: emit order event", "error", err, "kind", eventKind)
	}
}

func (m *OrderManager) recordOrderMetric(side domain.OrderSide, status domain.OrderStatus) {
	if m == nil || m.metrics == nil {
		return
	}
	m.metrics.RecordOrder(m.brokerName, string(side), string(status))
}

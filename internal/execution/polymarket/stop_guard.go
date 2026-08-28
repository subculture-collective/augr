package polymarket

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type StopGuardConfig struct {
	ExecutionAccount domain.ExecutionAccountBinding
	Broker           templateSender
	ExitRepo         repository.AtomicPredictionExitRepository
	EconomicWriter   execution.AcceptedOrderFillWriter
	Logger           *slog.Logger
	Metrics          StopGuardMetrics
}

type StopGuardMetrics interface {
	IncTriggered(slug string)
	IncSendError(slug string)
	ObserveTickToFireSeconds(slug string, seconds float64)
	SetActive(count float64)
}

type Position struct {
	AccountID    uuid.UUID
	Environment  domain.AccountEnvironment
	OriginType   string
	OriginID     string
	ID           string
	Slug         string
	Side         string
	OutcomeSide  string
	EntryPx      float64
	Size         float64
	StopPx       float64
	TakeProfitPx float64
	Scope        execution.ExecutionScope
}

type templateSender interface {
	PrepareTemplate(order *domain.Order) (*OrderTemplate, error)
	SendTemplate(ctx context.Context, tmpl *OrderTemplate) (*CreateOrderResponse, error)
}

type stopOrderLookup interface {
	GetOrderStatusByClientOrderIDResult(context.Context, string) (string, execution.BrokerOrderStatus, error)
}

type stopOrderTerminalizer interface {
	FinalizePredictionExit(context.Context, uuid.UUID, domain.AccountEnvironment, uuid.UUID, uuid.UUID, domain.OrderStatus, string, time.Time) error
}

type guardState int32

const (
	guardArmed guardState = iota
	guardFiring
	guardFired
)

type guardEntry struct {
	positionID string
	slug       string
	outcome    string
	stopPx     float64
	takePx     float64
	long       bool
	template   *OrderTemplate
	order      *domain.Order
	state      atomic.Int32
	receivedAt time.Time
	scope      execution.ExecutionScope
	claimed    atomic.Bool
	adopted    atomic.Bool
}

type StopGuard struct {
	executionAccount domain.ExecutionAccountBinding
	broker           templateSender
	exitRepo         repository.AtomicPredictionExitRepository
	economicWriter   execution.AcceptedOrderFillWriter
	logger           *slog.Logger
	metrics          StopGuardMetrics

	mu     sync.RWMutex
	bySlug map[string][]*guardEntry
	byID   map[string]*guardEntry
	count  atomic.Int32
}

type stopAccountLockHeldKey struct{}

func NewStopGuard(cfg StopGuardConfig) (*StopGuard, error) {
	if cfg.Broker == nil {
		return nil, errors.New("polymarket: stop guard broker is required")
	}
	if err := cfg.ExecutionAccount.Validate(); err != nil {
		return nil, fmt.Errorf("polymarket: stop guard execution account: %w", err)
	}
	if cfg.ExitRepo == nil {
		cfg.ExitRepo, _ = cfg.Broker.(repository.AtomicPredictionExitRepository)
	}
	if cfg.ExitRepo == nil {
		return nil, errors.New("polymarket: durable stop exit repository is required")
	}
	if _, ok := cfg.ExitRepo.(repository.ExecutionAccountLocker); !ok {
		return nil, errors.New("polymarket: stop exit repository must provide an execution account lock")
	}
	return &StopGuard{
		executionAccount: cfg.ExecutionAccount,
		broker:           cfg.Broker,
		exitRepo:         cfg.ExitRepo,
		economicWriter:   cfg.EconomicWriter,
		logger:           cfg.Logger,
		metrics:          cfg.Metrics,
		bySlug:           make(map[string][]*guardEntry),
		byID:             make(map[string]*guardEntry),
	}, nil
}

func (g *StopGuard) RegisterEntry(pos Position) error {
	_, err := g.registerEntry(pos, guardArmed, true)
	return err
}

func (g *StopGuard) registerEntry(pos Position, initialState guardState, activate bool) (*guardEntry, error) {
	if g == nil {
		return nil, errors.New("polymarket: stop guard is nil")
	}
	if err := g.executionAccount.Validate(); err != nil {
		return nil, fmt.Errorf("polymarket: stop guard execution account: %w", err)
	}
	if pos.AccountID != g.executionAccount.AccountID() || pos.Environment != g.executionAccount.Environment() {
		return nil, errors.New("polymarket: stop guard position belongs to a foreign execution account")
	}
	if strings.TrimSpace(pos.OriginType) == "" || strings.TrimSpace(pos.OriginID) == "" {
		return nil, errors.New("polymarket: stop guard position origin is required")
	}
	positionID := strings.TrimSpace(pos.ID)
	if positionID == "" {
		return nil, errors.New("polymarket: position id is required")
	}
	slug := strings.TrimSpace(pos.Slug)
	if slug == "" {
		return nil, errors.New("polymarket: slug is required")
	}
	if pos.Size <= 0 {
		return nil, errors.New("polymarket: size must be greater than zero")
	}
	if pos.StopPx <= 0 && pos.TakeProfitPx <= 0 {
		return nil, errors.New("polymarket: stop price or take-profit price is required")
	}
	long := strings.EqualFold(strings.TrimSpace(pos.Side), "BUY")
	short := strings.EqualFold(strings.TrimSpace(pos.Side), "SELL")
	if !long && !short {
		return nil, fmt.Errorf("polymarket: unsupported side %q", pos.Side)
	}
	outcome := strings.ToUpper(strings.TrimSpace(pos.OutcomeSide))
	if outcome == "" {
		outcome = "YES"
	}
	if outcome != "YES" && outcome != "NO" {
		return nil, fmt.Errorf("polymarket: unsupported outcome side %q", pos.OutcomeSide)
	}
	intent := "ORDER_INTENT_SELL_LONG"
	switch {
	case outcome == "NO" && long:
		intent = "ORDER_INTENT_SELL_SHORT"
	case outcome == "NO" && short:
		intent = "ORDER_INTENT_BUY_SHORT"
	case short:
		intent = "ORDER_INTENT_BUY_SHORT"
	}
	side := domain.OrderSideBuy
	if long {
		side = domain.OrderSideSell
	}
	order := &domain.Order{ID: uuid.New(), AccountID: pos.AccountID, Environment: pos.Environment, OriginType: pos.OriginType, OriginID: pos.OriginID, Ticker: slug, MarketType: domain.MarketTypePolymarket, Side: side, OrderType: domain.OrderTypeMarket, Quantity: pos.Size, Status: domain.OrderStatusPending, Broker: "polymarket", PredictionSide: outcome, PolymarketIntent: intent, CreatedAt: time.Now().UTC()}
	positionIntent := domain.PositionIntentBuyToClose
	if order.Side == domain.OrderSideSell {
		positionIntent = domain.PositionIntentSellToClose
	}
	order.PositionIntent = &positionIntent
	if durablePositionID, parseErr := uuid.Parse(positionID); parseErr == nil {
		order.ClosePositionIDs = []uuid.UUID{durablePositionID}
	}
	order.ClientOrderID = "augr-polymarket-stop-" + order.ID.String()
	tmpl, err := g.broker.PrepareTemplate(order)
	if err != nil {
		return nil, err
	}
	entry := &guardEntry{positionID: positionID, slug: slug, outcome: outcome, stopPx: pos.StopPx, takePx: pos.TakeProfitPx, long: long, template: tmpl, order: order, receivedAt: time.Now(), scope: pos.Scope}
	entry.state.Store(int32(initialState))
	if activate {
		g.activateEntry(entry)
	}
	return entry, nil
}

func (g *StopGuard) activateEntry(entry *guardEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	positionID, slug := entry.positionID, entry.slug
	if previous, exists := g.byID[positionID]; exists {
		previous.state.Store(int32(guardFired))
		oldEntries := g.bySlug[previous.slug]
		for i := range oldEntries {
			if oldEntries[i] == previous {
				oldEntries = append(oldEntries[:i], oldEntries[i+1:]...)
				break
			}
		}
		if len(oldEntries) == 0 {
			delete(g.bySlug, previous.slug)
		} else {
			g.bySlug[previous.slug] = oldEntries
		}
		g.byID[positionID] = entry
		g.bySlug[slug] = append(g.bySlug[slug], entry)
		return
	}
	g.byID[positionID] = entry
	g.bySlug[slug] = append(g.bySlug[slug], entry)
	g.count.Add(1)
	if g.metrics != nil {
		g.metrics.SetActive(float64(g.count.Load()))
	}
}

func (g *StopGuard) RegisterPosition(pos domain.Position) error {
	return g.RegisterPositionContext(context.Background(), pos)
}

func (g *StopGuard) RegisterPositionContext(ctx context.Context, pos domain.Position) error {
	if g == nil {
		return errors.New("polymarket: stop guard is nil")
	}
	if pos.MarketType != domain.MarketTypePolymarket {
		return errors.New("polymarket: stop guard requires exact polymarket market type")
	}
	positionID := pos.ID.String()
	if positionID == "" || pos.ID == uuid.Nil {
		return errors.New("polymarket: position id is required")
	}
	slug, outcome, err := polymarketPositionParts(pos.Ticker)
	if err != nil {
		return err
	}
	resolver, ok := g.economicWriter.(execution.PositionExecutionScopeResolver)
	if !ok {
		return errors.New("polymarket: stop guard economic writer cannot resolve position execution scope")
	}
	scope, err := resolver.ResolvePositionExecutionScope(ctx, pos)
	if err != nil {
		return fmt.Errorf("polymarket: resolve stop position execution scope: %w", err)
	}
	entry := Position{AccountID: pos.AccountID, Environment: pos.Environment, OriginType: pos.OriginType, OriginID: pos.OriginID, ID: positionID, Slug: slug, OutcomeSide: outcome, EntryPx: pos.AvgEntry, Size: pos.Quantity, Scope: scope}
	switch pos.Side {
	case domain.PositionSideLong:
		entry.Side = "BUY"
	case domain.PositionSideShort:
		entry.Side = "SELL"
	default:
		return fmt.Errorf("polymarket: unsupported position side %q", pos.Side)
	}
	if pos.StopLoss != nil {
		entry.StopPx = *pos.StopLoss
	}
	if pos.TakeProfit != nil {
		entry.TakeProfitPx = *pos.TakeProfit
	}
	guard, err := g.registerEntry(entry, guardFiring, false)
	if err != nil {
		return err
	}
	lookup, ok := g.exitRepo.(repository.PredictionExitReservationLookup)
	if !ok {
		guard.state.Store(int32(guardArmed))
		g.activateEntry(guard)
		return nil
	}
	order, err := lookup.GetPredictionExitOrderByPosition(ctx, pos.AccountID, pos.Environment, pos.ID)
	if errors.Is(err, repository.ErrNotFound) {
		guard.state.Store(int32(guardArmed))
		g.activateEntry(guard)
		return nil
	}
	if err != nil {
		return fmt.Errorf("polymarket: load reserved stop order: %w", err)
	}
	if err := validateRecoveredStopReservation(order, pos, entry); err != nil {
		return err
	}
	if order.Status != domain.OrderStatusPending && order.Status != domain.OrderStatusSubmitted && order.Status != domain.OrderStatusPartial {
		return fmt.Errorf("polymarket: reserved stop order has non-recoverable status %s", order.Status)
	}
	tmpl, err := g.broker.PrepareTemplate(order)
	if err != nil {
		return err
	}
	guard.order, guard.template = order, tmpl
	guard.claimed.Store(true)
	guard.state.Store(int32(guardArmed))
	locker := g.exitRepo.(repository.ExecutionAccountLocker)
	recovered := false
	if err := locker.WithExecutionAccountLock(ctx, pos.AccountID, func() error {
		recovered = g.recoverClaimedExit(context.WithValue(ctx, stopAccountLockHeldKey{}, true), guard, pos.ID)
		if !recovered {
			return errors.New("polymarket: claimed stop exit bootstrap reconciliation failed")
		}
		return nil
	}); err != nil {
		return err
	}
	if guardState(guard.state.Load()) != guardFired {
		g.activateEntry(guard)
	}
	return nil
}

func validateRecoveredStopReservation(order *domain.Order, position domain.Position, expected Position) error {
	if order == nil || order.ID == uuid.Nil || order.AccountID == uuid.Nil || !order.Environment.IsValid() || strings.TrimSpace(order.OriginType) == "" || strings.TrimSpace(order.OriginID) == "" || strings.TrimSpace(order.ClientOrderID) == "" || order.PositionIntent == nil {
		return errors.New("polymarket: recovered stop reservation lacks complete order identity")
	}
	wantSide, wantIntent := domain.OrderSideSell, domain.PositionIntentSellToClose
	wantPolymarketIntent := "ORDER_INTENT_SELL_LONG"
	if position.Side == domain.PositionSideShort {
		wantSide, wantIntent = domain.OrderSideBuy, domain.PositionIntentBuyToClose
		wantPolymarketIntent = "ORDER_INTENT_BUY_SHORT"
	} else if strings.EqualFold(expected.OutcomeSide, "NO") {
		wantPolymarketIntent = "ORDER_INTENT_SELL_SHORT"
	}
	wantTicker := strings.TrimSpace(expected.Slug)
	remaining := order.Quantity - order.FilledQuantity
	if order.FilledQuantity < 0 || !canonicalPolymarketQuantityPositive(remaining) || !canonicalPolymarketQuantityEqual(remaining, position.Quantity) || order.AccountID != position.AccountID || order.Environment != position.Environment || order.OriginType != position.OriginType || order.OriginID != position.OriginID || order.MarketType != domain.MarketTypePolymarket || position.MarketType != domain.MarketTypePolymarket || order.OrderType != domain.OrderTypeMarket || strings.TrimSpace(order.Ticker) != wantTicker || !strings.EqualFold(strings.TrimSpace(order.PredictionSide), expected.OutcomeSide) || strings.TrimSpace(order.PolymarketIntent) != wantPolymarketIntent || order.Side != wantSide || *order.PositionIntent != wantIntent || !canonicalPolymarketQuantityPositive(position.Quantity) || position.ClosedAt != nil {
		return errors.New("polymarket: recovered stop reservation does not close the exact persisted position")
	}
	return nil
}

func canonicalPolymarketQuantityEqual(left, right float64) bool {
	return isFinitePolymarketQuantity(left) && isFinitePolymarketQuantity(right) && math.Round(left*1e8) == math.Round(right*1e8)
}

func canonicalPolymarketQuantityPositive(quantity float64) bool {
	return isFinitePolymarketQuantity(quantity) && math.Round(quantity*1e8) > 0
}

func isFinitePolymarketQuantity(quantity float64) bool {
	return !math.IsNaN(quantity) && !math.IsInf(quantity, 0)
}

func (g *StopGuard) arm(positionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if entry := g.byID[positionID]; entry != nil {
		entry.state.Store(int32(guardArmed))
	}
}

func (g *StopGuard) Cancel(positionID string) {
	if g == nil {
		return
	}
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.byID[positionID]
	if !ok {
		return
	}
	entry.state.Store(int32(guardFired))
	delete(g.byID, positionID)
	entries := g.bySlug[entry.slug]
	for i := range entries {
		if entries[i] == entry {
			entries[i] = entries[len(entries)-1]
			entries = entries[:len(entries)-1]
			break
		}
	}
	if len(entries) == 0 {
		delete(g.bySlug, entry.slug)
	} else {
		g.bySlug[entry.slug] = entries
	}
	g.count.Add(-1)
	if g.metrics != nil {
		g.metrics.SetActive(float64(g.count.Load()))
	}
}

func (g *StopGuard) Active() int {
	if g == nil {
		return 0
	}
	return int(g.count.Load())
}

func (g *StopGuard) Reconcile(ctx context.Context) error {
	if g == nil || g.exitRepo == nil {
		return errors.New("polymarket: durable stop exit repository is required")
	}
	return g.exitRepo.ReconcilePredictionExitReservations(ctx, g.executionAccount.AccountID(), g.executionAccount.Environment())
}

func (g *StopGuard) OnTick(ctx context.Context, t marketdata.Tick) {
	if g == nil {
		return
	}
	g.mu.RLock()
	entries := append([]*guardEntry(nil), g.bySlug[t.Slug]...)
	g.mu.RUnlock()
	for _, entry := range entries {
		if !tickMatchesOutcome(t, entry.outcome) {
			continue
		}
		if entry == nil || !entry.state.CompareAndSwap(int32(guardArmed), int32(guardFiring)) {
			continue
		}
		positionID, parseErr := uuid.Parse(entry.positionID)
		if parseErr != nil {
			positionID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(entry.positionID))
		}
		if entry.claimed.Load() {
			locker, ok := g.exitRepo.(repository.ExecutionAccountLocker)
			recovered := false
			if ok {
				lockErr := locker.WithExecutionAccountLock(ctx, entry.order.AccountID, func() error {
					recovered = g.recoverClaimedExit(context.WithValue(ctx, stopAccountLockHeldKey{}, true), entry, positionID)
					return nil
				})
				if lockErr != nil {
					recovered = false
				}
			}
			if recovered {
				continue
			}
			entry.state.Store(int32(guardArmed))
			continue
		}
		crossed := (entry.long && ((entry.stopPx > 0 && t.Price <= entry.stopPx) || (entry.takePx > 0 && t.Price >= entry.takePx))) ||
			(!entry.long && ((entry.stopPx > 0 && t.Price >= entry.stopPx) || (entry.takePx > 0 && t.Price <= entry.takePx)))
		if !crossed {
			entry.state.Store(int32(guardArmed))
			continue
		}
		if g.metrics != nil {
			g.metrics.IncTriggered(entry.slug)
		}
		if g.metrics != nil {
			g.metrics.ObserveTickToFireSeconds(entry.slug, time.Since(t.ReceivedAt).Seconds())
		}
		locker, ok := g.exitRepo.(repository.ExecutionAccountLocker)
		if !ok {
			entry.state.Store(int32(guardArmed))
			continue
		}
		if err := locker.WithExecutionAccountLock(ctx, entry.order.AccountID, func() error {
			lockedCtx := context.WithValue(ctx, stopAccountLockHeldKey{}, true)
			if err := g.claimStopExitLocked(lockedCtx, entry, positionID); err != nil {
				return err
			}
			g.submitReservedStopLocked(lockedCtx, entry, positionID)
			return nil
		}); err != nil {
			if g.logger != nil {
				g.logger.Error("polymarket stop guard durable claim failed", "slug", entry.slug, "position_id", entry.positionID, "err", err)
			}
			entry.state.Store(int32(guardArmed))
		}
	}
}

func (g *StopGuard) claimStopExitLocked(ctx context.Context, entry *guardEntry, positionID uuid.UUID) error {
	entry.adopted.Store(false)
	err := g.exitRepo.CreatePredictionExitOrderAndReserve(ctx, entry.order.AccountID, entry.order.Environment, entry.order.OriginType, entry.order.OriginID, positionID, entry.order)
	if err != nil {
		resolved, resolveErr := g.resolveStopReservation(ctx, entry, positionID)
		if resolveErr != nil {
			return errors.Join(err, resolveErr)
		}
		entry.order = resolved
		entry.adopted.Store(true)
	}
	entry.claimed.Store(true)
	return nil
}

func (g *StopGuard) resolveStopReservation(ctx context.Context, entry *guardEntry, positionID uuid.UUID) (*domain.Order, error) {
	lookup, ok := g.exitRepo.(repository.PredictionExitReservationLookup)
	if !ok {
		return nil, errors.New("polymarket: prediction exit reservation lookup is required")
	}
	order, err := lookup.GetPredictionExitOrderByPosition(ctx, entry.order.AccountID, entry.order.Environment, positionID)
	if err != nil {
		return nil, fmt.Errorf("polymarket: resolve prediction exit reservation: %w", err)
	}
	if order.AccountID != entry.order.AccountID || order.Environment != entry.order.Environment || order.OriginType != entry.order.OriginType || order.OriginID != entry.order.OriginID ||
		!canonicalPolymarketQuantityEqual(order.Quantity-order.FilledQuantity, entry.order.Quantity-entry.order.FilledQuantity) || order.MarketType.Normalize() != domain.MarketTypePolymarket || order.OrderType != entry.order.OrderType ||
		order.Ticker != entry.order.Ticker || order.PredictionSide != entry.order.PredictionSide || order.PolymarketIntent != entry.order.PolymarketIntent || order.Side != entry.order.Side || order.PositionIntent == nil || entry.order.PositionIntent == nil || *order.PositionIntent != *entry.order.PositionIntent {
		return nil, errors.New("polymarket: resolved prediction exit reservation does not match claimed order")
	}
	return order, nil
}

func (g *StopGuard) submitReservedStopLocked(ctx context.Context, entry *guardEntry, positionID uuid.UUID) {
	if entry.adopted.Swap(false) {
		if !g.recoverClaimedExit(ctx, entry, positionID) {
			entry.state.Store(int32(guardArmed))
		}
		return
	}
	freshTemplate, err := g.broker.PrepareTemplate(entry.order)
	if err != nil {
		entry.state.Store(int32(guardArmed))
		return
	}
	entry.template = freshTemplate
	response, err := g.broker.SendTemplate(ctx, freshTemplate)
	if err != nil {
		if g.metrics != nil {
			g.metrics.IncSendError(entry.slug)
		}
		if g.logger != nil {
			g.logger.Error("polymarket stop guard send failed", "slug", entry.slug, "position_id", entry.positionID, "err", err)
		}
		if execution.IsDefinitiveBrokerRejection(err) {
			if !g.finalizeTerminalExit(ctx, entry, positionID, domain.OrderStatusRejected, "") {
				entry.state.Store(int32(guardArmed))
			}
			return
		}
		lookup, ok := g.broker.(stopOrderLookup)
		if !ok {
			entry.state.Store(int32(guardArmed))
			return
		}
		externalID, result, lookupErr := lookup.GetOrderStatusByClientOrderIDResult(ctx, entry.order.ClientOrderID)
		if lookupErr != nil {
			entry.state.Store(int32(guardArmed))
			return
		}
		if result.Status == domain.OrderStatusCancelled || result.Status == domain.OrderStatusRejected {
			fillResult := result
			fillResult.Status = domain.OrderStatusPartial
			if result.FilledQuantity > entry.order.FilledQuantity && !g.persistRecoveredExitFill(ctx, entry, externalID, fillResult) {
				entry.state.Store(int32(guardArmed))
				return
			}
			if !g.finalizeTerminalExit(ctx, entry, positionID, result.Status, externalID) {
				entry.state.Store(int32(guardArmed))
			}
			return
		}
		if result.Status != domain.OrderStatusPending && result.Status != domain.OrderStatusSubmitted && result.Status != domain.OrderStatusPartial && result.Status != domain.OrderStatusFilled {
			entry.state.Store(int32(guardArmed))
			return
		}
		if result.Status == domain.OrderStatusPartial || result.Status == domain.OrderStatusFilled {
			if result.Status == domain.OrderStatusFilled && !canonicalPolymarketQuantityEqual(result.FilledQuantity, entry.order.Quantity) {
				entry.state.Store(int32(guardArmed))
				return
			}
			if !g.persistRecoveredExitFill(ctx, entry, externalID, result) {
				entry.state.Store(int32(guardArmed))
				return
			}
			if result.Status == domain.OrderStatusFilled {
				g.Cancel(entry.positionID)
				return
			}
			entry.state.Store(int32(guardArmed))
			return
		}
		response = &CreateOrderResponse{ID: externalID}
	}
	if response == nil || strings.TrimSpace(response.ID) == "" {
		entry.state.Store(int32(guardArmed))
		return
	}
	submittedAt := time.Now().UTC()
	if err := g.exitRepo.MarkPredictionExitSubmitted(ctx, entry.order.AccountID, entry.order.ID, strings.TrimSpace(response.ID), submittedAt); err != nil {
		if g.logger != nil {
			g.logger.Error("polymarket stop guard submission persistence failed", "slug", entry.slug, "position_id", entry.positionID, "err", err)
		}
		entry.state.Store(int32(guardArmed))
		return
	}
	entry.order.ExternalID, entry.order.Status, entry.order.SubmittedAt = strings.TrimSpace(response.ID), domain.OrderStatusSubmitted, &submittedAt
	entry.state.Store(int32(guardArmed))
}

func (g *StopGuard) recoverClaimedExit(ctx context.Context, entry *guardEntry, positionID uuid.UUID) bool {
	lookup, ok := g.broker.(stopOrderLookup)
	if !ok {
		return false
	}
	externalID, result, err := lookup.GetOrderStatusByClientOrderIDResult(ctx, entry.order.ClientOrderID)
	if err != nil {
		if errors.Is(err, execution.ErrBrokerOrderNotFound) {
			tmpl, prepareErr := g.broker.PrepareTemplate(entry.order)
			if prepareErr != nil {
				return false
			}
			response, sendErr := g.broker.SendTemplate(ctx, tmpl)
			if sendErr != nil || response == nil || strings.TrimSpace(response.ID) == "" {
				return false
			}
			externalID = strings.TrimSpace(response.ID)
			submittedAt := time.Now().UTC()
			if markErr := g.exitRepo.MarkPredictionExitSubmitted(ctx, entry.order.AccountID, entry.order.ID, externalID, submittedAt); markErr != nil {
				return false
			}
			entry.state.Store(int32(guardArmed))
			return true
		}
		return false
	}
	if result.Status == domain.OrderStatusCancelled || result.Status == domain.OrderStatusRejected {
		fillResult := result
		fillResult.Status = domain.OrderStatusPartial
		if result.FilledQuantity > entry.order.FilledQuantity && !g.persistRecoveredExitFill(ctx, entry, externalID, fillResult) {
			return false
		}
		return g.finalizeTerminalExit(ctx, entry, positionID, result.Status, externalID)
	}
	if result.Status != domain.OrderStatusPending && result.Status != domain.OrderStatusSubmitted && result.Status != domain.OrderStatusPartial && result.Status != domain.OrderStatusFilled {
		return false
	}
	if result.Status == domain.OrderStatusPartial || result.Status == domain.OrderStatusFilled {
		if result.Status == domain.OrderStatusFilled && !canonicalPolymarketQuantityEqual(result.FilledQuantity, entry.order.Quantity) {
			return false
		}
		submittedAt := time.Now().UTC()
		if err := g.exitRepo.MarkPredictionExitSubmitted(ctx, entry.order.AccountID, entry.order.ID, strings.TrimSpace(externalID), submittedAt); err != nil && entry.order.Status == domain.OrderStatusPending {
			return false
		}
		if !g.persistRecoveredExitFill(ctx, entry, externalID, result) {
			return false
		}
		if result.Status == domain.OrderStatusFilled {
			entry.state.Store(int32(guardFired))
			g.Cancel(entry.positionID)
			return true
		}
		entry.order.ExternalID, entry.order.Status = strings.TrimSpace(externalID), domain.OrderStatusPartial
		entry.state.Store(int32(guardArmed))
		return true
	}
	submittedAt := time.Now().UTC()
	if err := g.exitRepo.MarkPredictionExitSubmitted(ctx, entry.order.AccountID, entry.order.ID, strings.TrimSpace(externalID), submittedAt); err != nil {
		return false
	}
	entry.order.ExternalID, entry.order.Status, entry.order.SubmittedAt = strings.TrimSpace(externalID), result.Status, &submittedAt
	entry.state.Store(int32(guardArmed))
	return true
}

func (g *StopGuard) persistRecoveredExitFill(ctx context.Context, entry *guardEntry, externalID string, result execution.BrokerOrderStatus) bool {
	if g.economicWriter == nil || result.FilledQuantity <= 0 || result.FilledAvgPrice == nil || *result.FilledAvgPrice <= 0 || result.FilledAt == nil || result.FilledAt.IsZero() {
		return false
	}
	locker, ok := g.economicWriter.(repository.ExecutionAccountLocker)
	if !ok {
		return false
	}
	if held, _ := ctx.Value(stopAccountLockHeldKey{}).(bool); held {
		return g.persistRecoveredExitFillLocked(ctx, entry, externalID, result)
	}
	committed := false
	err := locker.WithExecutionAccountLock(ctx, entry.order.AccountID, func() error {
		committed = g.persistRecoveredExitFillLocked(ctx, entry, externalID, result)
		if !committed {
			return errors.New("polymarket: stop economic mutation failed")
		}
		return nil
	})
	return err == nil && committed
}

func (g *StopGuard) persistRecoveredExitFillLocked(ctx context.Context, entry *guardEntry, externalID string, result execution.BrokerOrderStatus) bool {
	recoveredOrder := *entry.order
	recoveredOrder.ExternalID = strings.TrimSpace(externalID)
	recoveredOrder.Status = result.Status
	recoveredOrder.FilledQuantity = result.FilledQuantity
	recoveredOrder.FilledAvgPrice = result.FilledAvgPrice
	recoveredOrder.FilledAt = result.FilledAt
	if recoveredOrder.SubmittedAt == nil {
		submittedAt := result.FilledAt.UTC()
		recoveredOrder.SubmittedAt = &submittedAt
	}
	trade := &domain.Trade{ID: uuid.New(), AccountID: recoveredOrder.AccountID, Environment: recoveredOrder.Environment, OriginType: recoveredOrder.OriginType, OriginID: recoveredOrder.OriginID, OrderID: &recoveredOrder.ID, Ticker: recoveredOrder.Ticker, Side: recoveredOrder.Side, Quantity: result.FilledQuantity, Price: *result.FilledAvgPrice, ExecutedAt: result.FilledAt.UTC()}
	input := repository.OrderFillInput{IdempotencyKey: fmt.Sprintf("polymarket_stop_fill:v1:%s:observed:%.8f", recoveredOrder.ID, result.FilledQuantity), Order: &recoveredOrder, FillIntent: repository.OrderFillIntent{Side: recoveredOrder.Side, Quantity: result.FilledQuantity, ExecutionPrice: *result.FilledAvgPrice}, Now: result.FilledAt.UTC(), Trade: trade}
	originType, originID := entry.scope.Origin()
	if entry.scope.AccountID() != recoveredOrder.AccountID || entry.scope.Environment() != recoveredOrder.Environment || string(originType) != recoveredOrder.OriginType || originID != recoveredOrder.OriginID {
		return false
	}
	if _, err := g.economicWriter.ApplyAcceptedOrderFill(ctx, entry.scope, input); err != nil {
		return false
	}
	*entry.order = recoveredOrder
	return true
}

func (g *StopGuard) finalizeTerminalExit(ctx context.Context, entry *guardEntry, positionID uuid.UUID, status domain.OrderStatus, externalID string) bool {
	terminalizer, ok := g.exitRepo.(stopOrderTerminalizer)
	if !ok {
		return false
	}
	if err := terminalizer.FinalizePredictionExit(ctx, entry.order.AccountID, entry.order.Environment, positionID, entry.order.ID, status, strings.TrimSpace(externalID), time.Now().UTC()); err != nil {
		lookup, ok := g.exitRepo.(repository.PredictionExitReservationLookup)
		if !ok {
			return false
		}
		resolved, lookupErr := lookup.GetPredictionExitOrderByPosition(ctx, entry.order.AccountID, entry.order.Environment, positionID)
		if lookupErr == nil && resolved.ID == entry.order.ID {
			return false
		}
		if !errors.Is(lookupErr, repository.ErrNotFound) {
			return false
		}
	}
	remaining := entry.order.Quantity - entry.order.FilledQuantity
	if remaining > 0 {
		fresh := *entry.order
		fresh.ID = uuid.New()
		fresh.ClientOrderID = "augr-polymarket-stop-" + fresh.ID.String()
		fresh.ExternalID, fresh.Status, fresh.Quantity = "", domain.OrderStatusPending, remaining
		fresh.FilledQuantity, fresh.FilledAvgPrice, fresh.FilledAt, fresh.SubmittedAt = 0, nil, nil, nil
		tmpl, err := g.broker.PrepareTemplate(&fresh)
		if err != nil {
			return false
		}
		entry.order, entry.template = &fresh, tmpl
		if err := g.claimStopExitLocked(ctx, entry, positionID); err != nil {
			entry.claimed.Store(false)
			return false
		}
		entry.state.Store(int32(guardArmed))
		return true
	}
	entry.state.Store(int32(guardFired))
	g.Cancel(entry.positionID)
	return true
}

func polymarketPositionParts(ticker string) (string, string, error) {
	ticker = strings.TrimSpace(ticker)
	if ticker == "" {
		return "", "", errors.New("polymarket: ticker is required")
	}
	slug, outcome, found := strings.Cut(ticker, ":")
	slug = strings.TrimSpace(slug)
	outcome = strings.ToUpper(strings.TrimSpace(outcome))
	if !found || slug == "" || (outcome != "YES" && outcome != "NO") {
		return "", "", fmt.Errorf("polymarket: ticker %q is not a polymarket position ticker", ticker)
	}
	return slug, outcome, nil
}

func tickMatchesOutcome(t marketdata.Tick, outcome string) bool {
	side := strings.ToUpper(strings.TrimSpace(t.Side))
	if side != "YES" && side != "NO" {
		return false
	}
	return side == strings.ToUpper(strings.TrimSpace(outcome))
}

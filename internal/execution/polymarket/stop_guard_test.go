package polymarket

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type fakeBroker struct {
	prepareTmpl          *OrderTemplate
	sendCalls            atomic.Int32
	sendErr              error
	lastTmpl             *OrderTemplate
	lastOrder            *domain.Order
	mu                   sync.Mutex
	lookupStatus         domain.OrderStatus
	lookupErr            error
	lookupExternalID     string
	lookupFilledQuantity float64
	lookupFilledAvgPrice *float64
	lookupFilledAt       *time.Time
	submittedExternalID  string
	lockDepth            atomic.Int32
	preparedUnderLock    atomic.Bool
	sentUnderLock        atomic.Bool
	persistedUnderLock   atomic.Bool
	claimedUnderLock     atomic.Bool
}

func (f *fakeBroker) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	f.lockDepth.Add(1)
	defer f.lockDepth.Add(-1)
	return fn()
}

func (f *fakeBroker) GetOrderStatus(context.Context, string) (domain.OrderStatus, error) {
	return f.lookupStatus, f.lookupErr
}

func (f *fakeBroker) GetOrderStatusByClientOrderIDResult(context.Context, string) (string, execution.BrokerOrderStatus, error) {
	return f.lookupExternalID, execution.BrokerOrderStatus{Status: f.lookupStatus, FilledQuantity: f.lookupFilledQuantity, FilledAvgPrice: f.lookupFilledAvgPrice, FilledAt: f.lookupFilledAt}, f.lookupErr
}

type recordingStopFinancialLifecycle struct {
	inputs        []repository.OrderFillInput
	applyErr      error
	resolveCommit bool
}

func (r *recordingStopFinancialLifecycle) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	return fn()
}

func (r *recordingStopFinancialLifecycle) ApplyOrderFill(_ context.Context, input repository.OrderFillInput) (repository.OrderFillResult, error) {
	r.inputs = append(r.inputs, input)
	return repository.OrderFillResult{OrderID: input.Order.ID, TradeID: input.Trade.ID}, r.applyErr
}

func (r *recordingStopFinancialLifecycle) ResolveOrderFillCommit(_ context.Context, input repository.OrderFillInput) (repository.OrderFillResult, bool, error) {
	return repository.OrderFillResult{OrderID: input.Order.ID, TradeID: input.Trade.ID}, r.resolveCommit, nil
}

func (*recordingStopFinancialLifecycle) SettlePredictionDecision(context.Context, repository.PredictionDecisionSettlementInput) (repository.PredictionDecisionSettlementResult, error) {
	return repository.PredictionDecisionSettlementResult{}, nil
}

type sharedExitClaims struct {
	mu                     sync.Mutex
	claims                 map[uuid.UUID]uuid.UUID
	terminalStatus         domain.OrderStatus
	reservedOrder          *domain.Order
	createErrAfterCommit   bool
	finalizeErrAfterCommit bool
}

func (r *sharedExitClaims) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	return fn()
}

func (r *sharedExitClaims) GetPredictionExitOrderByPosition(_ context.Context, _ uuid.UUID, _ domain.AccountEnvironment, _ uuid.UUID) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reservedOrder == nil {
		return nil, repository.ErrNotFound
	}
	copy := *r.reservedOrder
	return &copy, nil
}

func (r *sharedExitClaims) CreatePredictionExitOrderAndReserve(_ context.Context, _ uuid.UUID, _ domain.AccountEnvironment, _, _ string, positionID uuid.UUID, order *domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claims == nil {
		r.claims = make(map[uuid.UUID]uuid.UUID)
	}
	if _, exists := r.claims[positionID]; exists {
		return errors.New("already claimed")
	}
	r.claims[positionID] = order.ID
	copy := *order
	r.reservedOrder = &copy
	if r.createErrAfterCommit {
		r.createErrAfterCommit = false
		return errors.New("reservation commit acknowledgement lost")
	}
	return nil
}
func (*sharedExitClaims) ReleasePredictionExitPosition(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (*sharedExitClaims) MarkPredictionExitSubmitted(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error {
	return nil
}
func (*sharedExitClaims) ReconcilePredictionExitReservations(context.Context, uuid.UUID, domain.AccountEnvironment) error {
	return nil
}
func (r *sharedExitClaims) FinalizePredictionExit(_ context.Context, _ uuid.UUID, _ domain.AccountEnvironment, positionID, _ uuid.UUID, status domain.OrderStatus, _ string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.claims, positionID)
	r.reservedOrder = nil
	r.terminalStatus = status
	if r.finalizeErrAfterCommit {
		r.finalizeErrAfterCommit = false
		return errors.New("finalization commit acknowledgement lost")
	}
	return nil
}

var testStopGuardBinding = func() domain.ExecutionAccountBinding {
	binding, err := domain.NewExecutionAccountBinding(uuid.MustParse("00000000-0000-4000-8000-000000000064"), domain.AccountEnvironmentPaperScored)
	if err != nil {
		panic(err)
	}
	return binding
}()

func scopedGuardPosition(position Position) Position {
	position.AccountID = testStopGuardBinding.AccountID()
	position.Environment = testStopGuardBinding.Environment()
	position.OriginType = "strategy_version"
	position.OriginID = uuid.MustParse("10000000-0000-4000-8000-000000000001").String()
	return position
}

func (f *fakeBroker) CreatePredictionExitOrderAndReserve(context.Context, uuid.UUID, domain.AccountEnvironment, string, string, uuid.UUID, *domain.Order) error {
	f.claimedUnderLock.Store(f.lockDepth.Load() > 0)
	return nil
}

func (f *fakeBroker) ReleasePredictionExitPosition(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeBroker) MarkPredictionExitSubmitted(_ context.Context, _ uuid.UUID, _ uuid.UUID, externalID string, _ time.Time) error {
	f.persistedUnderLock.Store(f.lockDepth.Load() > 0)
	f.submittedExternalID = externalID
	return nil
}

func (f *fakeBroker) ReconcilePredictionExitReservations(context.Context, uuid.UUID, domain.AccountEnvironment) error {
	return nil
}

func (f *fakeBroker) PrepareTemplate(order *domain.Order) (*OrderTemplate, error) {
	f.preparedUnderLock.Store(f.lockDepth.Load() > 0)
	f.mu.Lock()
	copyOrder := *order
	f.lastOrder = &copyOrder
	f.mu.Unlock()
	if f.prepareTmpl != nil {
		return f.prepareTmpl.Clone(), nil
	}
	return NewOrderTemplate([]byte(strings.Repeat("a", 32)), "POST", "https://example.com/v1/orders", []byte(`{}`))
}

func (f *fakeBroker) SendTemplate(_ context.Context, tmpl *OrderTemplate) (*createOrderResponse, error) {
	f.sentUnderLock.Store(f.lockDepth.Load() > 0)
	f.sendCalls.Add(1)
	f.mu.Lock()
	f.lastTmpl = tmpl
	f.mu.Unlock()
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &createOrderResponse{ID: "ok"}, nil
}

func TestStopGuardHoldsAccountLockAcrossBrokerEffectAndPersistence(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: uuid.NewString(), Slug: "lock-test", Side: "BUY", EntryPx: .5, Size: 1, StopPx: .45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "lock-test", Side: "YES", Price: .4, ReceivedAt: time.Now()})
	if !broker.claimedUnderLock.Load() || !broker.preparedUnderLock.Load() || !broker.sentUnderLock.Load() || !broker.persistedUnderLock.Load() || broker.lockDepth.Load() != 0 {
		t.Fatalf("lock coverage claim=%v prepare=%v send=%v persist=%v depth=%d", broker.claimedUnderLock.Load(), broker.preparedUnderLock.Load(), broker.sentUnderLock.Load(), broker.persistedUnderLock.Load(), broker.lockDepth.Load())
	}
}

func TestStopGuard_LongStopBelowFires(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.46, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire, got %d", got)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.44, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("expected one fire, got %d", got)
	}
}

func TestStopGuard_LongTakeProfitFires(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, TakeProfitPx: 0.55})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.54, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire, got %d", got)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.56, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("expected one fire, got %d", got)
	}
}

func TestStopGuard_DuplicateTickCrossingThresholdFiresOnce(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45})); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.44, ReceivedAt: time.Now()})
		}()
	}
	wg.Wait()
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("expected exactly one fire, got %d", got)
	}
}

func TestStopGuard_DurableClaimAllowsOneSendAcrossInstances(t *testing.T) {
	broker := &fakeBroker{}
	claims := &sharedExitClaims{}
	first, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: claims})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: claims})
	if err != nil {
		t.Fatal(err)
	}
	position := scopedGuardPosition(Position{ID: uuid.New().String(), Slug: "slug-a", Side: "BUY", Size: 1, StopPx: 0.45})
	if err := first.RegisterEntry(position); err != nil {
		t.Fatal(err)
	}
	if err := second.RegisterEntry(position); err != nil {
		t.Fatal(err)
	}
	tick := marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.44, ReceivedAt: time.Now()}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); first.OnTick(context.Background(), tick) }()
	go func() { defer wg.Done(); second.OnTick(context.Background(), tick) }()
	wg.Wait()
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("broker sends = %d, want one durable owner", got)
	}
}

func TestStopGuardResolvesAmbiguousReservationCommitBeforeSend(t *testing.T) {
	broker := &fakeBroker{lookupErr: execution.ErrBrokerOrderNotFound}
	claims := &sharedExitClaims{createErrAfterCommit: true}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: claims})
	if err != nil {
		t.Fatal(err)
	}
	positionID := uuid.New()
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: positionID.String(), Slug: "ambiguous-claim", Side: "BUY", OutcomeSide: "YES", Size: 1, StopPx: .45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "ambiguous-claim", Side: "YES", Price: .4, ReceivedAt: time.Now()})
	if broker.sendCalls.Load() != 1 || claims.reservedOrder == nil || claims.reservedOrder.ExternalID != "" {
		_, resolveErr := g.resolveStopReservation(context.Background(), g.byID[positionID.String()], positionID)
		t.Fatalf("ambiguous reservation recovery sends=%d reservation=%+v resolve=%v", broker.sendCalls.Load(), claims.reservedOrder, resolveErr)
	}
}

func TestStopGuardAdoptsOtherWorkersReservationAndReconcilesBeforeSend(t *testing.T) {
	positionID := uuid.New()
	intent := domain.PositionIntentSellToClose
	reserved := &domain.Order{ID: uuid.New(), AccountID: testStopGuardBinding.AccountID(), Environment: testStopGuardBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), Ticker: "adopted", MarketType: domain.MarketTypePolymarket, Side: domain.OrderSideSell, OrderType: domain.OrderTypeMarket, Quantity: 1, Status: domain.OrderStatusPending, PositionIntent: &intent, PredictionSide: "YES", PolymarketIntent: "ORDER_INTENT_SELL_LONG", ClientOrderID: "other-worker-client"}
	claims := &sharedExitClaims{claims: map[uuid.UUID]uuid.UUID{positionID: reserved.ID}, reservedOrder: reserved}
	broker := &fakeBroker{lookupStatus: domain.OrderStatusSubmitted, lookupExternalID: "provider-existing"}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: claims})
	if err != nil {
		t.Fatal(err)
	}
	position := scopedGuardPosition(Position{ID: positionID.String(), Slug: "adopted", Side: "BUY", OutcomeSide: "YES", Size: 1, StopPx: .45})
	position.OriginType, position.OriginID = reserved.OriginType, reserved.OriginID
	if err := g.RegisterEntry(position); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "adopted", Side: "YES", Price: .4, ReceivedAt: time.Now()})
	if broker.sendCalls.Load() != 0 {
		t.Fatalf("adopted reservation submitted again: sends=%d", broker.sendCalls.Load())
	}
	if got := g.byID[positionID.String()].order; got.ID != reserved.ID || got.ClientOrderID != reserved.ClientOrderID || got.ExternalID != "provider-existing" {
		t.Fatalf("adopted order not reconciled: id=%s want=%s client=%q want=%q external=%q", got.ID, reserved.ID, got.ClientOrderID, reserved.ClientOrderID, got.ExternalID)
	}
}

func TestStopGuard_DuplicateRegistrationIsIdempotent(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	pos := scopedGuardPosition(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45, TakeProfitPx: 0.55})
	if err := g.RegisterEntry(pos); err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(pos); err != nil {
		t.Fatal(err)
	}
	if got := g.Active(); got != 1 {
		t.Fatalf("expected one active guard after duplicate registration, got %d", got)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.56, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("expected one fire after duplicate registration, got %d", got)
	}
}

func TestStopGuard_DuplicateRegistrationRefreshesPersistedEconomics(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	position := scopedGuardPosition(Position{ID: "refresh", Slug: "slug-a", Side: "BUY", Size: 1, StopPx: 0.40})
	if err := g.RegisterEntry(position); err != nil {
		t.Fatal(err)
	}
	position.Size, position.StopPx = 3, 0.45
	if err := g.RegisterEntry(position); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.44, ReceivedAt: time.Now()})
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.lastOrder == nil || broker.lastOrder.Quantity != 3 {
		t.Fatalf("refreshed order = %+v", broker.lastOrder)
	}
}

func TestStopGuard_AmbiguousSendRecoversByClientIDBeforeDisarm(t *testing.T) {
	broker := &fakeBroker{sendErr: errors.New("timeout after send"), lookupStatus: domain.OrderStatusSubmitted, lookupExternalID: "poly-real-42"}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "recover", Slug: "slug-a", Side: "BUY", Size: 1, StopPx: 0.45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.44, ReceivedAt: time.Now()})
	if g.Active() != 1 {
		t.Fatalf("recovered submitted stop lost durable protection")
	}
	if broker.submittedExternalID != "poly-real-42" {
		t.Fatalf("persisted external id = %q", broker.submittedExternalID)
	}
}

func TestStopGuard_CancelPreventsFire(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45})); err != nil {
		t.Fatal(err)
	}
	g.Cancel("1")
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.44, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire after cancel, got %d", got)
	}
}

func TestStopGuard_OtherSlugIgnored(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "1", Slug: "slug-a", Side: "BUY", EntryPx: 0.50, Size: 1, StopPx: 0.45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-b", Side: "YES", Price: 0.44, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("expected no fire on other slug, got %d", got)
	}
}

func TestStopGuard_RegisterPositionPreservesNoOutcomeIntent(t *testing.T) {
	broker := &fakeBroker{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	stop := 0.40
	pos := domain.Position{ID: uuidFromString(t, "00000000-0000-0000-0000-000000000001"), AccountID: testStopGuardBinding.AccountID(), Environment: testStopGuardBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.New().String(), MarketType: domain.MarketTypePolymarket, Ticker: "slug-a:NO", Side: domain.PositionSideLong, Quantity: 2, AvgEntry: 0.50, StopLoss: &stop}
	if err := g.RegisterPosition(pos); err != nil {
		t.Fatal(err)
	}
	if broker.lastOrder == nil {
		t.Fatal("expected prepared order")
	}
	if broker.lastOrder.PredictionSide != "NO" || broker.lastOrder.PolymarketIntent != "ORDER_INTENT_SELL_SHORT" {
		t.Fatalf("unexpected NO close order: %+v", broker.lastOrder)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.39, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 0 {
		t.Fatalf("YES tick fired NO guard, got %d", got)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "NO", Price: 0.39, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("NO tick did not fire NO guard, got %d", got)
	}
}

func TestStopGuardBootstrapResumesReservedOrderIdentity(t *testing.T) {
	positionID := uuid.New()
	stop := 0.40
	pos := domain.Position{ID: positionID, AccountID: testStopGuardBinding.AccountID(), Environment: testStopGuardBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), MarketType: domain.MarketTypePolymarket, Ticker: "slug-a:YES", Side: domain.PositionSideLong, Quantity: 2, AvgEntry: 0.50, StopLoss: &stop}
	intent := domain.PositionIntentSellToClose
	reserved := &domain.Order{ID: uuid.New(), AccountID: pos.AccountID, Environment: pos.Environment, OriginType: pos.OriginType, OriginID: pos.OriginID, Ticker: "slug-a", MarketType: domain.MarketTypePolymarket, Side: domain.OrderSideSell, OrderType: domain.OrderTypeMarket, Quantity: 2, Status: domain.OrderStatusPending, PositionIntent: &intent, PredictionSide: "YES", PolymarketIntent: "ORDER_INTENT_SELL_LONG", ClientOrderID: "reserved-stop-client"}
	repo := &sharedExitClaims{reservedOrder: reserved}
	broker := &fakeBroker{lookupStatus: domain.OrderStatusSubmitted, lookupExternalID: "reserved-venue-id"}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterPositionContext(context.Background(), pos); err != nil {
		t.Fatal(err)
	}
	if broker.lastOrder == nil || broker.lastOrder.ID != reserved.ID || broker.lastOrder.ClientOrderID != reserved.ClientOrderID {
		t.Fatalf("bootstrap prepared replacement order: %+v", broker.lastOrder)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.39, ReceivedAt: time.Now()})
	if broker.sendCalls.Load() != 0 {
		t.Fatal("reserved order was resubmitted")
	}
}

func TestValidateRecoveredStopReservationComparesCanonicalFractionalRemainder(t *testing.T) {
	positionID := uuid.New()
	position := domain.Position{ID: positionID, AccountID: testStopGuardBinding.AccountID(), Environment: testStopGuardBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), MarketType: domain.MarketTypePolymarket, Ticker: "slug-a:YES", Side: domain.PositionSideLong, Quantity: 0.2}
	intent := domain.PositionIntentSellToClose
	order := &domain.Order{ID: uuid.New(), AccountID: position.AccountID, Environment: position.Environment, OriginType: position.OriginType, OriginID: position.OriginID, ClientOrderID: "fractional-stop", Ticker: "slug-a", MarketType: domain.MarketTypePolymarket, Side: domain.OrderSideSell, OrderType: domain.OrderTypeMarket, Quantity: 0.3, FilledQuantity: 0.1, Status: domain.OrderStatusPartial, PositionIntent: &intent, PredictionSide: "YES", PolymarketIntent: "ORDER_INTENT_SELL_LONG"}
	expected := scopedGuardPosition(Position{ID: positionID.String(), Slug: "slug-a", OutcomeSide: "YES"})
	expected.OriginID = position.OriginID
	if err := validateRecoveredStopReservation(order, position, expected); err != nil {
		t.Fatalf("canonical fractional remainder rejected: %v", err)
	}
}

func TestStopGuardReconcilesClaimedFilledExitBeforeTriggerCheck(t *testing.T) {
	positionID := uuid.New()
	stop := 0.40
	pos := domain.Position{ID: positionID, AccountID: testStopGuardBinding.AccountID(), Environment: testStopGuardBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), MarketType: domain.MarketTypePolymarket, Ticker: "slug-a:YES", Side: domain.PositionSideLong, Quantity: 2, AvgEntry: 0.50, StopLoss: &stop}
	intent := domain.PositionIntentSellToClose
	reserved := &domain.Order{ID: uuid.New(), AccountID: pos.AccountID, Environment: pos.Environment, OriginType: pos.OriginType, OriginID: pos.OriginID, Ticker: "slug-a", MarketType: domain.MarketTypePolymarket, Side: domain.OrderSideSell, OrderType: domain.OrderTypeMarket, Quantity: 2, Status: domain.OrderStatusSubmitted, PositionIntent: &intent, PredictionSide: "YES", PolymarketIntent: "ORDER_INTENT_SELL_LONG", ClientOrderID: "reserved-stop-client"}
	price := 0.41
	filledAt := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	broker := &fakeBroker{lookupStatus: domain.OrderStatusFilled, lookupExternalID: "filled-venue-id", lookupFilledQuantity: 2, lookupFilledAvgPrice: &price, lookupFilledAt: &filledAt}
	financial := &recordingStopFinancialLifecycle{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: &sharedExitClaims{reservedOrder: reserved}, FinancialLifecycle: financial})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterPositionContext(context.Background(), pos); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.50, ReceivedAt: time.Now()})
	if len(financial.inputs) != 1 || !financial.inputs[0].Now.Equal(filledAt) || financial.inputs[0].FillIntent.Quantity != 2 {
		t.Fatalf("recovered economics = %+v", financial.inputs)
	}
	if g.Active() != 0 || broker.sendCalls.Load() != 0 {
		t.Fatalf("filled recovery active=%d sends=%d", g.Active(), broker.sendCalls.Load())
	}
}

func TestStopGuardRecoveredFillDoesNotMutateOrderBeforeConfirmedCommit(t *testing.T) {
	filledAt := time.Now().UTC()
	price := 0.41
	order := &domain.Order{ID: uuid.New(), AccountID: testStopGuardBinding.AccountID(), Environment: testStopGuardBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.NewString(), Ticker: "slug-a", Side: domain.OrderSideSell, Status: domain.OrderStatusSubmitted, FilledQuantity: 0}
	financial := &recordingStopFinancialLifecycle{applyErr: errors.New("commit unknown")}
	g := &StopGuard{financialLifecycle: financial}
	entry := &guardEntry{order: order}
	if g.persistRecoveredExitFillLocked(context.Background(), entry, "venue-fill", execution.BrokerOrderStatus{Status: domain.OrderStatusFilled, FilledQuantity: 2, FilledAvgPrice: &price, FilledAt: &filledAt}) {
		t.Fatal("unconfirmed commit reported success")
	}
	if order.ExternalID != "" || order.Status != domain.OrderStatusSubmitted || order.FilledQuantity != 0 || order.FilledAt != nil {
		t.Fatalf("order mutated before commit confirmation: %+v", order)
	}
	if len(financial.inputs) != 1 || financial.inputs[0].Order == order {
		t.Fatal("recovered economics were not applied from an isolated order copy")
	}
}

func TestStopGuard_SendFailureRetainsDurableClaimWithoutResubmit(t *testing.T) {
	broker := &fakeBroker{sendErr: errors.New("temporary")}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "1", Slug: "slug-a", Side: "BUY", OutcomeSide: "YES", EntryPx: 0.50, Size: 1, StopPx: 0.45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.44, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("send calls = %d, want first attempt", got)
	}
	if got := g.Active(); got != 1 {
		t.Fatalf("active guards = %d, want guard retained after send failure", got)
	}
	broker.sendErr = nil
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: 0.43, ReceivedAt: time.Now()})
	if got := broker.sendCalls.Load(); got != 1 {
		t.Fatalf("send calls = %d, want no ambiguous resubmit", got)
	}
	if got := g.Active(); got != 1 {
		t.Fatalf("active guards = %d, want durable claim retained", got)
	}
}

func TestStopGuard_DefinitiveClientIDMissRetriesSameDurableOrder(t *testing.T) {
	broker := &fakeBroker{sendErr: errors.New("timeout after send"), lookupErr: execution.ErrBrokerOrderNotFound}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: "retry", Slug: "slug-a", Side: "BUY", Size: 1, StopPx: .45})); err != nil {
		t.Fatal(err)
	}
	tick := marketdata.Tick{Slug: "slug-a", Side: "YES", Price: .44, ReceivedAt: time.Now()}
	g.OnTick(context.Background(), tick)
	broker.sendErr, broker.lookupErr = nil, execution.ErrBrokerOrderNotFound
	g.OnTick(context.Background(), tick)
	if broker.sendCalls.Load() != 2 || g.Active() != 1 {
		t.Fatalf("definitive miss retry: sends=%d active=%d", broker.sendCalls.Load(), g.Active())
	}
}

func TestStopGuard_DefinitiveTerminalOutcomeReleasesAtomically(t *testing.T) {
	broker := &fakeBroker{sendErr: errors.New("timeout after send"), lookupStatus: domain.OrderStatusCancelled, lookupExternalID: "cancelled-order"}
	claims := &sharedExitClaims{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: claims})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: uuid.NewString(), Slug: "slug-a", Side: "BUY", Size: 1, StopPx: .45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "slug-a", Side: "YES", Price: .44, ReceivedAt: time.Now()})
	if g.Active() != 1 || claims.terminalStatus != domain.OrderStatusCancelled {
		t.Fatalf("terminal stop outcome: active=%d status=%s", g.Active(), claims.terminalStatus)
	}
}

func TestStopGuardResolvesAmbiguousPartialFinalizationAndReservesRemainder(t *testing.T) {
	price, filledAt := .4, time.Now().UTC()
	broker := &fakeBroker{sendErr: errors.New("timeout after send"), lookupStatus: domain.OrderStatusCancelled, lookupExternalID: "partial-cancel", lookupFilledQuantity: .4, lookupFilledAvgPrice: &price, lookupFilledAt: &filledAt}
	claims := &sharedExitClaims{finalizeErrAfterCommit: true}
	financial := &recordingStopFinancialLifecycle{}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker, ExitRepo: claims, FinancialLifecycle: financial})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterEntry(scopedGuardPosition(Position{ID: uuid.NewString(), Slug: "partial-finalize", Side: "BUY", OutcomeSide: "YES", Size: 1, StopPx: .45})); err != nil {
		t.Fatal(err)
	}
	g.OnTick(context.Background(), marketdata.Tick{Slug: "partial-finalize", Side: "YES", Price: .4, ReceivedAt: time.Now()})
	if len(financial.inputs) != 1 || claims.terminalStatus != domain.OrderStatusCancelled || claims.reservedOrder == nil || claims.reservedOrder.Status != domain.OrderStatusPending || claims.reservedOrder.Quantity != .6 {
		t.Fatalf("partial finalization inputs=%v terminal=%s remainder=%+v", financial.inputs, claims.terminalStatus, claims.reservedOrder)
	}
}

func TestStopGuardRejectsInvalidAndForeignEnvironmentBindings(t *testing.T) {
	broker := &fakeBroker{}
	if _, err := NewStopGuard(StopGuardConfig{Broker: broker}); err == nil {
		t.Fatal("invalid execution binding accepted")
	}
	g, err := NewStopGuard(StopGuardConfig{ExecutionAccount: testStopGuardBinding, Broker: broker})
	if err != nil {
		t.Fatal(err)
	}
	foreign := scopedGuardPosition(Position{ID: "foreign", Slug: "slug", Side: "BUY", Size: 1, StopPx: .4})
	foreign.Environment = domain.AccountEnvironmentShadow
	if err := g.RegisterEntry(foreign); err == nil {
		t.Fatal("foreign environment position accepted")
	}
	local := scopedGuardPosition(Position{ID: "local", Slug: "slug", Side: "BUY", Size: 1, StopPx: .4})
	if err := g.RegisterEntry(local); err != nil {
		t.Fatal(err)
	}
	if broker.lastOrder.AccountID != testStopGuardBinding.AccountID() || broker.lastOrder.Environment != testStopGuardBinding.Environment() {
		t.Fatalf("prepared order lost execution binding: %+v", broker.lastOrder)
	}
}

func uuidFromString(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

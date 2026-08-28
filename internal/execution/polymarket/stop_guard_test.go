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
	marketdata "github.com/PatrickFanella/get-rich-quick/internal/marketdata/polymarket"
)

type fakeBroker struct {
	prepareTmpl *OrderTemplate
	sendCalls   atomic.Int32
	sendErr     error
	lastTmpl    *OrderTemplate
	lastOrder   *domain.Order
	mu          sync.Mutex
}

type sharedExitClaims struct {
	mu     sync.Mutex
	claims map[uuid.UUID]uuid.UUID
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
	return nil
}

func (f *fakeBroker) ReleasePredictionExitPosition(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeBroker) MarkPredictionExitSubmitted(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error {
	return nil
}

func (f *fakeBroker) ReconcilePredictionExitReservations(context.Context, uuid.UUID, domain.AccountEnvironment) error {
	return nil
}

func (f *fakeBroker) PrepareTemplate(order *domain.Order) (*OrderTemplate, error) {
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
	f.sendCalls.Add(1)
	f.mu.Lock()
	f.lastTmpl = tmpl
	f.mu.Unlock()
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &createOrderResponse{ID: "ok"}, nil
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
	pos := domain.Position{ID: uuidFromString(t, "00000000-0000-0000-0000-000000000001"), AccountID: testStopGuardBinding.AccountID(), Environment: testStopGuardBinding.Environment(), OriginType: "strategy_version", OriginID: uuid.New().String(), Ticker: "slug-a:NO", Side: domain.PositionSideLong, Quantity: 2, AvgEntry: 0.50, StopLoss: &stop}
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

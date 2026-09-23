package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestSchedulerReloadReconcilesStrategyEntries(t *testing.T) {
	t.Parallel()

	keep := domain.Strategy{ID: uuid.New(), Ticker: "AAA", MarketType: domain.MarketTypeCrypto, ScheduleCron: "@every 1m", Status: domain.StrategyStatusActive}
	gone := domain.Strategy{ID: uuid.New(), Ticker: "BBB", MarketType: domain.MarketTypeCrypto, ScheduleCron: "@every 2m", Status: domain.StrategyStatusActive}
	changed := domain.Strategy{ID: uuid.New(), Ticker: "CCC", MarketType: domain.MarketTypeCrypto, ScheduleCron: "@every 3m", Status: domain.StrategyStatusActive}
	repo := &mockStrategyRepo{strategies: []domain.Strategy{keep, gone, changed}}
	fakeCron := &fakeCronEngine{}
	s := NewScheduler(repo, &mockPipeline{}, &mockRiskEngine{}, testLogger(), WithReloadInterval(0))
	s.newCron = func() cronEngine { return fakeCron }

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop()
	if got := s.RegisteredStrategyCount(); got != 3 {
		t.Fatalf("registered = %d, want 3", got)
	}

	added := domain.Strategy{ID: uuid.New(), Ticker: "DDD", MarketType: domain.MarketTypeCrypto, ScheduleCron: "@every 4m", Status: domain.StrategyStatusPaused}
	changed.ScheduleCron = "@every 5m"
	retired := domain.Strategy{ID: uuid.New(), Ticker: "EEE", MarketType: domain.MarketTypeCrypto, ScheduleCron: "", Status: domain.StrategyStatusActive}
	repo.mu.Lock()
	repo.strategies = []domain.Strategy{keep, changed, added, retired}
	repo.mu.Unlock()

	if err := s.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	if got := s.RegisteredStrategyCount(); got != 3 {
		t.Fatalf("registered after reload = %d, want 3 (keep, changed, added)", got)
	}
	fakeCron.mu.Lock()
	removed := append([]cron.EntryID(nil), fakeCron.removed...)
	fakeCron.mu.Unlock()
	if len(removed) != 2 {
		t.Fatalf("removed entries = %v, want 2 (gone + changed spec)", removed)
	}
	s.mu.Lock()
	entry, ok := s.strategyEntries[changed.ID]
	_, goneOK := s.strategyEntries[gone.ID]
	_, addedOK := s.strategyEntries[added.ID]
	s.mu.Unlock()
	if !ok || entry.spec != "@every 5m" {
		t.Fatalf("changed entry = %+v, want spec @every 5m", entry)
	}
	if goneOK {
		t.Fatal("deleted strategy still registered")
	}
	if !addedOK {
		t.Fatal("new paused strategy not registered")
	}

	// A second reload with no changes is a no-op.
	if err := s.Reload(context.Background()); err != nil {
		t.Fatalf("second Reload() error = %v", err)
	}
	fakeCron.mu.Lock()
	removedAfter := len(fakeCron.removed)
	fakeCron.mu.Unlock()
	if removedAfter != 2 {
		t.Fatalf("no-op reload removed entries: %d", removedAfter)
	}
}

func TestSchedulerReloadRequiresStart(t *testing.T) {
	t.Parallel()
	s := NewScheduler(&mockStrategyRepo{}, &mockPipeline{}, &mockRiskEngine{}, testLogger())
	if err := s.Reload(context.Background()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Reload() error = %v, want ErrNotStarted", err)
	}
}

func TestSchedulerPeriodicReloadPicksUpNewStrategies(t *testing.T) {
	t.Parallel()

	repo := &mockStrategyRepo{}
	fakeCron := &fakeCronEngine{}
	s := NewScheduler(repo, &mockPipeline{}, &mockRiskEngine{}, testLogger(), WithReloadInterval(10*time.Millisecond))
	s.newCron = func() cronEngine { return fakeCron }
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop()

	repo.mu.Lock()
	repo.strategies = []domain.Strategy{{ID: uuid.New(), Ticker: "AAA", MarketType: domain.MarketTypeCrypto, ScheduleCron: "@every 1m", Status: domain.StrategyStatusActive}}
	repo.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for s.RegisteredStrategyCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("periodic reload did not register the new strategy")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSchedulerRunOutcomeHook(t *testing.T) {
	t.Parallel()

	active := domain.Strategy{ID: uuid.New(), Ticker: "AAA", MarketType: domain.MarketTypeCrypto, ScheduleCron: "@every 1m", Status: domain.StrategyStatusActive}
	paused := domain.Strategy{ID: uuid.New(), Ticker: "BBB", MarketType: domain.MarketTypeCrypto, ScheduleCron: "@every 1m", Status: domain.StrategyStatusPaused}
	repo := &mockStrategyRepo{strategies: []domain.Strategy{active, paused}}

	var mu sync.Mutex
	outcomes := map[uuid.UUID]string{}
	var lastErr error
	execErr := errors.New("boom")
	s := NewScheduler(repo, nil, &mockRiskEngine{}, testLogger(),
		WithReloadInterval(0),
		WithStrategyExecution(func(context.Context, domain.Strategy) error { return execErr }),
		WithRunOutcomeHook(func(strategy domain.Strategy, outcome string, err error) {
			mu.Lock()
			defer mu.Unlock()
			outcomes[strategy.ID] = outcome
			if err != nil {
				lastErr = err
			}
		}),
	)
	s.ctx = context.Background()

	s.runStrategy(active)
	s.runStrategy(paused)

	mu.Lock()
	defer mu.Unlock()
	if outcomes[active.ID] != RunOutcomeExecutionFailed {
		t.Fatalf("active outcome = %q, want %q", outcomes[active.ID], RunOutcomeExecutionFailed)
	}
	if !errors.Is(lastErr, execErr) {
		t.Fatalf("hook err = %v, want %v", lastErr, execErr)
	}
	if outcomes[paused.ID] != RunOutcomePaused {
		t.Fatalf("paused outcome = %q, want %q", outcomes[paused.ID], RunOutcomePaused)
	}
}

func TestSchedulerRunOutcomeHookPanicContained(t *testing.T) {
	t.Parallel()
	active := domain.Strategy{ID: uuid.New(), Ticker: "AAA", MarketType: domain.MarketTypeCrypto, Status: domain.StrategyStatusActive}
	repo := &mockStrategyRepo{strategies: []domain.Strategy{active}}
	s := NewScheduler(repo, nil, &mockRiskEngine{}, testLogger(),
		WithStrategyExecution(func(context.Context, domain.Strategy) error { return nil }),
		WithRunOutcomeHook(func(domain.Strategy, string, error) { panic("recorder down") }),
	)
	s.ctx = context.Background()
	s.runStrategy(active) // must not panic
}

func TestSchedulerDefaultCronUsesNewYorkLocation(t *testing.T) {
	t.Parallel()
	s := NewScheduler(&mockStrategyRepo{}, &mockPipeline{}, &mockRiskEngine{}, testLogger())
	engine, ok := s.newCron().(*cron.Cron)
	if !ok {
		t.Fatalf("default cron engine type = %T", s.newCron())
	}
	if got := engine.Location(); got.String() != "America/New_York" {
		t.Fatalf("cron location = %s, want America/New_York", got)
	}
}

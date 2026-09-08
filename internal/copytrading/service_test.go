package copytrading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/copyorigin"
	"github.com/PatrickFanella/get-rich-quick/internal/data/edgar"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

type syncRepo struct {
	repository.CopyTradingRepository
	subscriptions []domain.CopySubscription
	source        domain.CopyLeaderSource
	saves         int
	observed      int
}

func (r *syncRepo) ListSubscriptions(context.Context, repository.CopySubscriptionFilter, int, int) ([]domain.CopySubscription, error) {
	return r.subscriptions, nil
}

func (r *syncRepo) GetSource(context.Context, uuid.UUID) (*domain.CopyLeaderSource, error) {
	sourceCopy := r.source
	return &sourceCopy, nil
}

func (r *syncRepo) Save13FSnapshot(_ context.Context, observation *domain.CopySourceObservation, snapshot *domain.CopyPortfolioSnapshot) (bool, error) {
	r.saves++
	observation.ID = uuid.New()
	snapshot.ID = uuid.New()
	snapshot.ObservationID = observation.ID
	return true, nil
}

func (r *syncRepo) UpdateSourceObserved(context.Context, uuid.UUID, time.Time, json.RawMessage) error {
	r.observed++
	return nil
}

func (r *syncRepo) UpdateLeaderIdentityStatus(context.Context, uuid.UUID, domain.CopyIdentityStatus) error {
	return nil
}

type fixed13FFetcher struct{ calls int }

func (f *fixed13FFetcher) FetchLatest13F(context.Context, string) (*edgar.ThirteenFFiling, error) {
	f.calls++
	return &edgar.ThirteenFFiling{
		CIK: "1067983", Accession: "0000000000-26-000001", Form: "13F-HR",
		ReportPeriod: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		FiledAt:      time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC),
		ContentHash:  "hash",
		Holdings:     []domain.CopyPortfolioHolding{{IssuerName: "Example", CUSIP: "123456789", DisclosedValue: 1000, SharesOrPrincipal: 10}},
	}, nil
}

type originCreateRepo struct {
	repository.CopyTradingRepository
	leader       domain.CopyLeader
	source       domain.CopyLeaderSource
	subscription *domain.CopySubscription
}

func (r *originCreateRepo) GetLeader(context.Context, uuid.UUID) (*domain.CopyLeader, error) {
	value := r.leader
	return &value, nil
}

func (r *originCreateRepo) GetSource(context.Context, uuid.UUID) (*domain.CopyLeaderSource, error) {
	value := r.source
	return &value, nil
}

func (r *originCreateRepo) CreateSubscription(_ context.Context, value *domain.CopySubscription) error {
	stored := *value
	r.subscription = &stored
	return nil
}

type strategyWriteTrap struct {
	repository.StrategyRepository
	creates int
}

type cancellationRaceCopyRepo struct {
	repository.CopyTradingRepository
	subscription    domain.CopySubscription
	observation     domain.CopySourceObservation
	snapshot        domain.CopyPortfolioSnapshot
	mapping         domain.CopyInstrumentMapping
	intentWrites    int
	intent          *domain.CopyTradeIntent
	claimedIntentID uuid.UUID
	stopAfterClaim  bool
	claimDenied     bool
	claimErr        error
	completeErr     error
	completeLost    bool
	completed       *domain.CopyTradeIntent
	listErr         error
}

func (r *cancellationRaceCopyRepo) ListSubscriptions(context.Context, repository.CopySubscriptionFilter, int, int) ([]domain.CopySubscription, error) {
	return nil, r.listErr
}

func (r *cancellationRaceCopyRepo) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	return fn()
}

func (r *cancellationRaceCopyRepo) GetSubscription(context.Context, uuid.UUID) (*domain.CopySubscription, error) {
	value := r.subscription
	return &value, nil
}

func (r *cancellationRaceCopyRepo) GetLatest13FSnapshot(context.Context, uuid.UUID) (*domain.CopySourceObservation, *domain.CopyPortfolioSnapshot, error) {
	observation, snapshot := r.observation, r.snapshot
	return &observation, &snapshot, nil
}

func (r *cancellationRaceCopyRepo) ListInstrumentMappings(context.Context, string, string, []string) ([]domain.CopyInstrumentMapping, error) {
	return []domain.CopyInstrumentMapping{r.mapping}, nil
}

func (r *cancellationRaceCopyRepo) CreateIntent(_ context.Context, intent *domain.CopyTradeIntent) (bool, error) {
	r.intentWrites++
	value := *intent
	r.intent = &value
	return true, nil
}

func (r *cancellationRaceCopyRepo) UpdateIntent(context.Context, *domain.CopyTradeIntent) error {
	return nil
}

func (r *cancellationRaceCopyRepo) ClaimIntentExecution(_ context.Context, intentID, _ uuid.UUID, _ time.Time) (bool, error) {
	r.claimedIntentID = intentID
	return !r.claimDenied && r.claimErr == nil, r.claimErr
}

func (r *cancellationRaceCopyRepo) GetClaimedIntentExecution(_ context.Context, _, _ uuid.UUID) (*domain.CopyTradeIntent, *domain.CopySubscription, error) {
	if r.stopAfterClaim {
		return nil, nil, repository.ErrNotFound
	}
	var intent domain.CopyTradeIntent
	if r.intent != nil && r.intent.ID == r.claimedIntentID {
		intent = *r.intent
	} else {
		price := 100.0
		intent = domain.CopyTradeIntent{ID: r.claimedIntentID, AccountID: r.subscription.AccountID, Environment: r.subscription.Environment, SubscriptionID: r.subscription.ID, OriginType: "copy_subscription", OriginID: r.subscription.ID, SourceObservationID: r.observation.ID, InstrumentKey: r.mapping.Ticker, Ticker: r.mapping.Ticker, Side: domain.OrderSideBuy, RequestedNotional: r.subscription.CapitalBudget, ExecutablePrice: &price, PolicyStatus: "approved", RiskStatus: "pending", Status: "received"}
	}
	subscription := r.subscription
	return &intent, &subscription, nil
}

func (r *cancellationRaceCopyRepo) CompleteIntentExecution(ctx context.Context, intent *domain.CopyTradeIntent, _ uuid.UUID) (bool, error) {
	if r.completeErr != nil || r.completeLost {
		return false, r.completeErr
	}
	value := *intent
	r.completed = &value
	return true, r.UpdateIntent(ctx, intent)
}

type cancellationRacePrices struct {
	snapshot PriceSnapshot
}

func (p cancellationRacePrices) Snapshots(context.Context, []string, time.Time) (map[string]PriceSnapshot, error) {
	return map[string]PriceSnapshot{p.snapshot.Ticker: p.snapshot}, nil
}

type countingCopyExecutor struct {
	calls   int
	request PaperOrderRequest
}

type recoveringCopyExecutor struct {
	countingCopyExecutor
	found bool
}

func (e *recoveringCopyExecutor) FindCopyOrderEffect(context.Context, PaperOrderRequest) (bool, error) {
	return e.found, nil
}

type countingCopyLifecycle struct{ calls int }

func (l *countingCopyLifecycle) ProposeCopyIntent(context.Context, domain.CopySubscription, domain.CopyTradeIntent, uuid.UUID) error {
	l.calls++
	return nil
}

func (e *countingCopyExecutor) ExecuteCopyOrder(_ context.Context, request PaperOrderRequest) (PaperOrderResult, error) {
	e.calls++
	e.request = request
	id := uuid.New()
	return PaperOrderResult{Scope: request.Scope, OrderID: &id, Status: domain.OrderStatusSubmitted}, nil
}

type resultCopyExecutor struct {
	calls  int
	result PaperOrderResult
	err    error
}

func (e *resultCopyExecutor) ExecuteCopyOrder(_ context.Context, request PaperOrderRequest) (PaperOrderResult, error) {
	e.calls++
	e.result.Scope = request.Scope
	return e.result, e.err
}

type plannedOriginStore struct {
	err         error
	calls       int
	intents     []domain.CopyTradeIntent
	replayed    bool
	foreign     bool
	status      string
	risk        string
	recoverable []copyorigin.RecoverableRun
}

func (s *plannedOriginStore) ListUnfinishedRuns(context.Context, uuid.UUID, domain.AccountEnvironment) ([]copyorigin.RecoverableRun, error) {
	return s.recoverable, nil
}

func (s *plannedOriginStore) RegisterRun(context.Context, *copyorigin.Run) (*copyorigin.Run, error) {
	panic("unexpected non-atomic origin registration")
}

func (s *plannedOriginStore) GetRun(context.Context, uuid.UUID) (*copyorigin.Run, error) {
	return nil, repository.ErrNotFound
}

func (s *plannedOriginStore) RegisterPlannedRun(_ context.Context, run *copyorigin.Run, intents []domain.CopyTradeIntent) (*copyorigin.Run, []copyorigin.PlannedIntent, error) {
	s.calls++
	s.intents = append([]domain.CopyTradeIntent(nil), intents...)
	if s.err != nil {
		return nil, nil, s.err
	}
	planned := make([]copyorigin.PlannedIntent, len(intents))
	for i := range intents {
		if s.foreign {
			intents[i].AccountID = uuid.New()
		}
		if s.status != "" {
			intents[i].Status = s.status
			intents[i].RiskStatus = s.risk
		}
		planned[i] = copyorigin.PlannedIntent{Intent: intents[i], Created: !s.replayed}
	}
	return run, planned, nil
}

type completedLoserRunRepo struct {
	repository.PipelineRunRepository
}

func (*completedLoserRunRepo) Create(context.Context, *domain.PipelineRun) error { return nil }

func (*completedLoserRunRepo) Finalize(_ context.Context, ref domain.PipelineRunRef, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	run := domain.PipelineRun{ID: ref.ID, TradeDate: ref.TradeDate, Status: domain.PipelineStatusCompleted, Signal: *finalization.Signal}
	return repository.PipelineRunFinalizationReceipt{Run: run}, nil
}

func (r *strategyWriteTrap) Create(context.Context, *domain.Strategy) error {
	r.creates++
	return nil
}

func TestCreateSubscriptionOwnsOriginWithoutBackingStrategy(t *testing.T) {
	t.Parallel()
	leaderID, sourceID := uuid.New(), uuid.New()
	repo := &originCreateRepo{
		leader: domain.CopyLeader{ID: leaderID, EntityType: domain.CopyLeaderInstitution},
		source: domain.CopyLeaderSource{ID: sourceID, LeaderID: leaderID, SourceType: domain.CopySourceSEC13F},
	}
	strategies := &strategyWriteTrap{}
	binding, _ := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	service := NewService(ServiceDeps{Repo: repo, Strategies: strategies, ExecutionAccount: binding})
	subscription := domain.DefaultCopySubscription()
	subscription.LeaderID, subscription.SourceID = leaderID, sourceID
	subscription.ID = uuid.New()
	subscription.OriginType, subscription.OriginID = "operator", uuid.New()
	legacy := uuid.New()
	subscription.LegacyStrategyID = &legacy

	if err := service.CreateSubscription(context.Background(), &subscription); err != nil {
		t.Fatal(err)
	}
	if strategies.creates != 0 {
		t.Fatalf("backing strategy writes=%d", strategies.creates)
	}
	if repo.subscription == nil || repo.subscription.ID == uuid.Nil || repo.subscription.ID != subscription.ID || repo.subscription.AccountID != binding.AccountID() || repo.subscription.Environment != binding.Environment() || repo.subscription.OriginType != "copy_subscription" || repo.subscription.OriginID != subscription.ID || repo.subscription.LegacyStrategyID != nil {
		t.Fatalf("subscription=%+v retained=%+v", subscription, repo.subscription)
	}
}

func TestSync13FSubscriptionsRefreshesSharedPausedSourceOnce(t *testing.T) {
	t.Parallel()
	sourceID := uuid.New()
	repo := &syncRepo{
		source: domain.CopyLeaderSource{ID: sourceID, SourceType: domain.CopySourceSEC13F, ExternalKey: "1067983"},
		subscriptions: []domain.CopySubscription{
			{ID: uuid.New(), SourceID: sourceID, Status: domain.CopySubscriptionPaused, IsPaper: true},
			{ID: uuid.New(), SourceID: sourceID, Status: domain.CopySubscriptionPaused, IsPaper: true},
		},
	}
	fetcher := &fixed13FFetcher{}
	service := NewService(ServiceDeps{Repo: repo, EDGAR: fetcher, Now: func() time.Time { return time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC) }})

	summary, err := service.Sync13FSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("Sync13FSubscriptions() error = %v", err)
	}
	if summary.Subscriptions != 2 || summary.SourcesChecked != 1 || summary.NewFilings != 1 || summary.Rebalanced != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if fetcher.calls != 1 || repo.saves != 1 || repo.observed != 1 {
		t.Fatalf("calls fetch=%d save=%d observed=%d", fetcher.calls, repo.saves, repo.observed)
	}
}

func TestOriginNativeRebalanceUsesAtomicPlanningBoundary(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	subscription := domain.DefaultCopySubscription()
	subscription.ID, subscription.SourceID = uuid.New(), uuid.New()
	subscription.OriginType, subscription.OriginID = "copy_subscription", subscription.ID
	subscription.Status = domain.CopySubscriptionPaperActive
	binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	subscription.AccountID, subscription.Environment = binding.AccountID(), binding.Environment()
	subscription.MaxSpreadBPS = 200
	repo := &cancellationRaceCopyRepo{
		subscription: subscription,
		observation:  domain.CopySourceObservation{ID: uuid.New()},
		snapshot:     domain.CopyPortfolioSnapshot{TotalDisclosedValue: 1000, Holdings: []domain.CopyPortfolioHolding{{CUSIP: "123456789", DisclosedValue: 1000}}},
		mapping:      domain.CopyInstrumentMapping{IdentifierValue: "123456789", Ticker: "AAPL", Confidence: "provider_verified"},
	}
	availableAt := now.Add(-time.Second)
	prices := cancellationRacePrices{snapshot: PriceSnapshot{Ticker: "AAPL", QuoteSnapshotID: uuid.New(), Bid: "99", Ask: "100", AvailableAt: &availableAt, MarketStatus: "open", SessionStatus: "regular", AvgDollarVolume: 1_000_000_000}}
	executor := &countingCopyExecutor{}

	t.Run("success", func(t *testing.T) {
		store := &plannedOriginStore{}
		service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: store, Runs: &completedLoserRunRepo{}, Prices: prices, Executor: executor, Now: func() time.Time { return now }})
		result, err := service.Rebalance(context.Background(), subscription.ID)
		if err != nil {
			t.Fatal(err)
		}
		if store.calls != 1 || len(store.intents) != 1 || result.OriginRunID == uuid.Nil || len(result.Intents) != 1 {
			t.Fatalf("atomic calls=%d planned=%d result=%+v", store.calls, len(store.intents), result)
		}
		if repo.intentWrites != 0 || executor.calls != 1 {
			t.Fatalf("downstream effects: intent writes=%d orders=%d", repo.intentWrites, executor.calls)
		}
		if executor.request.OriginRunID != result.OriginRunID {
			t.Fatalf("executor origin run=%s, persisted=%s", executor.request.OriginRunID, result.OriginRunID)
		}
	})

	t.Run("failure", func(t *testing.T) {
		repo.intentWrites, executor.calls = 0, 0
		store := &plannedOriginStore{err: errors.New("transaction rolled back")}
		service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: store, Runs: &completedLoserRunRepo{}, Prices: prices, Executor: executor, Now: func() time.Time { return now }})
		result, err := service.Rebalance(context.Background(), subscription.ID)
		if err == nil || result != nil {
			t.Fatalf("Rebalance() = (%+v, %v), want atomic failure", result, err)
		}
		if repo.intentWrites != 0 || executor.calls != 0 {
			t.Fatalf("effects after atomic failure: intent writes=%d orders=%d", repo.intentWrites, executor.calls)
		}
	})

	t.Run("registered received intent resumes", func(t *testing.T) {
		repo.intentWrites, executor.calls = 0, 0
		store := &plannedOriginStore{replayed: true}
		service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: store, Prices: prices, Executor: executor, Now: func() time.Time { return now }})
		result, err := service.Rebalance(context.Background(), subscription.ID)
		if err != nil || len(result.Intents) != 1 || executor.calls != 1 {
			t.Fatalf("Rebalance() = (%+v, %v), executor calls=%d", result, err, executor.calls)
		}
	})

	for _, retry := range []struct{ status, risk string }{{"ordered", "approved"}, {"partial", "approved"}, {"failed", "pending"}} {
		t.Run("registered "+retry.status+" intent resumes", func(t *testing.T) {
			repo.intentWrites, executor.calls = 0, 0
			store := &plannedOriginStore{replayed: true, status: retry.status, risk: retry.risk}
			service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: store, Prices: prices, Executor: executor, Now: func() time.Time { return now }})
			result, err := service.Rebalance(context.Background(), subscription.ID)
			if err != nil || len(result.Intents) != 1 || executor.calls != 1 {
				t.Fatalf("Rebalance() = (%+v, %v), executor calls=%d", result, err, executor.calls)
			}
		})
	}

	t.Run("foreign registered intent is rejected before execution", func(t *testing.T) {
		repo.intentWrites, executor.calls = 0, 0
		service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: &plannedOriginStore{foreign: true}, Prices: prices, Executor: executor, Now: func() time.Time { return now }})
		_, err := service.Rebalance(context.Background(), subscription.ID)
		if err == nil || !strings.Contains(err.Error(), "intent ownership") {
			t.Fatalf("Rebalance() error = %v, want intent ownership rejection", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls=%d, want 0", executor.calls)
		}
	})

	t.Run("subscription stop after claim prevents child order", func(t *testing.T) {
		repo.stopAfterClaim, executor.calls = true, 0
		defer func() { repo.stopAfterClaim = false }()
		service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: &plannedOriginStore{}, Prices: prices, Executor: executor, Now: func() time.Time { return now }})
		_, err := service.Rebalance(context.Background(), subscription.ID)
		if err == nil || !strings.Contains(err.Error(), "reauthorize claimed copy intent") {
			t.Fatalf("Rebalance() error=%v", err)
		}
		if executor.calls != 0 {
			t.Fatalf("executor calls=%d", executor.calls)
		}
	})

	t.Run("infrastructure failure remains pending risk", func(t *testing.T) {
		repo.completed = nil
		executeErr := errors.New("executor unavailable")
		service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: &plannedOriginStore{}, Prices: prices, Executor: &resultCopyExecutor{err: executeErr}, Now: func() time.Time { return now }})
		result, rebalanceErr := service.Rebalance(context.Background(), subscription.ID)
		if rebalanceErr != nil || len(result.Intents) != 1 || repo.completed == nil {
			t.Fatalf("Rebalance() = (%+v, %v), completed=%+v", result, rebalanceErr, repo.completed)
		}
		if repo.completed.Status != "failed" || repo.completed.RiskStatus != "pending" || len(repo.completed.RiskReasons) != 1 || repo.completed.RiskReasons[0] != executeErr.Error() {
			t.Fatalf("completed intent=%+v", repo.completed)
		}
	})

	for _, terminal := range []struct {
		status     domain.OrderStatus
		wantStatus string
		wantRisk   string
	}{{domain.OrderStatusFilled, "filled", "approved"}, {domain.OrderStatusRejected, "failed", "rejected"}, {domain.OrderStatusCancelled, "failed", "rejected"}} {
		t.Run("terminal order maps before executor error "+terminal.status.String(), func(t *testing.T) {
			repo.completed = nil
			orderID := uuid.New()
			executeErr := errors.New("terminal recovery notice")
			executor := &resultCopyExecutor{result: PaperOrderResult{OrderID: &orderID, Status: terminal.status}, err: executeErr}
			service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: &plannedOriginStore{}, Prices: prices, Executor: executor, Now: func() time.Time { return now }})
			result, rebalanceErr := service.Rebalance(context.Background(), subscription.ID)
			if rebalanceErr != nil || len(result.Intents) != 1 || repo.completed == nil {
				t.Fatalf("Rebalance() = (%+v, %v), completed=%+v", result, rebalanceErr, repo.completed)
			}
			if repo.completed.Status != terminal.wantStatus || repo.completed.RiskStatus != terminal.wantRisk {
				t.Fatalf("completed terminal intent=%+v", repo.completed)
			}
		})
	}
}

func TestPausedReceivedIntentRecoversDurableOrderWithoutProposal(t *testing.T) {
	binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	subscription := domain.DefaultCopySubscription()
	subscription.ID, subscription.AccountID, subscription.Environment = uuid.New(), binding.AccountID(), binding.Environment()
	subscription.OriginType, subscription.OriginID, subscription.Status, subscription.IsPaper = "copy_subscription", subscription.ID, domain.CopySubscriptionPaused, true
	price := 100.0
	intent := domain.CopyTradeIntent{ID: uuid.New(), AccountID: subscription.AccountID, Environment: subscription.Environment, SubscriptionID: subscription.ID, OriginType: subscription.OriginType, OriginID: subscription.OriginID, SourceObservationID: uuid.New(), InstrumentKey: "AAPL", Ticker: "AAPL", Side: domain.OrderSideBuy, RequestedNotional: 1000, ExecutablePrice: &price, CalculationVersion: 1, PolicyStatus: "approved", RiskStatus: "pending", Status: "received"}
	run, err := copyorigin.NewRun(subscription, []domain.CopyTradeIntent{intent})
	if err != nil {
		t.Fatal(err)
	}
	repo := &cancellationRaceCopyRepo{subscription: subscription, observation: domain.CopySourceObservation{ID: intent.SourceObservationID}, mapping: domain.CopyInstrumentMapping{Ticker: intent.Ticker}, intent: &intent}
	executor := &recoveringCopyExecutor{found: true}
	lifecycle := &countingCopyLifecycle{}
	service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, Executor: executor, Lifecycle: lifecycle})
	result, err := service.executePlannedRun(context.Background(), &subscription, run, []copyorigin.PlannedIntent{{Intent: intent}}, Preview{Intents: []domain.CopyTradeIntent{intent}})
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || lifecycle.calls != 0 || result.Intents[0].Status != "ordered" {
		t.Fatalf("recovery calls=%d proposals=%d result=%+v", executor.calls, lifecycle.calls, result.Intents)
	}
}

func TestSyncResumesUnfinishedRunsBeforeNewFilingWork(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	binding, _ := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	subscription := domain.DefaultCopySubscription()
	subscription.ID, subscription.SourceID = uuid.New(), uuid.New()
	subscription.AccountID, subscription.Environment = binding.AccountID(), binding.Environment()
	subscription.OriginType, subscription.OriginID, subscription.Status = "copy_subscription", subscription.ID, domain.CopySubscriptionPaperActive
	observationID := uuid.New()
	repo := &cancellationRaceCopyRepo{subscription: subscription, observation: domain.CopySourceObservation{ID: observationID}, mapping: domain.CopyInstrumentMapping{Ticker: "AAPL"}, listErr: errors.New("new filing scan unavailable")}
	store := &plannedOriginStore{}
	for i, state := range []struct{ status, risk string }{{"received", "pending"}, {"ordered", "approved"}, {"partial", "approved"}, {"failed", "pending"}} {
		intent := domain.CopyTradeIntent{ID: uuid.New(), AccountID: binding.AccountID(), Environment: binding.Environment(), SubscriptionID: subscription.ID, OriginType: "copy_subscription", OriginID: subscription.ID, SourceObservationID: observationID, InstrumentKey: fmt.Sprintf("AAPL-%d", i), Ticker: "AAPL", Side: domain.OrderSideBuy, RequestedNotional: 100, CalculationVersion: CalculationVersion, PolicyStatus: "approved", RiskStatus: state.risk, Status: state.status}
		run, err := copyorigin.NewRun(subscription, []domain.CopyTradeIntent{intent})
		if err != nil {
			t.Fatal(err)
		}
		store.recoverable = append(store.recoverable, copyorigin.RecoverableRun{Run: run, SubscriptionID: subscription.ID, Intents: []copyorigin.PlannedIntent{{Intent: intent}}})
	}
	executor := &countingCopyExecutor{}
	service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, OriginRuns: store, Executor: executor, Now: func() time.Time { return now }})
	_, err := service.Sync13FSubscriptions(context.Background())
	if err == nil || !strings.Contains(err.Error(), "new filing scan unavailable") {
		t.Fatalf("Sync13FSubscriptions() error=%v", err)
	}
	if executor.calls != 4 {
		t.Fatalf("resumed executions=%d, want 4 before filing scan", executor.calls)
	}
}

func TestInactiveSubscriptionRecoversOnlyEffectfulIntents(t *testing.T) {
	binding, _ := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	base := domain.DefaultCopySubscription()
	base.ID, base.SourceID = uuid.New(), uuid.New()
	base.AccountID, base.Environment = binding.AccountID(), binding.Environment()
	base.OriginType, base.OriginID, base.Status = "copy_subscription", base.ID, domain.CopySubscriptionPaperActive
	price := 100.0
	for _, tc := range []struct {
		status    string
		effectful bool
		wantCalls int
	}{{"received", false, 0}, {"ordered", true, 1}, {"partial", true, 1}} {
		t.Run(tc.status, func(t *testing.T) {
			intent := domain.CopyTradeIntent{ID: uuid.New(), AccountID: binding.AccountID(), Environment: binding.Environment(), SubscriptionID: base.ID, OriginType: "copy_subscription", OriginID: base.ID, SourceObservationID: uuid.New(), InstrumentKey: "AAPL", Ticker: "AAPL", Side: domain.OrderSideBuy, RequestedNotional: 100, ExecutablePrice: &price, CalculationVersion: CalculationVersion, PolicyStatus: "approved", RiskStatus: "approved", Status: tc.status}
			if tc.effectful {
				id := uuid.New()
				intent.OrderID = &id
			}
			run, err := copyorigin.NewRun(base, []domain.CopyTradeIntent{intent})
			if err != nil {
				t.Fatal(err)
			}
			inactive := base
			inactive.Status = domain.CopySubscriptionPaused
			repo := &cancellationRaceCopyRepo{subscription: inactive, observation: domain.CopySourceObservation{ID: intent.SourceObservationID}, mapping: domain.CopyInstrumentMapping{Ticker: "AAPL"}, intent: &intent}
			executor := &countingCopyExecutor{}
			service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, Executor: executor, Now: time.Now})
			if _, err := service.executePlannedRun(context.Background(), &inactive, run, []copyorigin.PlannedIntent{{Intent: intent}}, Preview{}); err != nil {
				t.Fatal(err)
			}
			if executor.calls != tc.wantCalls {
				t.Fatalf("inactive %s executor calls=%d, want %d", tc.status, executor.calls, tc.wantCalls)
			}
		})
	}
}

// Planning failure is covered by TestOriginNativeRebalanceUsesAtomicPlanningBoundary.
// These are the current claim/completion contracts replacing the removed pipeline-finalization tests.
func TestPlannedCopyIntentClaimAndFailurePersistence(t *testing.T) {
	claimErr := errors.New("claim unavailable")
	updateErr := errors.New("completion unavailable")
	executionErr := errors.New("execution unavailable")
	for _, tc := range []struct {
		name                                                   string
		denied, revoked, completionLost                        bool
		claimError, completionError, executionError, wantError error
		wantExecution, wantCompletion                          bool
	}{
		{name: "claim already owned", denied: true},
		{name: "claim write fails", claimError: claimErr, wantError: claimErr},
		{name: "claim revoked before execution", revoked: true, wantError: repository.ErrNotFound},
		{name: "execution failure is durably retryable", executionError: executionErr, wantExecution: true, wantCompletion: true},
		{name: "completion write fails", completionError: updateErr, wantExecution: true, wantError: updateErr},
		{name: "execution and completion fail", executionError: executionErr, completionError: updateErr, wantExecution: true, wantError: updateErr},
		{name: "completion claim lost", completionLost: true, wantExecution: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
			if err != nil {
				t.Fatal(err)
			}
			subscription := domain.DefaultCopySubscription()
			subscription.ID, subscription.SourceID = uuid.New(), uuid.New()
			subscription.AccountID, subscription.Environment = binding.AccountID(), binding.Environment()
			subscription.OriginType, subscription.OriginID, subscription.Status = "copy_subscription", subscription.ID, domain.CopySubscriptionPaperActive
			intent := domain.CopyTradeIntent{ID: uuid.New(), AccountID: binding.AccountID(), Environment: binding.Environment(), SubscriptionID: subscription.ID, OriginType: "copy_subscription", OriginID: subscription.ID, SourceObservationID: uuid.New(), InstrumentKey: "AAPL", Ticker: "AAPL", Side: domain.OrderSideBuy, RequestedNotional: 100, CalculationVersion: CalculationVersion, PolicyStatus: "approved", RiskStatus: "pending", Status: "received"}
			run, err := copyorigin.NewRun(subscription, []domain.CopyTradeIntent{intent})
			if err != nil {
				t.Fatal(err)
			}
			repo := &cancellationRaceCopyRepo{subscription: subscription, intent: &intent, claimDenied: tc.denied, claimErr: tc.claimError, stopAfterClaim: tc.revoked, completeErr: tc.completionError, completeLost: tc.completionLost}
			executor := &resultCopyExecutor{err: tc.executionError}
			lifecycle := &countingCopyLifecycle{}
			service := NewService(ServiceDeps{ExecutionAccount: binding, Repo: repo, Executor: executor, Lifecycle: lifecycle})
			_, err = service.executePlannedRun(t.Context(), &subscription, run, []copyorigin.PlannedIntent{{Intent: intent}}, Preview{})
			if tc.completionLost {
				if err == nil || !strings.Contains(err.Error(), "applied=false") {
					t.Fatalf("lost completion: %v", err)
				}
			} else if !errors.Is(err, tc.wantError) {
				t.Fatalf("error=%v, want %v", err, tc.wantError)
			}
			wantCalls := 0
			if tc.wantExecution {
				wantCalls = 1
			}
			if executor.calls != wantCalls || lifecycle.calls != wantCalls {
				t.Fatalf("executor=%d lifecycle=%d want=%d", executor.calls, lifecycle.calls, wantCalls)
			}
			if (repo.completed != nil) != tc.wantCompletion {
				t.Fatalf("persisted=%+v", repo.completed)
			}
			if tc.wantCompletion && (repo.completed.Status != "failed" || repo.completed.RiskStatus != "pending" || len(repo.completed.RiskReasons) != 1 || repo.completed.RiskReasons[0] != executionErr.Error()) {
				t.Fatalf("retryable failure=%+v", repo.completed)
			}
		})
	}
}

package copytrading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/copyorigin"
	"github.com/PatrickFanella/get-rich-quick/internal/data/edgar"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
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
	return true, nil
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

type cancellationWinnerRunRepo struct {
	repository.PipelineRunRepository
	winner domain.PipelineRun
	cancel context.CancelCauseFunc
	calls  int
}

func (*cancellationWinnerRunRepo) Create(context.Context, *domain.PipelineRun) error { return nil }

func (r *cancellationWinnerRunRepo) Finalize(ctx context.Context, ref domain.PipelineRunRef, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	r.calls++
	if r.calls == 1 && r.cancel != nil && finalization.Status == domain.PipelineStatusCompleted {
		r.cancel(runcontrol.Operator)
		<-ctx.Done()
		return repository.PipelineRunFinalizationReceipt{}, ctx.Err()
	}
	r.winner.ID, r.winner.TradeDate = ref.ID, ref.TradeDate
	return repository.PipelineRunFinalizationReceipt{Applied: true, Run: r.winner}, nil
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

type effectCopyRepo struct {
	cancellationRaceCopyRepo
	createErr error
	updateErr error
	intents   map[uuid.UUID]domain.CopyTradeIntent
}

func (r *effectCopyRepo) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	return fn()
}

func (r *effectCopyRepo) CreateIntent(_ context.Context, intent *domain.CopyTradeIntent) (bool, error) {
	r.intentWrites++
	if r.createErr != nil {
		return false, r.createErr
	}
	if r.intents == nil {
		r.intents = make(map[uuid.UUID]domain.CopyTradeIntent)
	}
	if _, exists := r.intents[intent.ID]; exists {
		return false, nil
	}
	r.intents[intent.ID] = *intent
	return true, nil
}

func (r *effectCopyRepo) UpdateIntent(_ context.Context, intent *domain.CopyTradeIntent) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.intents[intent.ID] = *intent
	return nil
}

type authorizedRunRepo struct {
	repository.PipelineRunRepository
	finalization repository.PipelineRunFinalization
	calls        int
}

func (*authorizedRunRepo) Create(context.Context, *domain.PipelineRun) error { return nil }

func (r *authorizedRunRepo) Finalize(_ context.Context, ref domain.PipelineRunRef, finalization repository.PipelineRunFinalization) (repository.PipelineRunFinalizationReceipt, error) {
	r.calls++
	r.finalization = finalization
	run := domain.PipelineRun{ID: ref.ID, TradeDate: ref.TradeDate, Status: finalization.Status, Signal: *finalization.Signal}
	return repository.PipelineRunFinalizationReceipt{Applied: true, Run: run}, nil
}

type effectEventRepo struct {
	repository.AgentEventRepository
	events []domain.AgentEvent
	err    error
}

func (r *effectEventRepo) Create(_ context.Context, event *domain.AgentEvent) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, *event)
	return nil
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

func TestRebalanceCancellationWinnerPreventsIntentsAndOrders(t *testing.T) {
	t.Skip("legacy pipeline authority removed; copy-origin run owns execution")
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	strategyID := uuid.New()
	subscription := domain.DefaultCopySubscription()
	subscription.ID = uuid.New()
	subscription.SourceID = uuid.New()
	subscription.OriginType, subscription.OriginID = "copy_subscription", subscription.ID
	subscription.Status = domain.CopySubscriptionPaperActive
	binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	subscription.AccountID, subscription.Environment = binding.AccountID(), binding.Environment()
	subscription.LegacyStrategyID = &strategyID
	observation := domain.CopySourceObservation{ID: uuid.New()}
	repo := &cancellationRaceCopyRepo{
		subscription: subscription,
		observation:  observation,
		snapshot: domain.CopyPortfolioSnapshot{
			TotalDisclosedValue: 1000,
			Holdings:            []domain.CopyPortfolioHolding{{CUSIP: "123456789", DisclosedValue: 1000}},
		},
		mapping: domain.CopyInstrumentMapping{IdentifierValue: "123456789", Ticker: "AAPL", Confidence: "provider_verified"},
	}
	availableAt := now.Add(-time.Second)
	prices := cancellationRacePrices{snapshot: PriceSnapshot{
		Ticker: "AAPL", QuoteSnapshotID: uuid.New(), Bid: "99", Ask: "100", AvailableAt: &availableAt,
		MarketStatus: "open", SessionStatus: "regular", AvgDollarVolume: 1_000_000_000,
	}}
	ctx, cancel := context.WithCancelCause(context.Background())
	runs := &cancellationWinnerRunRepo{winner: domain.PipelineRun{Status: domain.PipelineStatusCancelled, Signal: domain.PipelineSignalHold, ErrorMessage: "operator cancelled"}, cancel: cancel}
	executor := &countingCopyExecutor{}
	service := NewService(ServiceDeps{Repo: repo, OriginRuns: &plannedOriginStore{}, Runs: runs, Prices: prices, Executor: executor, Now: func() time.Time { return now }})

	result, err := service.Rebalance(ctx, subscription.ID)
	if err == nil {
		t.Fatal("Rebalance() error = nil, want terminal-authority conflict")
	}
	if result == nil || result.Run.Status != domain.PipelineStatusCancelled || result.Run.ErrorMessage != "operator cancelled" {
		t.Fatalf("Rebalance() result = %+v, want canonical cancellation winner", result)
	}
	if repo.intentWrites != 0 || executor.calls != 0 || len(result.Intents) != 0 {
		t.Fatalf("effects after cancellation: intent writes=%d order calls=%d result intents=%d", repo.intentWrites, executor.calls, len(result.Intents))
	}
	if runs.calls != 2 {
		t.Fatalf("finalize calls=%d, want completed attempt plus detached cancellation", runs.calls)
	}
}

func TestRebalanceCompletedWinnerLoserPreventsIntentsAndOrders(t *testing.T) {
	t.Skip("legacy pipeline authority removed; copy-origin run owns execution")
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	strategyID := uuid.New()
	subscription := domain.DefaultCopySubscription()
	subscription.ID, subscription.SourceID = uuid.New(), uuid.New()
	subscription.OriginType, subscription.OriginID = "copy_subscription", subscription.ID
	subscription.Status, subscription.LegacyStrategyID = domain.CopySubscriptionPaperActive, &strategyID
	repo := &cancellationRaceCopyRepo{
		subscription: subscription,
		observation:  domain.CopySourceObservation{ID: uuid.New()},
		snapshot:     domain.CopyPortfolioSnapshot{TotalDisclosedValue: 1000, Holdings: []domain.CopyPortfolioHolding{{CUSIP: "123456789", DisclosedValue: 1000}}},
		mapping:      domain.CopyInstrumentMapping{IdentifierValue: "123456789", Ticker: "AAPL", Confidence: "provider_verified"},
	}
	availableAt := now.Add(-time.Second)
	prices := cancellationRacePrices{snapshot: PriceSnapshot{Ticker: "AAPL", QuoteSnapshotID: uuid.New(), Bid: "99", Ask: "100", AvailableAt: &availableAt, MarketStatus: "open", SessionStatus: "regular", AvgDollarVolume: 1_000_000_000}}
	executor := &countingCopyExecutor{}
	service := NewService(ServiceDeps{Repo: repo, OriginRuns: &plannedOriginStore{}, Runs: &completedLoserRunRepo{}, Prices: prices, Executor: executor, Now: func() time.Time { return now }})

	result, err := service.Rebalance(context.Background(), subscription.ID)
	if err == nil || !strings.Contains(err.Error(), "lost terminal authority") {
		t.Fatalf("Rebalance() error = %v, want lost terminal authority", err)
	}
	if result == nil || result.Run.Status != domain.PipelineStatusCompleted {
		t.Fatalf("Rebalance() result = %+v, want canonical completed winner", result)
	}
	if repo.intentWrites != 0 || executor.calls != 0 || len(result.Intents) != 0 {
		t.Fatalf("loser effects: intent writes=%d order calls=%d result intents=%d", repo.intentWrites, executor.calls, len(result.Intents))
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

func newLegacyEffectService(repo *effectCopyRepo, runs *authorizedRunRepo, events *effectEventRepo, executor PaperOrderExecutor) (*Service, uuid.UUID) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	strategyID := uuid.New()
	subscription := domain.DefaultCopySubscription()
	subscription.ID, subscription.SourceID = uuid.New(), uuid.New()
	subscription.OriginType, subscription.OriginID = "copy_subscription", subscription.ID
	subscription.Status, subscription.LegacyStrategyID = domain.CopySubscriptionPaperActive, &strategyID
	subscription.MaxSpreadBPS = 200
	repo.subscription = subscription
	repo.observation = domain.CopySourceObservation{ID: uuid.New()}
	repo.snapshot = domain.CopyPortfolioSnapshot{TotalDisclosedValue: 1000, Holdings: []domain.CopyPortfolioHolding{{CUSIP: "123456789", DisclosedValue: 1000}}}
	repo.mapping = domain.CopyInstrumentMapping{IdentifierValue: "123456789", Ticker: "AAPL", Confidence: "provider_verified"}
	availableAt := now.Add(-time.Second)
	prices := cancellationRacePrices{snapshot: PriceSnapshot{Ticker: "AAPL", QuoteSnapshotID: uuid.New(), Bid: "99", Ask: "100", AvailableAt: &availableAt, MarketStatus: "open", SessionStatus: "regular", AvgDollarVolume: 1_000_000_000}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(ServiceDeps{Repo: repo, OriginRuns: &plannedOriginStore{}, Runs: runs, Events: events, Prices: prices, Executor: executor, Logger: logger, Now: func() time.Time { return now }}), subscription.ID
}

func decodeEventMetadata(t *testing.T, event domain.AgentEvent) map[string]any {
	t.Helper()
	var metadata map[string]any
	if err := json.Unmarshal(event.Metadata, &metadata); err != nil {
		t.Fatalf("decode event metadata: %v", err)
	}
	return metadata
}

func TestRecordEffectFailureStoresFullRunRef(t *testing.T) {
	events := &effectEventRepo{}
	service := NewService(ServiceDeps{Events: events})
	run := domain.PipelineRun{ID: uuid.New(), TradeDate: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC), StrategyID: uuid.New()}
	intent := domain.CopyTradeIntent{ID: uuid.New()}

	if err := service.recordEffectFailure(context.Background(), run, intent, effectFailure{stage: "execute_order", err: errors.New("failed")}); err != nil {
		t.Fatalf("recordEffectFailure() error = %v", err)
	}
	if len(events.events) != 1 {
		t.Fatalf("events = %d, want 1", len(events.events))
	}
	event := events.events[0]
	if event.PipelineRunID == nil || *event.PipelineRunID != run.ID || event.PipelineRunTradeDate == nil || !event.PipelineRunTradeDate.Equal(run.TradeDate) {
		t.Fatalf("event run ref = (%v, %v), want (%s, %s)", event.PipelineRunID, event.PipelineRunTradeDate, run.ID, run.TradeDate)
	}
}

func TestRebalanceCompletedAuthorityCreateIntentFailure(t *testing.T) {
	t.Skip("legacy post-finalization intent creation removed")
	createErr := errors.New("intent insert failed")
	repo := &effectCopyRepo{createErr: createErr}
	runs, events, executor := &authorizedRunRepo{}, &effectEventRepo{}, &resultCopyExecutor{}
	service, subscriptionID := newLegacyEffectService(repo, runs, events, executor)

	result, err := service.Rebalance(context.Background(), subscriptionID)
	if !errors.Is(err, createErr) {
		t.Fatalf("Rebalance() error = %v, want create error", err)
	}
	if result == nil || result.Run.Status != domain.PipelineStatusCompleted || runs.calls != 1 {
		t.Fatalf("result=%+v finalize calls=%d", result, runs.calls)
	}
	if executor.calls != 0 || len(events.events) != 1 {
		t.Fatalf("executor calls=%d failure events=%d", executor.calls, len(events.events))
	}
	failureMetadata := decodeEventMetadata(t, events.events[0])
	if events.events[0].EventKind != "copy_rebalance_effects_failed" || failureMetadata["stage"] != "create_intent" {
		t.Fatalf("failure event=%+v metadata=%v", events.events[0], failureMetadata)
	}
	if runs.finalization.Event == nil || runs.finalization.Event.Title != "Copy plan authorized" {
		t.Fatalf("completion event=%+v", runs.finalization.Event)
	}
	completionMetadata := decodeEventMetadata(t, *runs.finalization.Event)
	if completionMetadata["completion_scope"] != "planning_authority" || completionMetadata["source_observation_id"] != repo.observation.ID.String() || completionMetadata["planned_intent_count"] != float64(1) || completionMetadata["approved_intent_count"] != float64(1) {
		t.Fatalf("completion metadata=%v", completionMetadata)
	}
}

func TestRebalanceSuccessfulOrderUpdateFailureIsNotReplayed(t *testing.T) {
	t.Skip("legacy post-finalization intent creation removed")
	updateErr := errors.New("intent update failed")
	orderID := uuid.New()
	repo := &effectCopyRepo{updateErr: updateErr}
	runs, events := &authorizedRunRepo{}, &effectEventRepo{}
	executor := &resultCopyExecutor{result: PaperOrderResult{OrderID: &orderID, Status: domain.OrderStatusFilled}}
	service, subscriptionID := newLegacyEffectService(repo, runs, events, executor)

	result, err := service.Rebalance(context.Background(), subscriptionID)
	if !errors.Is(err, updateErr) || result == nil || result.Run.Status != domain.PipelineStatusCompleted {
		t.Fatalf("Rebalance() = (%+v, %v)", result, err)
	}
	if executor.calls != 1 || len(events.events) != 1 {
		t.Fatalf("executor calls=%d failure events=%d", executor.calls, len(events.events))
	}
	metadata := decodeEventMetadata(t, events.events[0])
	if metadata["stage"] != "update_intent" || metadata["returned_order_id"] != orderID.String() {
		t.Fatalf("failure metadata=%v", metadata)
	}

	repo.updateErr = nil
	if _, retryErr := service.Rebalance(context.Background(), subscriptionID); retryErr != nil {
		t.Fatalf("retry Rebalance() error = %v", retryErr)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls after retry=%d, want 1", executor.calls)
	}
}

func TestRebalanceExecutionFailurePersistenceOutcomes(t *testing.T) {
	t.Skip("legacy post-finalization intent creation removed")
	executeErr := errors.New("risk engine unavailable")

	t.Run("successful update is durable business outcome", func(t *testing.T) {
		repo := &effectCopyRepo{}
		runs, events := &authorizedRunRepo{}, &effectEventRepo{}
		service, subscriptionID := newLegacyEffectService(repo, runs, events, &resultCopyExecutor{err: executeErr})

		result, err := service.Rebalance(context.Background(), subscriptionID)
		if err != nil || result == nil || result.Run.Status != domain.PipelineStatusCompleted || len(result.Intents) != 1 {
			t.Fatalf("Rebalance() = (%+v, %v)", result, err)
		}
		intent := result.Intents[0]
		persisted := repo.intents[intent.ID]
		if persisted.Status != "risk_rejected" || persisted.RiskStatus != "rejected" || len(persisted.RiskReasons) != 1 || persisted.RiskReasons[0] != executeErr.Error() {
			t.Fatalf("persisted intent=%+v", persisted)
		}
		if len(events.events) != 0 {
			t.Fatalf("failure events=%d, want fully represented durably", len(events.events))
		}
	})

	t.Run("update failure event retains execution failure", func(t *testing.T) {
		updateErr := errors.New("risk rejection update failed")
		returnedOrderID := uuid.New()
		repo := &effectCopyRepo{updateErr: updateErr}
		runs, events := &authorizedRunRepo{}, &effectEventRepo{}
		executor := &resultCopyExecutor{result: PaperOrderResult{OrderID: &returnedOrderID}, err: executeErr}
		service, subscriptionID := newLegacyEffectService(repo, runs, events, executor)

		_, err := service.Rebalance(context.Background(), subscriptionID)
		if !errors.Is(err, updateErr) || len(events.events) != 1 {
			t.Fatalf("error=%v failure events=%d", err, len(events.events))
		}
		metadata := decodeEventMetadata(t, events.events[0])
		if metadata["stage"] != "update_intent" || metadata["preceding_failure_stage"] != "execute_order" || metadata["preceding_failure_error"] != executeErr.Error() || metadata["returned_order_id"] != returnedOrderID.String() || metadata["observed_intent_status"] != "risk_rejected" {
			t.Fatalf("failure metadata=%v", metadata)
		}
	})
}

func TestRebalanceFailureEventPersistenceErrorIsJoined(t *testing.T) {
	t.Skip("legacy post-finalization intent creation removed")
	createErr := errors.New("intent insert failed")
	eventErr := errors.New("event insert failed")
	repo := &effectCopyRepo{createErr: createErr}
	runs, events, executor := &authorizedRunRepo{}, &effectEventRepo{err: eventErr}, &resultCopyExecutor{}
	service, subscriptionID := newLegacyEffectService(repo, runs, events, executor)

	result, err := service.Rebalance(context.Background(), subscriptionID)
	if !errors.Is(err, createErr) || !errors.Is(err, eventErr) {
		t.Fatalf("Rebalance() error=%v, want joined effect and event errors", err)
	}
	if result == nil || result.Run.Status != domain.PipelineStatusCompleted || runs.calls != 1 || executor.calls != 0 {
		t.Fatalf("result=%+v finalize calls=%d executor calls=%d", result, runs.calls, executor.calls)
	}
}

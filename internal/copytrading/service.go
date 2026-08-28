package copytrading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/copyorigin"
	"github.com/PatrickFanella/get-rich-quick/internal/data/edgar"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

type ThirteenFFetcher interface {
	FetchLatest13F(ctx context.Context, cik string) (*edgar.ThirteenFFiling, error)
}

type PriceProvider interface {
	Snapshots(ctx context.Context, tickers []string, asOf time.Time) (map[string]PriceSnapshot, error)
}

type PaperOrderRequest struct {
	Scope        execution.ExecutionScope
	Subscription domain.CopySubscription
	Intent       domain.CopyTradeIntent
	OriginRunID  uuid.UUID
	ClaimID      uuid.UUID
}

type PaperOrderResult struct {
	Scope   execution.ExecutionScope
	OrderID *uuid.UUID
	Status  domain.OrderStatus
}

type PaperOrderExecutor interface {
	ExecuteCopyOrder(ctx context.Context, request PaperOrderRequest) (PaperOrderResult, error)
}

type CopyOriginLifecycle interface {
	ProposeCopyIntent(context.Context, domain.CopySubscription, domain.CopyTradeIntent, uuid.UUID) error
}

type ServiceDeps struct {
	ExecutionAccount domain.ExecutionAccountBinding
	Repo             repository.CopyTradingRepository
	OriginRuns       copyorigin.PlannedStore
	Strategies       repository.StrategyRepository
	Runs             repository.PipelineRunRepository
	Events           repository.AgentEventRepository
	RunRegistry      interface {
		Register(uuid.UUID, time.Time, context.CancelCauseFunc) error
		Deregister(uuid.UUID, time.Time)
	}
	Positions repository.PositionRepository
	EDGAR     ThirteenFFetcher
	Prices    PriceProvider
	Executor  PaperOrderExecutor
	Lifecycle CopyOriginLifecycle
	Logger    *slog.Logger
	Now       func() time.Time
}

type Service struct{ deps ServiceDeps }

func NewService(deps ServiceDeps) *Service {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}
}

type LeaderDetail struct {
	Leader  domain.CopyLeader         `json:"leader"`
	Sources []domain.CopyLeaderSource `json:"sources"`
}

func (s *Service) CreateLeader(ctx context.Context, leader *domain.CopyLeader) error {
	if s == nil || s.deps.Repo == nil {
		return fmt.Errorf("copy trading repository is unavailable")
	}
	if err := leader.Validate(); err != nil {
		return err
	}
	return s.deps.Repo.CreateLeader(ctx, leader)
}

func (s *Service) GetLeader(ctx context.Context, id uuid.UUID) (*LeaderDetail, error) {
	leader, err := s.deps.Repo.GetLeader(ctx, id)
	if err != nil {
		return nil, err
	}
	sources, err := s.deps.Repo.ListSourcesByLeader(ctx, id)
	if err != nil {
		return nil, err
	}
	return &LeaderDetail{Leader: *leader, Sources: sources}, nil
}

func (s *Service) ListLeaders(ctx context.Context, filter repository.CopyLeaderFilter, limit, offset int) ([]domain.CopyLeader, int, error) {
	items, err := s.deps.Repo.ListLeaders(ctx, filter, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.deps.Repo.CountLeaders(ctx, filter)
	return items, total, err
}

func (s *Service) AddSource(ctx context.Context, source *domain.CopyLeaderSource) error {
	if err := source.Validate(); err != nil {
		return err
	}
	leader, err := s.deps.Repo.GetLeader(ctx, source.LeaderID)
	if err != nil {
		return err
	}
	if source.SourceType == domain.CopySourceSEC13F && leader.EntityType != domain.CopyLeaderInstitution {
		return fmt.Errorf("sec_13f sources require an institutional leader")
	}
	if (source.SourceType == domain.CopySourceSEC13F || source.SourceType == domain.CopySourceSECForm4) && source.Provider != "sec" {
		return fmt.Errorf("SEC sources require provider=sec")
	}
	return s.deps.Repo.CreateSource(ctx, source)
}

type RefreshResult struct {
	Created     bool                         `json:"created"`
	Observation domain.CopySourceObservation `json:"observation"`
	Snapshot    domain.CopyPortfolioSnapshot `json:"snapshot"`
}

func (s *Service) RefreshSource(ctx context.Context, sourceID uuid.UUID) (*RefreshResult, error) {
	source, err := s.deps.Repo.GetSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if source.SourceType != domain.CopySourceSEC13F {
		return nil, fmt.Errorf("source refresh supports sec_13f only")
	}
	if s.deps.EDGAR == nil {
		return nil, fmt.Errorf("SEC EDGAR provider is unavailable")
	}
	filing, err := s.deps.EDGAR.FetchLatest13F(ctx, source.ExternalKey)
	if err != nil {
		return nil, err
	}
	now := s.deps.Now().UTC()
	payload, _ := json.Marshal(map[string]any{"cik": filing.CIK, "accession": filing.Accession, "form": filing.Form, "holding_count": len(filing.Holdings)})
	observation := domain.CopySourceObservation{SourceID: source.ID, ProviderObservationID: filing.Accession, ObservationKind: "portfolio_snapshot", SchemaVersion: 1, EffectiveAt: filing.ReportPeriod.UTC(), PublishedAt: filing.FiledAt.UTC(), ObservedAt: now, Status: "active", ContentHash: filing.ContentHash, NormalizedPayload: payload, SourceURL: filing.SourceURL}
	if strings.HasSuffix(filing.Form, "/A") {
		observation.AmendmentNumber = 1
		previous, previousSnapshot, getErr := s.deps.Repo.GetLatest13FSnapshot(ctx, source.ID)
		if getErr == nil && previousSnapshot.ReportPeriod.Equal(filing.ReportPeriod) && previous.ProviderObservationID != filing.Accession {
			observation.SupersedesID = &previous.ID
		} else if getErr != nil && !errors.Is(getErr, repository.ErrNotFound) {
			return nil, getErr
		}
	}
	total := 0.0
	for _, holding := range filing.Holdings {
		total += holding.DisclosedValue
	}
	snapshot := domain.CopyPortfolioSnapshot{ReportPeriod: filing.ReportPeriod, TotalDisclosedValue: total, HoldingCount: len(filing.Holdings), Holdings: filing.Holdings}
	created, err := s.deps.Repo.Save13FSnapshot(ctx, &observation, &snapshot)
	if err != nil {
		return nil, err
	}
	checkpoint, _ := json.Marshal(map[string]any{"accession": filing.Accession, "content_hash": filing.ContentHash})
	if err := s.deps.Repo.UpdateSourceObserved(ctx, source.ID, now, checkpoint); err != nil {
		return nil, err
	}
	if err := s.deps.Repo.UpdateLeaderIdentityStatus(ctx, source.LeaderID, domain.CopyIdentityPublicFiling); err != nil {
		return nil, err
	}
	if !created {
		existingObservation, existingSnapshot, getErr := s.deps.Repo.GetLatest13FSnapshot(ctx, source.ID)
		if getErr == nil {
			observation, snapshot = *existingObservation, *existingSnapshot
		}
	}
	return &RefreshResult{Created: created, Observation: observation, Snapshot: snapshot}, nil
}

func (s *Service) UpsertMapping(ctx context.Context, mapping *domain.CopyInstrumentMapping) error {
	if err := mapping.Validate(); err != nil {
		return err
	}
	return s.deps.Repo.UpsertInstrumentMapping(ctx, mapping)
}

func (s *Service) CreateSubscription(ctx context.Context, subscription *domain.CopySubscription) error {
	if subscription == nil {
		return fmt.Errorf("subscription is required")
	}
	// Subscription and origin identity are server-owned. Request JSON cannot
	// select an identity that may collide with an existing attribution graph.
	subscription.ID = uuid.New()
	if err := s.deps.ExecutionAccount.Validate(); err != nil {
		return fmt.Errorf("copy subscription execution account: %w", err)
	}
	subscription.AccountID = s.deps.ExecutionAccount.AccountID()
	subscription.Environment = s.deps.ExecutionAccount.Environment()
	subscription.OriginType, subscription.OriginID = "copy_subscription", subscription.ID
	subscription.LegacyStrategyID = nil
	if err := subscription.Validate(); err != nil {
		return err
	}
	leader, err := s.deps.Repo.GetLeader(ctx, subscription.LeaderID)
	if err != nil {
		return err
	}
	source, err := s.deps.Repo.GetSource(ctx, subscription.SourceID)
	if err != nil {
		return err
	}
	if source.LeaderID != leader.ID {
		return fmt.Errorf("source does not belong to leader")
	}
	if source.SourceType != domain.CopySourceSEC13F {
		return fmt.Errorf("MVP subscriptions require a sec_13f source")
	}
	return s.deps.Repo.CreateSubscription(ctx, subscription)
}

func (s *Service) ListSubscriptions(ctx context.Context, filter repository.CopySubscriptionFilter, limit, offset int) ([]domain.CopySubscription, int, error) {
	items, err := s.deps.Repo.ListSubscriptions(ctx, filter, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.deps.Repo.CountSubscriptions(ctx, filter)
	return items, total, err
}

func (s *Service) GetSubscription(ctx context.Context, id uuid.UUID) (*domain.CopySubscription, error) {
	return s.deps.Repo.GetSubscription(ctx, id)
}

func (s *Service) UpdateSubscription(ctx context.Context, id uuid.UUID, replacement *domain.CopySubscription) (*domain.CopySubscription, error) {
	locker, ok := s.deps.Repo.(repository.ExecutionAccountLocker)
	if !ok {
		return nil, fmt.Errorf("copy subscription update requires execution account locker")
	}
	var result *domain.CopySubscription
	err := locker.WithExecutionAccountLock(ctx, s.deps.ExecutionAccount.AccountID(), func() error {
		var updateErr error
		result, updateErr = s.updateSubscriptionLocked(ctx, id, replacement)
		return updateErr
	})
	return result, err
}

func (s *Service) updateSubscriptionLocked(ctx context.Context, id uuid.UUID, replacement *domain.CopySubscription) (*domain.CopySubscription, error) {
	current, err := s.deps.Repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.validateSubscriptionBinding(current); err != nil {
		return nil, err
	}
	if current.Status != domain.CopySubscriptionDraft && current.Status != domain.CopySubscriptionPreviewed && current.Status != domain.CopySubscriptionPaused {
		return nil, fmt.Errorf("subscription can only be edited while draft, previewed, or paused")
	}
	replacement.ID, replacement.LeaderID, replacement.SourceID = current.ID, current.LeaderID, current.SourceID
	replacement.AccountID, replacement.Environment = current.AccountID, current.Environment
	replacement.LegacyStrategyID, replacement.OriginType, replacement.OriginID = current.LegacyStrategyID, current.OriginType, current.OriginID
	replacement.Status, replacement.IsPaper, replacement.CreatedBy, replacement.CreatedAt = current.Status, true, current.CreatedBy, current.CreatedAt
	if err := replacement.Validate(); err != nil {
		return nil, err
	}
	if err := s.deps.Repo.UpdateSubscription(ctx, replacement); err != nil {
		return nil, err
	}
	return replacement, nil
}

func (s *Service) Preview(ctx context.Context, subscriptionID uuid.UUID) (*Preview, error) {
	locker, ok := s.deps.Repo.(repository.ExecutionAccountLocker)
	if !ok {
		return nil, fmt.Errorf("copy subscription preview requires execution account locker")
	}
	var result *Preview
	err := locker.WithExecutionAccountLock(ctx, s.deps.ExecutionAccount.AccountID(), func() error {
		var previewErr error
		result, previewErr = s.previewLocked(ctx, subscriptionID)
		return previewErr
	})
	return result, err
}

func (s *Service) previewLocked(ctx context.Context, subscriptionID uuid.UUID) (*Preview, error) {
	subscription, err := s.deps.Repo.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	if err := s.validateSubscriptionBinding(subscription); err != nil {
		return nil, err
	}
	observation, snapshot, err := s.deps.Repo.GetLatest13FSnapshot(ctx, subscription.SourceID)
	if err != nil {
		return nil, err
	}
	identifiers := make([]string, 0, len(snapshot.Holdings))
	for _, holding := range snapshot.Holdings {
		identifiers = append(identifiers, holding.CUSIP)
	}
	mappings, err := s.deps.Repo.ListInstrumentMappings(ctx, "sec", "cusip", identifiers)
	if err != nil {
		return nil, err
	}
	tickers := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		tickers = append(tickers, mapping.Ticker)
	}
	prices := map[string]PriceSnapshot{}
	decisionAt := s.deps.Now().UTC().Truncate(time.Microsecond)
	if s.deps.Prices != nil && len(tickers) > 0 {
		prices, err = s.deps.Prices.Snapshots(ctx, tickers, decisionAt)
		if err != nil {
			return nil, fmt.Errorf("copy trading prices: %w", err)
		}
	}
	positions := []domain.Position{}
	if s.deps.Positions != nil {
		if scoped, ok := s.deps.Positions.(repository.ExecutionScopedPositionRepository); ok {
			positions, err = scoped.GetByExecutionScope(ctx, subscription.AccountID, subscription.Environment, subscription.OriginType, subscription.OriginID.String(), repository.PositionFilter{}, 1000, 0)
		} else if subscription.LegacyStrategyID != nil {
			positions, err = s.deps.Positions.GetByStrategy(ctx, *subscription.LegacyStrategyID, repository.PositionFilter{}, 1000, 0)
		} else {
			return nil, fmt.Errorf("copy trading: execution-scoped position repository is required for canonical subscription")
		}
		if err != nil {
			return nil, err
		}
	}
	preview := Build13FTarget(TargetInput{Subscription: *subscription, Observation: *observation, Snapshot: *snapshot, Mappings: mappings, Prices: prices, Positions: positions, DecisionAt: decisionAt})
	if subscription.Status == domain.CopySubscriptionDraft {
		subscription.Status = domain.CopySubscriptionPreviewed
		if err := s.deps.Repo.UpdateSubscription(ctx, subscription); err != nil {
			return nil, err
		}
	}
	return &preview, nil
}

func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, next domain.CopySubscriptionStatus) (*domain.CopySubscription, error) {
	var result *domain.CopySubscription
	run := func() error {
		var err error
		result, err = s.setStatus(ctx, id, next)
		return err
	}
	locker, ok := s.deps.Repo.(repository.ExecutionAccountLocker)
	if !ok {
		return nil, fmt.Errorf("copy subscription status update requires execution account locker")
	}
	if err := locker.WithExecutionAccountLock(ctx, s.deps.ExecutionAccount.AccountID(), run); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) setStatus(ctx context.Context, id uuid.UUID, next domain.CopySubscriptionStatus) (*domain.CopySubscription, error) {
	subscription, err := s.deps.Repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.validateSubscriptionBinding(subscription); err != nil {
		return nil, err
	}
	if err := validateStatusTransition(subscription.Status, next); err != nil {
		return nil, err
	}
	if next == domain.CopySubscriptionPaperActive && subscription.Status == domain.CopySubscriptionDraft {
		if _, err := s.previewLocked(ctx, id); err != nil {
			return nil, fmt.Errorf("activation preview: %w", err)
		}
		subscription, err = s.deps.Repo.GetSubscription(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	subscription.Status = next
	if next == domain.CopySubscriptionStopped {
		now := s.deps.Now().UTC()
		subscription.StoppedAt = &now
	}
	if err := s.deps.Repo.UpdateSubscription(ctx, subscription); err != nil {
		return nil, err
	}
	if s.deps.Strategies != nil && subscription.LegacyStrategyID != nil {
		strategy, getErr := s.deps.Strategies.Get(ctx, *subscription.LegacyStrategyID)
		if getErr != nil {
			return nil, getErr
		}
		if next == domain.CopySubscriptionPaperActive {
			strategy.Status = domain.StrategyStatusActive
		} else {
			strategy.Status = domain.StrategyStatusPaused
		}
		if err := s.deps.Strategies.Update(ctx, strategy); err != nil {
			return nil, err
		}
	}
	return subscription, nil
}

func validateStatusTransition(current, next domain.CopySubscriptionStatus) error {
	allowed := map[domain.CopySubscriptionStatus]map[domain.CopySubscriptionStatus]bool{
		domain.CopySubscriptionDraft:       {domain.CopySubscriptionPreviewed: true, domain.CopySubscriptionPaperActive: true, domain.CopySubscriptionStopped: true},
		domain.CopySubscriptionPreviewed:   {domain.CopySubscriptionPaperActive: true, domain.CopySubscriptionStopped: true},
		domain.CopySubscriptionPaperActive: {domain.CopySubscriptionPaused: true, domain.CopySubscriptionStopped: true},
		domain.CopySubscriptionPaused:      {domain.CopySubscriptionPaperActive: true, domain.CopySubscriptionStopped: true},
	}
	if current == next {
		return nil
	}
	if !allowed[current][next] {
		return fmt.Errorf("invalid copy subscription transition %s -> %s", current, next)
	}
	return nil
}

type RebalanceResult struct {
	Run             domain.PipelineRun       `json:"run,omitempty"`
	OriginRunID     uuid.UUID                `json:"origin_run_id,omitempty"`
	OriginRunSHA256 string                   `json:"origin_run_sha256,omitempty"`
	Preview         Preview                  `json:"preview"`
	Intents         []domain.CopyTradeIntent `json:"intents"`
}

type SyncSummary struct {
	Subscriptions  int `json:"subscriptions"`
	SourcesChecked int `json:"sources_checked"`
	NewFilings     int `json:"new_filings"`
	Rebalanced     int `json:"rebalanced"`
}

// Sync13FSubscriptions refreshes each subscribed source once and only
// rebalances active paper subscriptions when that source produced a new
// immutable observation. Paused subscriptions continue collecting filings.
func (s *Service) Sync13FSubscriptions(ctx context.Context) (SyncSummary, error) {
	var summary SyncSummary
	if err := s.resumeUnfinishedRuns(ctx, &summary); err != nil {
		return summary, err
	}
	subscriptions := make([]domain.CopySubscription, 0)
	for offset := 0; ; offset += 100 {
		page, err := s.deps.Repo.ListSubscriptions(ctx, repository.CopySubscriptionFilter{}, 100, offset)
		if err != nil {
			return summary, err
		}
		subscriptions = append(subscriptions, page...)
		if len(page) < 100 {
			break
		}
	}
	summary.Subscriptions = len(subscriptions)
	newSource := make(map[uuid.UUID]bool)
	for _, subscription := range subscriptions {
		if subscription.Status == domain.CopySubscriptionStopped {
			continue
		}
		if _, checked := newSource[subscription.SourceID]; checked {
			continue
		}
		result, err := s.RefreshSource(ctx, subscription.SourceID)
		if err != nil {
			return summary, fmt.Errorf("refresh source %s: %w", subscription.SourceID, err)
		}
		summary.SourcesChecked++
		newSource[subscription.SourceID] = result.Created
		if result.Created {
			summary.NewFilings++
		}
	}
	for _, subscription := range subscriptions {
		if subscription.Status != domain.CopySubscriptionPaperActive || !newSource[subscription.SourceID] {
			continue
		}
		if _, err := s.Rebalance(ctx, subscription.ID); err != nil {
			return summary, fmt.Errorf("rebalance subscription %s: %w", subscription.ID, err)
		}
		summary.Rebalanced++
	}
	return summary, nil
}

func (s *Service) resumeUnfinishedRuns(ctx context.Context, summary *SyncSummary) error {
	store, ok := s.deps.OriginRuns.(copyorigin.RecoveryStore)
	if !ok {
		return nil
	}
	account := s.deps.ExecutionAccount
	if err := account.Validate(); err != nil {
		return fmt.Errorf("resume copy runs: %w", err)
	}
	runs, err := store.ListUnfinishedRuns(ctx, account.AccountID(), account.Environment())
	if err != nil {
		return fmt.Errorf("list unfinished copy runs: %w", err)
	}
	for _, recoverable := range runs {
		if recoverable.Run == nil || recoverable.SubscriptionID == uuid.Nil {
			return fmt.Errorf("resume copy runs: incomplete persisted run identity")
		}
		subscription, err := s.deps.Repo.GetSubscription(ctx, recoverable.SubscriptionID)
		if err != nil {
			return err
		}
		if err := s.validateSubscriptionBinding(subscription); err != nil {
			return err
		}
		preview := Preview{Intents: make([]domain.CopyTradeIntent, 0, len(recoverable.Intents))}
		for _, intent := range recoverable.Intents {
			preview.Intents = append(preview.Intents, intent.Intent)
		}
		if _, err := s.executePlannedRun(ctx, subscription, recoverable.Run, recoverable.Intents, preview); err != nil {
			return fmt.Errorf("resume copy run %s: %w", recoverable.Run.ID(), err)
		}
		if summary != nil {
			summary.Rebalanced++
		}
	}
	return nil
}

func (s *Service) Rebalance(ctx context.Context, id uuid.UUID) (*RebalanceResult, error) {
	locker, ok := s.deps.Repo.(repository.ExecutionAccountLocker)
	if !ok {
		return nil, fmt.Errorf("copy rebalance requires execution account locker")
	}
	var subscription *domain.CopySubscription
	var persistedOrigin *copyorigin.Run
	var registered []copyorigin.PlannedIntent
	var preview Preview
	err := locker.WithExecutionAccountLock(ctx, s.deps.ExecutionAccount.AccountID(), func() error {
		var planErr error
		subscription, persistedOrigin, registered, preview, planErr = s.planRebalanceLocked(ctx, id)
		return planErr
	})
	if err != nil {
		return nil, err
	}
	return s.executePlannedRun(ctx, subscription, persistedOrigin, registered, preview)
}

func (s *Service) planRebalanceLocked(ctx context.Context, id uuid.UUID) (*domain.CopySubscription, *copyorigin.Run, []copyorigin.PlannedIntent, Preview, error) {
	subscription, err := s.deps.Repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, nil, nil, Preview{}, err
	}
	if err := s.validateSubscriptionBinding(subscription); err != nil {
		return nil, nil, nil, Preview{}, err
	}
	if subscription.Status != domain.CopySubscriptionPaperActive || !subscription.IsPaper {
		return nil, nil, nil, Preview{}, fmt.Errorf("subscription must be paper_active")
	}
	if s.deps.OriginRuns == nil {
		return nil, nil, nil, Preview{}, fmt.Errorf("copy origin run repository is unavailable")
	}
	observation, snapshot, err := s.deps.Repo.GetLatest13FSnapshot(ctx, subscription.SourceID)
	if err != nil {
		return nil, nil, nil, Preview{}, err
	}
	if retries, ok := s.deps.OriginRuns.(copyorigin.RetryStore); ok {
		persisted, intents, loadErr := retries.GetPlannedRun(ctx, subscription.ID, observation.ID, CalculationVersion)
		if loadErr == nil {
			preview := Preview{Observation: *observation, Snapshot: *snapshot, Intents: make([]domain.CopyTradeIntent, 0, len(intents))}
			for _, intent := range intents {
				preview.Intents = append(preview.Intents, intent.Intent)
			}
			return subscription, persisted, intents, preview, nil
		}
		if !errors.Is(loadErr, repository.ErrNotFound) {
			return nil, nil, nil, Preview{}, loadErr
		}
	}
	previewValue, err := s.previewLocked(ctx, id)
	if err != nil {
		return nil, nil, nil, Preview{}, err
	}
	planned := append([]domain.CopyTradeIntent(nil), previewValue.Intents...)
	originRun, err := copyorigin.NewRun(*subscription, planned)
	if err != nil {
		return nil, nil, nil, Preview{}, err
	}
	persistedOrigin, registered, err := s.deps.OriginRuns.RegisterPlannedRun(ctx, originRun, planned)
	if err != nil {
		return nil, nil, nil, Preview{}, err
	}
	return subscription, persistedOrigin, registered, *previewValue, nil
}

func (s *Service) executePlannedRun(ctx context.Context, subscription *domain.CopySubscription, persistedOrigin *copyorigin.Run, registered []copyorigin.PlannedIntent, preview Preview) (*RebalanceResult, error) {
	result := &RebalanceResult{OriginRunID: persistedOrigin.ID(), OriginRunSHA256: persistedOrigin.Digest(), Preview: preview, Intents: make([]domain.CopyTradeIntent, 0, len(registered))}
	if s.deps.Executor == nil {
		return result, fmt.Errorf("paper executor is unavailable")
	}
	for _, plannedIntent := range registered {
		candidate := plannedIntent.Intent
		if err := validateCopyIntentOwnership(candidate, *subscription); err != nil {
			return result, err
		}
		retryable := candidate.Status == "received" || candidate.Status == "ordered" || candidate.Status == "partial" || (candidate.Status == "failed" && candidate.RiskStatus == "pending")
		if subscription.Status != domain.CopySubscriptionPaperActive || !subscription.IsPaper {
			retryable = (candidate.Status == "ordered" || candidate.Status == "partial") && candidate.OrderID != nil
		}
		if candidate.PolicyStatus != "approved" || !retryable {
			result.Intents = append(result.Intents, candidate)
			continue
		}
		claimID := uuid.New()
		locked := false
		claimed := false
		run := func() error {
			var claimErr error
			claimed, claimErr = s.deps.Repo.ClaimIntentExecution(ctx, candidate.ID, claimID, s.deps.Now().UTC())
			if claimErr != nil {
				return fmt.Errorf("claim copy intent %s: %w", candidate.ID, claimErr)
			}
			if !claimed {
				return nil
			}
			var runErr error
			candidate, subscription, runErr = s.executeClaimedIntent(ctx, candidate, subscription, persistedOrigin.ID(), claimID, locked)
			return runErr
		}
		if locker, ok := s.deps.Repo.(repository.ExecutionAccountLocker); ok {
			locked = true
			if err := locker.WithExecutionAccountLock(ctx, subscription.AccountID, run); err != nil {
				return result, err
			}
		} else if err := run(); err != nil {
			return result, err
		}
		if !claimed {
			result.Intents = append(result.Intents, candidate)
			continue
		}
		result.Intents = append(result.Intents, candidate)
	}
	return result, nil
}

type lockedCopyExecutor interface {
	ExecuteCopyOrderWithAccountLockHeld(context.Context, PaperOrderRequest) (PaperOrderResult, error)
}

func (s *Service) executeClaimedIntent(ctx context.Context, candidate domain.CopyTradeIntent, subscription *domain.CopySubscription, originRunID, claimID uuid.UUID, lockHeld bool) (domain.CopyTradeIntent, *domain.CopySubscription, error) {
	reauthorized, currentSubscription, err := s.deps.Repo.GetClaimedIntentExecution(ctx, candidate.ID, claimID)
	if err != nil {
		return candidate, subscription, fmt.Errorf("reauthorize claimed copy intent %s: %w", candidate.ID, err)
	}
	candidate, subscription = *reauthorized, currentSubscription
	if err := validateCopyIntentOwnership(candidate, *subscription); err != nil {
		return candidate, subscription, err
	}
	if s.deps.Lifecycle != nil {
		if err := s.deps.Lifecycle.ProposeCopyIntent(ctx, *subscription, candidate, originRunID); err != nil {
			candidate.Status, candidate.RiskStatus, candidate.RiskReasons = "failed", "pending", []string{fmt.Errorf("propose copy common lifecycle: %w", err).Error()}
			completed, completeErr := s.deps.Repo.CompleteIntentExecution(ctx, &candidate, claimID)
			if completeErr != nil || !completed {
				return candidate, subscription, fmt.Errorf("complete copy intent %s after lifecycle failure: applied=%t: %w", candidate.ID, completed, completeErr)
			}
			return candidate, subscription, nil
		}
	}
	scope, err := execution.NewCopyExecutionScope(subscription.AccountID, subscription.Environment, subscription.ID, originRunID)
	if err != nil {
		return candidate, subscription, fmt.Errorf("copy execution scope: %w", err)
	}
	request := PaperOrderRequest{Scope: scope, Subscription: *subscription, Intent: candidate, OriginRunID: originRunID, ClaimID: claimID}
	var executionResult PaperOrderResult
	if executor, ok := s.deps.Executor.(lockedCopyExecutor); lockHeld && ok {
		executionResult, err = executor.ExecuteCopyOrderWithAccountLockHeld(ctx, request)
	} else {
		executionResult, err = s.deps.Executor.ExecuteCopyOrder(ctx, request)
	}
	if validationErr := validatePaperOrderResult(executionResult, scope); (executionResult.OrderID != nil || executionResult.Status.IsValid()) && validationErr != nil {
		candidate.Status, candidate.RiskStatus, candidate.OrderID, candidate.RiskReasons = "failed", "pending", nil, []string{validationErr.Error()}
		if err != nil {
			candidate.RiskReasons = append(candidate.RiskReasons, err.Error())
		}
		completed, completeErr := s.deps.Repo.CompleteIntentExecution(ctx, &candidate, claimID)
		if completeErr != nil || !completed {
			return candidate, subscription, fmt.Errorf("update copy intent %s: applied=%t: %w", candidate.ID, completed, completeErr)
		}
		return candidate, subscription, nil
	}
	candidate.OrderID = executionResult.OrderID
	terminalMapped := false
	if executionResult.Status == domain.OrderStatusFilled {
		candidate.Status, candidate.RiskStatus, terminalMapped = "filled", "approved", true
	} else if executionResult.Status == domain.OrderStatusRejected || executionResult.Status == domain.OrderStatusCancelled {
		candidate.Status, candidate.RiskStatus, terminalMapped = "failed", "rejected", true
	}
	if err != nil && !terminalMapped {
		candidate.Status, candidate.RiskStatus, candidate.RiskReasons = "failed", "pending", []string{err.Error()}
		if candidate.OrderID != nil && (executionResult.Status == domain.OrderStatusPending || executionResult.Status == domain.OrderStatusSubmitted || executionResult.Status == domain.OrderStatusPartial) {
			candidate.Status = "received"
		}
	} else if err == nil && !terminalMapped {
		candidate.RiskStatus = "approved"
		switch executionResult.Status {
		case domain.OrderStatusFilled:
			candidate.Status = "filled"
		case domain.OrderStatusSubmitted:
			candidate.Status = "ordered"
		case domain.OrderStatusPartial, domain.OrderStatusPending:
			candidate.Status = "partial"
		default:
			candidate.Status, candidate.RiskStatus, candidate.RiskReasons = "failed", "pending", []string{"copy executor returned terminal unsuccessful order status " + executionResult.Status.String()}
		}
	} else if err != nil {
		candidate.RiskReasons = []string{err.Error()}
	}
	completed, updateErr := s.deps.Repo.CompleteIntentExecution(ctx, &candidate, claimID)
	if updateErr != nil || !completed {
		return candidate, subscription, fmt.Errorf("update copy intent %s: applied=%t: %w", candidate.ID, completed, updateErr)
	}
	return candidate, subscription, nil
}

func (s *Service) validateSubscriptionBinding(subscription *domain.CopySubscription) error {
	if s == nil || subscription == nil {
		return fmt.Errorf("copy subscription is required")
	}
	if err := s.deps.ExecutionAccount.Validate(); err != nil {
		return fmt.Errorf("copy execution account: %w", err)
	}
	if subscription.AccountID != s.deps.ExecutionAccount.AccountID() || subscription.Environment != s.deps.ExecutionAccount.Environment() {
		return fmt.Errorf("copy subscription account and environment do not match configured execution account")
	}
	return nil
}

func validateCopyIntentOwnership(intent domain.CopyTradeIntent, subscription domain.CopySubscription) error {
	if intent.AccountID != subscription.AccountID || intent.Environment != subscription.Environment || intent.SubscriptionID != subscription.ID || intent.OriginType != "copy_subscription" || intent.OriginID != subscription.ID {
		return fmt.Errorf("copy intent ownership does not match persisted subscription")
	}
	return nil
}

func validatePaperOrderResult(result PaperOrderResult, scope execution.ExecutionScope) error {
	wantType, wantID := scope.Origin()
	gotType, gotID := result.Scope.Origin()
	if result.Scope.AccountID() != scope.AccountID() || result.Scope.Environment() != scope.Environment() || gotType != wantType || gotID != wantID || result.Scope.CopyOriginRunID() != scope.CopyOriginRunID() || result.OrderID == nil || *result.OrderID == uuid.Nil || !result.Status.IsValid() {
		return fmt.Errorf("copy executor returned incomplete order result")
	}
	return nil
}

type effectFailure struct {
	stage           string
	err             error
	returnedOrderID *uuid.UUID
	precedingStage  string
	precedingError  error
}

func effectStage(err error, stage string) string {
	if err == nil {
		return ""
	}
	return stage
}

func (s *Service) recordEffectFailure(ctx context.Context, run domain.PipelineRun, intent domain.CopyTradeIntent, failure effectFailure) error {
	metadata := map[string]any{
		"intent_id":              intent.ID,
		"stage":                  failure.stage,
		"error":                  failure.err.Error(),
		"observed_intent_status": intent.Status,
	}
	if failure.returnedOrderID != nil {
		metadata["returned_order_id"] = *failure.returnedOrderID
	}
	if failure.precedingError != nil {
		metadata["preceding_failure_stage"] = failure.precedingStage
		metadata["preceding_failure_error"] = failure.precedingError.Error()
	}
	encoded, _ := json.Marshal(metadata)
	event := &domain.AgentEvent{
		PipelineRunID:        &run.ID,
		PipelineRunTradeDate: &run.TradeDate,
		StrategyID:           &run.StrategyID,
		EventKind:            "copy_rebalance_effects_failed",
		Title:                "Copy rebalance effects failed",
		Summary:              fmt.Sprintf("Copy intent %s failed during %s", intent.ID, failure.stage),
		Tags:                 []string{"pipeline", "copy_trading", "effects_failed"},
		Metadata:             encoded,
	}
	var err error
	if s.deps.Events == nil {
		err = errors.New("agent event repository is unavailable")
	} else {
		eventCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err = s.deps.Events.Create(eventCtx, event)
		cancel()
	}
	if err == nil {
		return nil
	}
	observabilityErr := fmt.Errorf("persist copy rebalance failure event: %w", err)
	s.deps.Logger.Error("copy rebalance failure event persistence failed", slog.Any("error", observabilityErr), slog.String("intent_id", intent.ID.String()), slog.String("stage", failure.stage))
	return observabilityErr
}

func (s *Service) ListIntents(ctx context.Context, subscriptionID uuid.UUID, limit, offset int) ([]domain.CopyTradeIntent, error) {
	return s.deps.Repo.ListIntents(ctx, subscriptionID, limit, offset)
}

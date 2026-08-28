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
	Subscription domain.CopySubscription
	Intent       domain.CopyTradeIntent
	OriginRunID  uuid.UUID
}

type PaperOrderResult struct {
	OrderID *uuid.UUID
	Status  domain.OrderStatus
}

type PaperOrderExecutor interface {
	ExecuteCopyOrder(ctx context.Context, request PaperOrderRequest) (PaperOrderResult, error)
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
	current, err := s.deps.Repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.Status != domain.CopySubscriptionDraft && current.Status != domain.CopySubscriptionPreviewed && current.Status != domain.CopySubscriptionPaused {
		return nil, fmt.Errorf("subscription can only be edited while draft, previewed, or paused")
	}
	replacement.ID, replacement.LeaderID, replacement.SourceID = current.ID, current.LeaderID, current.SourceID
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
	subscription, err := s.deps.Repo.GetSubscription(ctx, subscriptionID)
	if err != nil {
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
	if s.deps.Positions != nil && subscription.LegacyStrategyID != nil {
		positions, err = s.deps.Positions.GetByStrategy(ctx, *subscription.LegacyStrategyID, repository.PositionFilter{}, 1000, 0)
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
	subscription, err := s.deps.Repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateStatusTransition(subscription.Status, next); err != nil {
		return nil, err
	}
	if next == domain.CopySubscriptionPaperActive && subscription.Status == domain.CopySubscriptionDraft {
		if _, err := s.Preview(ctx, id); err != nil {
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

func (s *Service) Rebalance(ctx context.Context, id uuid.UUID) (*RebalanceResult, error) {
	subscription, err := s.deps.Repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if subscription.Status != domain.CopySubscriptionPaperActive || !subscription.IsPaper {
		return nil, fmt.Errorf("subscription must be paper_active")
	}
	preview, err := s.Preview(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.deps.OriginRuns == nil {
		return nil, fmt.Errorf("copy origin run repository is unavailable")
	}
	planned := append([]domain.CopyTradeIntent(nil), preview.Intents...)
	originRun, err := copyorigin.NewRun(*subscription, planned)
	if err != nil {
		return nil, err
	}
	persistedOrigin, registered, err := s.deps.OriginRuns.RegisterPlannedRun(ctx, originRun, planned)
	if err != nil {
		return nil, err
	}
	result := &RebalanceResult{OriginRunID: persistedOrigin.ID(), OriginRunSHA256: persistedOrigin.Digest(), Preview: *preview, Intents: make([]domain.CopyTradeIntent, 0, len(registered))}
	if s.deps.Executor == nil {
		return result, fmt.Errorf("paper executor is unavailable")
	}
	for _, plannedIntent := range registered {
		candidate := plannedIntent.Intent
		if candidate.PolicyStatus != "approved" || candidate.OrderID != nil || candidate.Status != "received" {
			result.Intents = append(result.Intents, candidate)
			continue
		}
		if claimer, ok := s.deps.Repo.(repository.CopyIntentExecutionClaimer); ok {
			claimID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(persistedOrigin.ID().String()+":"+candidate.ID.String()))
			claimed, claimErr := claimer.ClaimIntentExecution(ctx, candidate.ID, claimID, s.deps.Now().UTC())
			if claimErr != nil {
				return result, fmt.Errorf("claim copy intent %s: %w", candidate.ID, claimErr)
			}
			if !claimed {
				result.Intents = append(result.Intents, candidate)
				continue
			}
		}
		executionResult, executeErr := s.deps.Executor.ExecuteCopyOrder(ctx, PaperOrderRequest{Subscription: *subscription, Intent: candidate, OriginRunID: persistedOrigin.ID()})
		candidate.OrderID = executionResult.OrderID
		if executeErr != nil {
			candidate.Status, candidate.RiskStatus = "risk_rejected", "rejected"
			candidate.RiskReasons = []string{executeErr.Error()}
		} else {
			candidate.Status, candidate.RiskStatus = "ordered", "approved"
			if executionResult.Status == domain.OrderStatusFilled {
				candidate.Status = "filled"
			}
		}
		if updateErr := s.deps.Repo.UpdateIntent(ctx, &candidate); updateErr != nil {
			return result, fmt.Errorf("update copy intent %s: %w", candidate.ID, updateErr)
		}
		result.Intents = append(result.Intents, candidate)
	}
	return result, nil
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
		PipelineRunID: &run.ID,
		StrategyID:    &run.StrategyID,
		EventKind:     "copy_rebalance_effects_failed",
		Title:         "Copy rebalance effects failed",
		Summary:       fmt.Sprintf("Copy intent %s failed during %s", intent.ID, failure.stage),
		Tags:          []string{"pipeline", "copy_trading", "effects_failed"},
		Metadata:      encoded,
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

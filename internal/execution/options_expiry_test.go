package execution_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type recordingOptionSettlementRepo struct {
	inputs      []repository.OptionPositionSettlementInput
	err         error
	lockCalls   int
	retry       bool
	retryChecks int
	resolved    int
	committed   bool
	resolveErr  error
	evidence    int
}

type cancellationSafeSettlementState struct {
	called bool
}

func (r *recordingOptionSettlementRepo) WithExecutionAccountLock(_ context.Context, _ uuid.UUID, fn func() error) error {
	r.lockCalls++
	return fn()
}

func (r *recordingOptionSettlementRepo) HasOptionSettlementSyncRetries(context.Context, uuid.UUID, domain.AccountEnvironment) (bool, error) {
	r.retryChecks++
	return r.retry, nil
}

func (r *recordingOptionSettlementRepo) ResolveOptionSettlementSyncRetries(context.Context, uuid.UUID, domain.AccountEnvironment) error {
	r.resolved++
	r.retry = false
	return nil
}

type rebuildingSettlementState struct {
	rebuildErr error
	rebuilt    int
}

func (*rebuildingSettlementState) ApplyOptionSettlement(context.Context, uuid.UUID, float64) error {
	return nil
}

func (s *rebuildingSettlementState) RebuildOptionSettlementState(context.Context) error {
	s.rebuilt++
	return s.rebuildErr
}

func (s *cancellationSafeSettlementState) ApplyOptionSettlement(ctx context.Context, _ uuid.UUID, _ float64) error {
	s.called = true
	return ctx.Err()
}

func (r *recordingOptionSettlementRepo) SettleOptionPosition(_ context.Context, input repository.OptionPositionSettlementInput) (repository.OptionPositionSettlementResult, error) {
	if r.err != nil {
		return repository.OptionPositionSettlementResult{}, r.err
	}
	r.inputs = append(r.inputs, input)
	return repository.OptionPositionSettlementResult{PositionID: input.PositionID, TradeID: uuid.New()}, nil
}

func (r *recordingOptionSettlementRepo) ResolveOptionSettlementCommit(_ context.Context, input repository.OptionPositionSettlementInput) (repository.OptionPositionSettlementResult, bool, error) {
	return repository.OptionPositionSettlementResult{PositionID: input.PositionID, TradeID: uuid.New()}, r.committed, r.resolveErr
}

func (r *recordingOptionSettlementRepo) RecordOptionSettlementSyncFailure(context.Context, repository.OptionPositionSettlementInput, error) error {
	r.evidence++
	return nil
}

type failingSettlementState struct{}

func (*failingSettlementState) ApplyOptionSettlement(context.Context, uuid.UUID, float64) error {
	return errors.New("broker sync failed")
}

func (*failingSettlementState) RebuildOptionSettlementState(context.Context) error {
	return errors.New("broker rebuild failed")
}

func expiryPosition(symbol, underlying string, optionType domain.OptionType, strike, entry, quantity float64, side domain.PositionSide, expiry time.Time) domain.Position {
	return domain.Position{ID: uuid.New(), Ticker: symbol, Side: side, Quantity: quantity, AvgEntry: entry, AssetClass: domain.AssetClassOption, UnderlyingTicker: underlying, OptionType: &optionType, Strike: &strike, Expiry: &expiry, ContractMultiplier: 100}
}

func stampExpiryPositions(scope execution.ExecutionScope, positions []domain.Position) {
	originType, originID := scope.Origin()
	for i := range positions {
		positions[i].AccountID, positions[i].Environment = scope.AccountID(), scope.Environment()
		positions[i].OriginType, positions[i].OriginID = string(originType), originID
		positions[i].StrategyID = scope.LegacyStrategyID()
	}
}

func TestSettleExpiredOptionPositionsPersistsExerciseAndWorthlessExpiryWithoutFabricatingAssignment(t *testing.T) {
	now := time.Date(2027, 12, 18, 22, 0, 0, 0, time.UTC)
	expiry := time.Date(2027, 12, 17, 0, 0, 0, 0, time.UTC)
	positions := []domain.Position{
		expiryPosition("AAPL271217C00150000", "AAPL", domain.OptionTypeCall, 150, 2, 1, domain.PositionSideLong, expiry),
		expiryPosition("AAPL271217P00140000", "AAPL", domain.OptionTypePut, 140, 1, 2, domain.PositionSideLong, expiry),
	}
	scope := optionExecutionScope(uuid.New(), uuid.New())
	stampExpiryPositions(scope, positions)
	settlementRepo := &recordingOptionSettlementRepo{}
	summary, err := execution.SettleExpiredOptionPositions(context.Background(), scope, positions, map[execution.OptionExpiryPriceKey]float64{execution.NewOptionExpiryPriceKey("AAPL", expiry): 155}, now, settlementRepo)
	if err != nil {
		t.Fatalf("SettleExpiredOptionPositions() error = %v", err)
	}
	if summary.CashSettled != 1 || summary.ExpiredWorthless != 1 || len(settlementRepo.inputs) != 2 {
		t.Fatalf("unexpected settlement summary=%+v atomic_calls=%d", summary, len(settlementRepo.inputs))
	}
	if settlementRepo.lockCalls != 1 {
		t.Fatalf("execution account lock calls = %d, want exactly one", settlementRepo.lockCalls)
	}
	if math.Abs(settlementRepo.inputs[0].SettlementPrice-5) > 1e-9 || settlementRepo.inputs[0].ExitReason != "exercise_cash_settled" {
		t.Fatalf("ITM settlement incorrect: %+v", settlementRepo.inputs[0])
	}
	if settlementRepo.inputs[1].SettlementPrice != 0 || settlementRepo.inputs[1].ExitReason != "expired_worthless" {
		t.Fatalf("OTM settlement incorrect: %+v", settlementRepo.inputs[1])
	}
}

func TestSettleExpiredOptionPositionsRebuildsPendingSyncRetryWithoutOpenCandidate(t *testing.T) {
	scope := optionExecutionScope(uuid.New(), uuid.New())
	repo := &recordingOptionSettlementRepo{retry: true}
	state := &rebuildingSettlementState{}
	summary, err := execution.SettleExpiredOptionPositions(context.Background(), scope, nil, nil, time.Now(), repo, state)
	if err != nil {
		t.Fatal(err)
	}
	if summary != (execution.OptionsExpirySummary{}) || state.rebuilt != 1 || repo.resolved != 1 || repo.retryChecks != 1 {
		t.Fatalf("retry recovery summary=%+v rebuilt=%d resolved=%d checks=%d", summary, state.rebuilt, repo.resolved, repo.retryChecks)
	}
}

func TestSettleExpiredOptionPositionsKeepsRetryEvidenceWhenRebuildFails(t *testing.T) {
	scope := optionExecutionScope(uuid.New(), uuid.New())
	repo := &recordingOptionSettlementRepo{retry: true}
	state := &rebuildingSettlementState{rebuildErr: errors.New("restore failed")}
	_, err := execution.SettleExpiredOptionPositions(context.Background(), scope, nil, nil, time.Now(), repo, state)
	if err == nil || repo.resolved != 0 || !repo.retry {
		t.Fatalf("failed rebuild err=%v resolved=%d retry=%v", err, repo.resolved, repo.retry)
	}
}

func TestSettleExpiredOptionPositionsValidatesBatchBeforeMutation(t *testing.T) {
	now := time.Date(2027, 12, 18, 22, 0, 0, 0, time.UTC)
	expiry := now.Add(-24 * time.Hour)
	positions := []domain.Position{expiryPosition("AAPL271217C00150000", "AAPL", domain.OptionTypeCall, 150, 2, 1, domain.PositionSideLong, expiry), expiryPosition("MSFT271217C00300000", "MSFT", domain.OptionTypeCall, 300, 2, 1, domain.PositionSideLong, expiry)}
	scope := optionExecutionScope(uuid.New(), uuid.New())
	stampExpiryPositions(scope, positions)
	settlementRepo := &recordingOptionSettlementRepo{}
	_, err := execution.SettleExpiredOptionPositions(context.Background(), scope, positions, map[execution.OptionExpiryPriceKey]float64{execution.NewOptionExpiryPriceKey("AAPL", expiry): 155}, now, settlementRepo)
	if err == nil || len(settlementRepo.inputs) != 0 {
		t.Fatalf("invalid batch must fail before mutation: err=%v atomic_calls=%d", err, len(settlementRepo.inputs))
	}
}

func TestSettleExpiredOptionPositionsFinishesPaperCleanupAfterCallerCancellation(t *testing.T) {
	now := time.Date(2027, 12, 18, 22, 0, 0, 0, time.UTC)
	expiry := now.Add(-24 * time.Hour)
	positions := []domain.Position{expiryPosition("AAPL271217C00150000", "AAPL", domain.OptionTypeCall, 150, 2, 1, domain.PositionSideLong, expiry)}
	scope := optionExecutionScope(uuid.New(), uuid.New())
	stampExpiryPositions(scope, positions)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := &cancellationSafeSettlementState{}
	if _, err := execution.SettleExpiredOptionPositions(ctx, scope, positions, map[execution.OptionExpiryPriceKey]float64{execution.NewOptionExpiryPriceKey("AAPL", expiry): 155}, now, &recordingOptionSettlementRepo{}, state); err != nil {
		t.Fatalf("post-commit cleanup inherited caller cancellation: %v", err)
	}
	if !state.called {
		t.Fatal("paper settlement state was not updated")
	}
}

func TestSettleExpiredOptionPositionsSyncsBrokerAfterAmbiguousCommittedSettlement(t *testing.T) {
	now := time.Date(2027, 12, 18, 22, 0, 0, 0, time.UTC)
	expiry := now.Add(-24 * time.Hour)
	positions := []domain.Position{expiryPosition("AAPL271217C00150000", "AAPL", domain.OptionTypeCall, 150, 2, 1, domain.PositionSideLong, expiry)}
	scope := optionExecutionScope(uuid.New(), uuid.New())
	stampExpiryPositions(scope, positions)
	repo := &recordingOptionSettlementRepo{err: errors.New("commit acknowledgement lost"), committed: true}
	state := &cancellationSafeSettlementState{}
	summary, err := execution.SettleExpiredOptionPositions(context.Background(), scope, positions, map[execution.OptionExpiryPriceKey]float64{execution.NewOptionExpiryPriceKey("AAPL", expiry): 155}, now, repo, state)
	if err != nil || !state.called || summary.CashSettled != 1 {
		t.Fatalf("ambiguous committed settlement err=%v sync=%v summary=%+v", err, state.called, summary)
	}
}

func TestSettleExpiredOptionPositionsQueuesSyncRetryAfterAmbiguousCommit(t *testing.T) {
	now := time.Date(2027, 12, 18, 22, 0, 0, 0, time.UTC)
	expiry := now.Add(-24 * time.Hour)
	positions := []domain.Position{expiryPosition("AAPL271217C00150000", "AAPL", domain.OptionTypeCall, 150, 2, 1, domain.PositionSideLong, expiry)}
	scope := optionExecutionScope(uuid.New(), uuid.New())
	stampExpiryPositions(scope, positions)
	repo := &recordingOptionSettlementRepo{err: errors.New("commit acknowledgement lost"), committed: true}
	_, err := execution.SettleExpiredOptionPositions(context.Background(), scope, positions, map[execution.OptionExpiryPriceKey]float64{execution.NewOptionExpiryPriceKey("AAPL", expiry): 155}, now, repo, &failingSettlementState{})
	if err == nil || repo.evidence != 1 {
		t.Fatalf("ambiguous commit sync failure err=%v retry evidence=%d", err, repo.evidence)
	}
}

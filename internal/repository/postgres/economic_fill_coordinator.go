package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// EconomicFillCoordinator is the single PostgreSQL commit boundary for an
// accepted execution. Raw source evidence must already be durable.
type EconomicFillCoordinator struct{ pool *pgxpool.Pool }

var _ execution.EconomicFillCoordinator = (*EconomicFillCoordinator)(nil)

func NewEconomicFillCoordinator(pool *pgxpool.Pool) *EconomicFillCoordinator {
	return &EconomicFillCoordinator{pool: pool}
}

func (coordinator *EconomicFillCoordinator) ApplyAcceptedFill(ctx context.Context, input execution.AcceptedFillInput) (execution.AcceptedFillResult, error) {
	if err := input.Validate(); err != nil {
		return execution.AcceptedFillResult{}, fmt.Errorf("postgres: validate accepted fill: %w", err)
	}
	if coordinator == nil || coordinator.pool == nil {
		return execution.AcceptedFillResult{}, fmt.Errorf("postgres: economic fill coordinator runtime pool is required")
	}
	// Validate a replay before opening the write transaction. The locked check
	// below remains authoritative for a new transition.
	lifecycleRepo := NewExecutionLifecycleRepo(coordinator.pool)
	current, err := lifecycleRepo.GetExecutionLifecycle(ctx, input.Scope.AccountID(), input.Transition.Event.IntentID)
	if err != nil {
		return execution.AcceptedFillResult{}, err
	}
	transitionAlreadyApplied := findLifecycleEvent(current.Events, input.Transition.Event.ID) != nil
	if transitionAlreadyApplied {
		existing := findLifecycleEvent(current.Events, input.Transition.Event.ID)
		matches, matchErr := lifecycleRepo.transitionReplayMatches(ctx, current, input.Transition, existing)
		if matchErr != nil {
			return execution.AcceptedFillResult{}, matchErr
		}
		if !matches {
			return execution.AcceptedFillResult{}, fmt.Errorf("postgres: accepted fill replay changed lifecycle payload: %w", repository.ErrIdempotencyConflict)
		}
	}

	tx, err := coordinator.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return execution.AcceptedFillResult{}, fmt.Errorf("postgres: begin accepted fill transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAcceptedFillLifecycle(ctx, tx, input, transitionAlreadyApplied); err != nil {
		return execution.AcceptedFillResult{}, err
	}
	var mutation repository.OrderFillResult
	if input.Mutation.Order != nil {
		if err := validateOrderFillInput(input.Mutation); err != nil {
			return execution.AcceptedFillResult{}, err
		}
		mutation, err = applyOrderFillTx(ctx, tx, input.Mutation)
		if err != nil {
			return execution.AcceptedFillResult{}, err
		}
	}
	if !transitionAlreadyApplied {
		if _, err := lifecycle.ApplyTransition(input.PriorLifecycle, input.Transition); err != nil {
			return execution.AcceptedFillResult{}, fmt.Errorf("postgres: validate accepted lifecycle transition: %w", err)
		}
		if err := lifecycleRepo.insertExecutionFillTransition(ctx, tx, input.Transition); err != nil {
			return execution.AcceptedFillResult{}, err
		}
	}
	asOf := acceptedEconomicAsOf(input.LedgerTransaction.EffectiveAt, input.LedgerTransaction.ObservedAt, input.Mutation.Now)
	if _, err := enqueueEconomicProjectionTx(ctx, tx, input.Scope.AccountID(), input.LedgerTransaction.ID, asOf); err != nil {
		return execution.AcceptedFillResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return execution.AcceptedFillResult{}, fmt.Errorf("postgres: commit accepted fill: %w", err)
	}
	persisted, err := lifecycleRepo.GetExecutionLifecycle(ctx, input.Scope.AccountID(), input.Transition.Event.IntentID)
	if err != nil {
		return execution.AcceptedFillResult{}, fmt.Errorf("postgres: reload accepted fill lifecycle: %w", err)
	}
	replayed := transitionAlreadyApplied
	if input.Mutation.Order != nil {
		replayed = replayed && mutation.Replayed
	}
	return execution.AcceptedFillResult{Mutation: mutation, Lifecycle: persisted, Replayed: replayed}, nil
}

func lockAcceptedFillLifecycle(ctx context.Context, tx pgx.Tx, input execution.AcceptedFillInput, replay bool) error {
	var accountID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT account_id FROM execution_intents WHERE id=$1 FOR UPDATE`, input.Transition.Event.IntentID).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ErrNotFound
		}
		return fmt.Errorf("postgres: lock accepted fill lifecycle: %w", err)
	}
	if accountID != input.Scope.AccountID() {
		return repository.ErrNotFound
	}
	var latestEventID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM execution_lifecycle_events WHERE intent_id=$1 ORDER BY ingest_sequence DESC LIMIT 1`, input.Transition.Event.IntentID).Scan(&latestEventID); err != nil {
		return fmt.Errorf("postgres: load accepted fill lifecycle frontier: %w", err)
	}
	if replay {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM execution_lifecycle_events WHERE intent_id=$1 AND id=$2)`, input.Transition.Event.IntentID, input.Transition.Event.ID).Scan(&exists); err != nil || !exists {
			if err != nil {
				return fmt.Errorf("postgres: verify accepted fill replay: %w", err)
			}
			return fmt.Errorf("postgres: accepted fill replay disappeared during lock")
		}
		return nil
	}
	if len(input.PriorLifecycle.Events) == 0 || input.PriorLifecycle.Events[len(input.PriorLifecycle.Events)-1].ID != latestEventID {
		return fmt.Errorf("postgres: accepted fill lifecycle changed before lock")
	}
	return nil
}

func acceptedEconomicAsOf(values ...time.Time) time.Time {
	var result time.Time
	for _, value := range values {
		value = value.UTC().Truncate(time.Microsecond)
		if value.After(result) {
			result = value
		}
	}
	return result
}

func (coordinator *EconomicFillCoordinator) ApplyAcceptedOptionFills(ctx context.Context, inputs []execution.AcceptedFillInput) ([]execution.AcceptedFillResult, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("postgres: accepted option fills are required")
	}
	if coordinator == nil || coordinator.pool == nil {
		return nil, fmt.Errorf("postgres: economic fill coordinator runtime pool is required")
	}
	mutations := make([]repository.OptionFillInput, len(inputs))
	replayed := make([]bool, len(inputs))
	lifecycleRepo := NewExecutionLifecycleRepo(coordinator.pool)
	for index := range inputs {
		if err := inputs[index].Validate(); err != nil {
			return nil, fmt.Errorf("postgres: validate accepted option fill %d: %w", index, err)
		}
		if inputs[index].OptionMutation == nil || inputs[index].OptionMutation.StatusOnly {
			return nil, fmt.Errorf("postgres: accepted option fill %d requires an economic option mutation", index)
		}
		mutations[index] = *inputs[index].OptionMutation
		loaded, err := lifecycleRepo.GetExecutionLifecycle(ctx, inputs[index].Scope.AccountID(), inputs[index].Transition.Event.IntentID)
		if err != nil {
			return nil, err
		}
		existing := findLifecycleEvent(loaded.Events, inputs[index].Transition.Event.ID)
		if existing != nil {
			matches, matchErr := lifecycleRepo.transitionReplayMatches(ctx, loaded, inputs[index].Transition, existing)
			if matchErr != nil {
				return nil, matchErr
			}
			if !matches {
				return nil, fmt.Errorf("postgres: accepted option fill replay changed lifecycle payload: %w", repository.ErrIdempotencyConflict)
			}
			replayed[index] = true
		}
	}
	if err := validateOptionFillBatch(mutations); err != nil {
		return nil, err
	}
	tx, err := coordinator.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres: begin accepted option fill transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockOrder := make([]int, len(inputs))
	for index := range lockOrder {
		lockOrder[index] = index
	}
	sort.Slice(lockOrder, func(left, right int) bool {
		return inputs[lockOrder[left]].Transition.Event.IntentID.String() < inputs[lockOrder[right]].Transition.Event.IntentID.String()
	})
	for _, index := range lockOrder {
		if err := lockAcceptedFillLifecycle(ctx, tx, inputs[index], replayed[index]); err != nil {
			return nil, err
		}
	}
	mutationResults, mutationReplay, err := applyOptionFillsTx(ctx, tx, mutations)
	if err != nil {
		return nil, err
	}
	results := make([]execution.AcceptedFillResult, len(inputs))
	for index := range inputs {
		if !replayed[index] {
			if _, err := lifecycle.ApplyTransition(inputs[index].PriorLifecycle, inputs[index].Transition); err != nil {
				return nil, fmt.Errorf("postgres: validate accepted option lifecycle %d: %w", index, err)
			}
			if err := lifecycleRepo.insertExecutionFillTransition(ctx, tx, inputs[index].Transition); err != nil {
				return nil, err
			}
		}
		asOf := acceptedEconomicAsOf(inputs[index].LedgerTransaction.EffectiveAt, inputs[index].LedgerTransaction.ObservedAt, mutations[index].FilledAt)
		if _, err := enqueueEconomicProjectionTx(ctx, tx, inputs[index].Scope.AccountID(), inputs[index].LedgerTransaction.ID, asOf); err != nil {
			return nil, err
		}
		value := mutationResults[index]
		results[index].OptionFill = &value
		results[index].Replayed = replayed[index] && mutationReplay
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: commit accepted option fills: %w", err)
	}
	for index := range inputs {
		persisted, err := lifecycleRepo.GetExecutionLifecycle(ctx, inputs[index].Scope.AccountID(), inputs[index].Transition.Event.IntentID)
		if err != nil {
			return nil, fmt.Errorf("postgres: reload accepted option lifecycle %d: %w", index, err)
		}
		results[index].Lifecycle = persisted
	}
	return results, nil
}

func (coordinator *EconomicFillCoordinator) SettlePredictionDecision(ctx context.Context, input execution.AcceptedPredictionSettlementInput) (repository.PredictionDecisionSettlementResult, error) {
	if err := input.Validate(); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: validate accepted prediction settlement: %w", err)
	}
	if coordinator == nil || coordinator.pool == nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: economic fill coordinator runtime pool is required")
	}
	tx, err := coordinator.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: begin accepted prediction settlement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := settlePredictionDecisionTx(ctx, tx, input.Mutation)
	if err != nil {
		return repository.PredictionDecisionSettlementResult{}, err
	}
	if !result.Replayed {
		if _, err := insertEconomicNormalizationAggregate(ctx, tx, input.Normalization); err != nil {
			return repository.PredictionDecisionSettlementResult{}, err
		}
	}
	asOf := acceptedEconomicAsOf(input.LedgerTransaction.EffectiveAt, input.LedgerTransaction.ObservedAt, input.Mutation.ResolvedAt)
	if _, err := enqueueEconomicProjectionTx(ctx, tx, input.Scope.AccountID(), input.LedgerTransaction.ID, asOf); err != nil {
		return repository.PredictionDecisionSettlementResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return repository.PredictionDecisionSettlementResult{}, fmt.Errorf("postgres: commit accepted prediction settlement: %w", err)
	}
	return result, nil
}

package copyorigin

import (
	"context"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

type Store interface {
	RegisterRun(context.Context, *Run) (*Run, error)
	GetRun(context.Context, uuid.UUID) (*Run, error)
}

// PlannedStore atomically registers one run and its executable intent rows.
type PlannedStore interface {
	Store
	RegisterPlannedRun(context.Context, *Run, []domain.CopyTradeIntent) (*Run, []PlannedIntent, error)
}

type PlannedIntent struct {
	Intent  domain.CopyTradeIntent
	Created bool
}

// RetryStore loads immutable execution evidence before callers regenerate a
// time-sensitive preview for the same source observation.
type RetryStore interface {
	GetPlannedRun(context.Context, uuid.UUID, uuid.UUID, int) (*Run, []PlannedIntent, error)
}

type RecoverableRun struct {
	Run            *Run
	SubscriptionID uuid.UUID
	Intents        []PlannedIntent
}

// RecoveryStore enumerates durable, unfinished effects by execution scope.
// Recovery is independent of source refresh and new run creation.
type RecoveryStore interface {
	ListUnfinishedRuns(context.Context, uuid.UUID, domain.AccountEnvironment) ([]RecoverableRun, error)
}

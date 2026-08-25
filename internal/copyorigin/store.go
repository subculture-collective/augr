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
	RegisterPlannedRun(context.Context, *Run, []domain.CopyTradeIntent) (*Run, []domain.CopyTradeIntent, error)
}

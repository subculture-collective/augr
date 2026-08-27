package domain

import (
	"time"

	"github.com/google/uuid"
)

// PipelineRunRef is the complete identity of a date-partitioned pipeline run.
type PipelineRunRef struct {
	ID        uuid.UUID
	TradeDate time.Time
}

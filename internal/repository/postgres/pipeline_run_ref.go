package postgres

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

func validateOptionalPipelineRunRef(id *uuid.UUID, tradeDate *time.Time) error {
	if id == nil && tradeDate == nil {
		return nil
	}
	if id == nil || tradeDate == nil || *id == uuid.Nil || tradeDate.IsZero() {
		return fmt.Errorf("pipeline run ID and trade date must both be set or both be absent")
	}
	return nil
}

func validatePipelineRunRef(id uuid.UUID, tradeDate time.Time) error {
	if id == uuid.Nil || tradeDate.IsZero() {
		return fmt.Errorf("pipeline run ID and trade date must both be set or both be absent")
	}
	return nil
}

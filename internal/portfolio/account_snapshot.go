package portfolio

import (
	"time"

	"github.com/google/uuid"
)

type AccountSnapshot struct {
	ID                 uuid.UUID
	ObservedAt         time.Time
	Equity             float64
	BuyingPower        float64
	OptionsBuyingPower float64
	FallbackUsed       bool
	// InternalAccount marks the canonical internal paper ledger account whose
	// options buying power is pinned to zero by schema.
	InternalAccount bool
}

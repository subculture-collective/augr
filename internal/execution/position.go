package execution

import (
	"fmt"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// NewPosition creates a position stamped with its canonical execution scope.
func NewPosition(scope ExecutionScope, ticker string, side domain.PositionSide, quantity, avgEntry float64) (*domain.Position, error) {
	if err := validateExecutionAccount(scope.accountID, scope.environment); err != nil {
		return nil, err
	}
	if scope.originType == "" || scope.originID == "" {
		return nil, fmt.Errorf("execution origin is required")
	}
	if ticker == "" {
		return nil, fmt.Errorf("ticker is required")
	}
	if !side.IsValid() {
		return nil, fmt.Errorf("invalid position side: %q", side)
	}
	if quantity <= 0 {
		return nil, fmt.Errorf("quantity must be positive, got %v", quantity)
	}
	if avgEntry <= 0 {
		return nil, fmt.Errorf("avg_entry must be positive, got %v", avgEntry)
	}

	return &domain.Position{
		AccountID:   scope.accountID,
		Environment: scope.environment,
		OriginType:  string(scope.originType),
		OriginID:    scope.originID,
		Ticker:      ticker,
		Side:        side,
		Quantity:    quantity,
		AvgEntry:    avgEntry,
	}, nil
}

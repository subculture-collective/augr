package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PositionSide represents whether a position is long or short.
type PositionSide string

const (
	PositionSideLong  PositionSide = "long"
	PositionSideShort PositionSide = "short"
)

// String returns the string representation of a PositionSide.
func (s PositionSide) String() string {
	return string(s)
}

// IsValid returns true if the side is a defined PositionSide constant.
func (s PositionSide) IsValid() bool {
	switch s {
	case PositionSideLong, PositionSideShort:
		return true
	}
	return false
}

// PositionScope identifies the account and authoritative origin of a position.
type PositionScope struct {
	AccountID   uuid.UUID
	Environment AccountEnvironment
	OriginType  string
	OriginID    string
}

// NewPosition creates a Position in a canonical execution scope.
func NewPosition(scope PositionScope, ticker string, side PositionSide, quantity, avgEntry float64) (*Position, error) {
	if scope.AccountID == uuid.Nil {
		return nil, fmt.Errorf("position account ID is required")
	}
	if !scope.Environment.IsValid() {
		return nil, fmt.Errorf("invalid position environment: %q", scope.Environment)
	}
	if scope.OriginType == "" || strings.TrimSpace(scope.OriginType) != scope.OriginType {
		return nil, fmt.Errorf("canonical position origin type is required")
	}
	if scope.OriginID == "" || strings.TrimSpace(scope.OriginID) != scope.OriginID {
		return nil, fmt.Errorf("canonical position origin ID is required")
	}
	if err := requireNonEmpty("ticker", ticker); err != nil {
		return nil, err
	}
	if !side.IsValid() {
		return nil, fmt.Errorf("invalid position side: %q", side)
	}
	if err := requirePositive("quantity", quantity); err != nil {
		return nil, err
	}
	if err := requirePositive("avg_entry", avgEntry); err != nil {
		return nil, err
	}
	return &Position{
		AccountID:   scope.AccountID,
		Environment: scope.Environment,
		OriginType:  scope.OriginType,
		OriginID:    scope.OriginID,
		Ticker:      ticker,
		Side:        side,
		Quantity:    quantity,
		AvgEntry:    avgEntry,
	}, nil
}

// Position represents an open or closed trading position.
type Position struct {
	ID            uuid.UUID          `json:"id"`
	AccountID     uuid.UUID          `json:"account_id,omitzero"`
	Environment   AccountEnvironment `json:"environment,omitempty"`
	OriginType    string             `json:"origin_type,omitempty"`
	OriginID      string             `json:"origin_id,omitempty"`
	StrategyID    *uuid.UUID         `json:"strategy_id,omitempty"`
	MarketType    MarketType         `json:"market_type,omitempty"`
	Ticker        string             `json:"ticker"`
	Side          PositionSide       `json:"side"`
	Quantity      float64            `json:"quantity"`
	AvgEntry      float64            `json:"avg_entry"`
	CurrentPrice  *float64           `json:"current_price,omitempty"`
	UnrealizedPnL *float64           `json:"unrealized_pnl,omitempty"`
	RealizedPnL   float64            `json:"realized_pnl"`
	StopLoss      *float64           `json:"stop_loss,omitempty"`
	TakeProfit    *float64           `json:"take_profit,omitempty"`
	OpenedAt      time.Time          `json:"opened_at"`
	ClosedAt      *time.Time         `json:"closed_at,omitempty"`

	// Options fields (nil/zero for equity positions).
	AssetClass         AssetClass  `json:"asset_class,omitempty"`
	UnderlyingTicker   string      `json:"underlying_ticker,omitempty"`
	OptionType         *OptionType `json:"option_type,omitempty"`
	Strike             *float64    `json:"strike,omitempty"`
	Expiry             *time.Time  `json:"expiry,omitempty"`
	ContractMultiplier float64     `json:"contract_multiplier,omitempty"`
	LegGroupID         *uuid.UUID  `json:"leg_group_id,omitempty"`
	Delta              *float64    `json:"delta,omitempty"`
	Gamma              *float64    `json:"gamma,omitempty"`
	Theta              *float64    `json:"theta,omitempty"`
	Vega               *float64    `json:"vega,omitempty"`
}

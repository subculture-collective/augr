package domain

import (
	"time"

	"github.com/google/uuid"
)

// OptionType identifies a contract as a call or put.
type OptionType string

const (
	OptionTypeCall OptionType = "call"
	OptionTypePut  OptionType = "put"
)

// PositionIntent describes the open/close direction of an options trade.
type PositionIntent string

const (
	PositionIntentBuyToOpen   PositionIntent = "buy_to_open"
	PositionIntentSellToOpen  PositionIntent = "sell_to_open"
	PositionIntentBuyToClose  PositionIntent = "buy_to_close"
	PositionIntentSellToClose PositionIntent = "sell_to_close"
)

// AssetClass discriminates between equity and options instruments.
type AssetClass string

const (
	AssetClassEquity AssetClass = "equity"
	AssetClassOption AssetClass = "option"
)

// OptionContract describes a single options contract.
type OptionContract struct {
	InstrumentID uuid.UUID  `json:"instrument_id,omitempty"`
	OCCSymbol    string     `json:"occ_symbol"`
	Underlying   string     `json:"underlying"`
	OptionType   OptionType `json:"option_type"`
	Strike       float64    `json:"strike"`
	Expiry       time.Time  `json:"expiry"`
	Multiplier   float64    `json:"multiplier"`
	Style        string     `json:"style,omitempty"` // "american" or "european"
}

// OptionGreeks holds the sensitivity measures for an options contract.
type OptionGreeks struct {
	Delta float64 `json:"delta"`
	Gamma float64 `json:"gamma"`
	Theta float64 `json:"theta"`
	Vega  float64 `json:"vega"`
	Rho   float64 `json:"rho,omitempty"`
	IV    float64 `json:"iv"`
}

// OptionSnapshot is a point-in-time view of a contract including price and Greeks.
type OptionSnapshot struct {
	Contract            OptionContract `json:"contract"`
	ContractPayloadID   uuid.UUID      `json:"contract_payload_id,omitempty"`
	ContractSHA256      string         `json:"contract_sha256,omitempty"`
	QuotePayloadID      uuid.UUID      `json:"quote_payload_id,omitempty"`
	QuoteSHA256         string         `json:"quote_sha256,omitempty"`
	SnapshotPayloadID   uuid.UUID      `json:"snapshot_payload_id,omitempty"`
	SnapshotSHA256      string         `json:"snapshot_sha256,omitempty"`
	Greeks              OptionGreeks   `json:"greeks"`
	Bid                 float64        `json:"bid"`
	BidSize             float64        `json:"bid_size"`
	Ask                 float64        `json:"ask"`
	AskSize             float64        `json:"ask_size"`
	Mid                 float64        `json:"mid"`
	Last                float64        `json:"last"`
	LastSize            float64        `json:"last_size"`
	Volume              float64        `json:"volume"`
	OpenInterest        float64        `json:"open_interest"`
	ObservedAt          time.Time      `json:"observed_at,omitempty"`
	QuoteObservedAt     time.Time      `json:"quote_observed_at,omitempty"`
	LastTradeObservedAt time.Time      `json:"last_trade_observed_at,omitempty"`
}

// SpreadLeg is one leg of a multi-leg options spread.
type SpreadLeg struct {
	Contract          OptionContract `json:"contract"`
	ContractPayloadID uuid.UUID      `json:"contract_payload_id,omitempty"`
	ContractSHA256    string         `json:"contract_sha256,omitempty"`
	QuotePayloadID    uuid.UUID      `json:"quote_payload_id,omitempty"`
	QuoteSHA256       string         `json:"quote_sha256,omitempty"`
	SnapshotPayloadID uuid.UUID      `json:"snapshot_payload_id,omitempty"`
	SnapshotSHA256    string         `json:"snapshot_sha256,omitempty"`
	Side              OrderSide      `json:"side"`
	PositionIntent    PositionIntent `json:"position_intent"`
	Ratio             int            `json:"ratio"`
	Quantity          float64        `json:"quantity"`
	ExecutablePrice   float64        `json:"executable_price"`
	Bid               float64        `json:"bid"`
	BidSize           float64        `json:"bid_size"`
	Ask               float64        `json:"ask"`
	AskSize           float64        `json:"ask_size"`
	QuoteObservedAt   time.Time      `json:"quote_observed_at,omitempty"`
	Greeks            OptionGreeks   `json:"greeks"`
	ClosePositionID   uuid.UUID      `json:"-"`
}

// OptionStrategyType identifies a named options strategy.
type OptionStrategyType string

const (
	StrategyLongCall       OptionStrategyType = "long_call"
	StrategyLongPut        OptionStrategyType = "long_put"
	StrategyCoveredCall    OptionStrategyType = "covered_call"
	StrategyCashSecuredPut OptionStrategyType = "cash_secured_put"
	StrategyBullCallSpread OptionStrategyType = "bull_call_spread"
	StrategyBearPutSpread  OptionStrategyType = "bear_put_spread"
	StrategyBullPutSpread  OptionStrategyType = "bull_put_spread"
	StrategyBearCallSpread OptionStrategyType = "bear_call_spread"
	StrategyIronCondor     OptionStrategyType = "iron_condor"
	StrategyIronButterfly  OptionStrategyType = "iron_butterfly"
	StrategyLongStraddle   OptionStrategyType = "long_straddle"
	StrategyLongStrangle   OptionStrategyType = "long_strangle"
	StrategyShortStraddle  OptionStrategyType = "short_straddle"
	StrategyShortStrangle  OptionStrategyType = "short_strangle"
	StrategyCalendarSpread OptionStrategyType = "calendar_spread"
	StrategyDiagonalSpread OptionStrategyType = "diagonal_spread"
)

// OptionSpread describes a multi-leg options position.
type OptionSpread struct {
	StrategyType    OptionStrategyType `json:"strategy_type"`
	Underlying      string             `json:"underlying"`
	Legs            []SpreadLeg        `json:"legs"`
	MaxRisk         float64            `json:"max_risk"`
	MaxReward       float64            `json:"max_reward"`
	LiquidityUSD    float64            `json:"liquidity_usd"`
	SpreadPct       float64            `json:"spread_pct"`
	QuoteObservedAt time.Time          `json:"quote_observed_at,omitempty"`
}

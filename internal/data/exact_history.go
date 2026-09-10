package data

import (
	"context"
	"encoding/json"
	"time"
)

// ExactHistoricalBar retains decimal source values without a float round-trip.
// Empty TradeCount or VWAP means not supplied; it must not become a source zero.
type ExactHistoricalBar struct {
	PageIndex, RowIndex            int
	Timestamp                      time.Time
	Open, High, Low, Close, Volume string
	TradeCount, VWAP               string
	Raw                            json.RawMessage
}

// HistoricalSourcePage retains credential-free request identity and raw evidence.
type HistoricalSourcePage struct {
	RequestPath string
	Query       string
	Body        []byte
}

// ExactHistoricalResult separates exact immutable inputs from legacy OHLCV.
type ExactHistoricalResult struct {
	Bars    []ExactHistoricalBar
	Pages   []HistoricalSourcePage
	Receipt HistoricalFetchReceipt
}

// ExactHistoricalProvider exposes source-preserving bars for immutable imports.
type ExactHistoricalProvider interface {
	GetExactOHLCVWithReceipt(context.Context, string, Timeframe, time.Time, time.Time, string, string) (ExactHistoricalResult, error)
}

// ExactOptionsHistoricalProvider retains option bars without a float round-trip.
// Implementations must preserve the actual options feed and source page identity.
type ExactOptionsHistoricalProvider interface {
	GetExactOptionsOHLCVWithReceipt(context.Context, string, Timeframe, time.Time, time.Time, string, string) (ExactHistoricalResult, error)
}

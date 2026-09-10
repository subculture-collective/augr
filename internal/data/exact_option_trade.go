package data

import (
	"context"
	"encoding/json"
	"time"
)

// ExactOptionTrade preserves source decimal values and the provider event identity.
type ExactOptionTrade struct {
	PageIndex, RowIndex               int
	Timestamp                         time.Time
	ProviderID, Price, Size, Exchange string
	Raw                               json.RawMessage
}

// ExactOptionsTradeResult retains original pages alongside their decoded trades.
type ExactOptionsTradeResult struct {
	Trades  []ExactOptionTrade
	Pages   []HistoricalSourcePage
	Receipt HistoricalFetchReceipt
}

// ExactOptionsTradeProvider supplies source-preserving historical option trades.
type ExactOptionsTradeProvider interface {
	GetExactOptionsTradesWithReceipt(context.Context, string, time.Time, time.Time, string) (ExactOptionsTradeResult, error)
}

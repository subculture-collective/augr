package data

import (
	"context"
	"encoding/json"
	"time"
)

// ExactOptionSnapshot preserves supplied nested values without guessed defaults.
// Contract reference terms must be resolved separately; snapshots do not report them.
type ExactOptionSnapshot struct {
	PageIndex                                         int
	Symbol                                            string
	QuoteTimestamp                                    time.Time
	BidPrice, BidSize, AskPrice, AskSize              string
	ImpliedVolatility, Delta, Gamma, Theta, Vega, Rho string
	LatestTrade                                       *ExactOptionTrade
	Raw                                               json.RawMessage
}

// ExactOptionsSnapshotResult retains current source pages, not historical coverage.
type ExactOptionsSnapshotResult struct {
	Snapshots []ExactOptionSnapshot
	Pages     []HistoricalSourcePage
	Receipt   HistoricalFetchReceipt
}

// ExactOptionsSnapshotProvider supplies exact current snapshots for an underlying.
type ExactOptionsSnapshotProvider interface {
	GetExactOptionsSnapshotsWithReceipt(context.Context, string, string) (ExactOptionsSnapshotResult, error)
}

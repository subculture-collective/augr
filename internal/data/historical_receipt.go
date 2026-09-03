package data

import (
	"context"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// HistoricalFetchReceipt is provider-issued provenance for a bounded historical
// request. Promotion-quality importers require this receipt instead of inferring
// entitlement or pagination from a non-empty result.
type HistoricalFetchReceipt struct {
	Provider           string
	Feed               string
	AdjustmentPolicy   string
	Pages              int
	Entitled           bool
	PaginationComplete bool
}

// VerifiedStockHistoricalProvider returns stock bars with request provenance.
type VerifiedStockHistoricalProvider interface {
	GetOHLCVWithReceipt(context.Context, string, Timeframe, time.Time, time.Time, string, string) ([]domain.OHLCV, HistoricalFetchReceipt, error)
}

// VerifiedOptionsHistoricalProvider returns option bars with request provenance.
type VerifiedOptionsHistoricalProvider interface {
	GetOptionsOHLCVWithReceipt(context.Context, string, Timeframe, time.Time, time.Time, string, string) ([]domain.OHLCV, HistoricalFetchReceipt, error)
}

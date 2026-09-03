package data

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// ManifestBindingReceipt makes every research read replayable to the exact
// immutable evidence graph that supplied it.
type ManifestBindingReceipt struct {
	ScopeID            uuid.UUID `json:"scope_id"`
	AccountID          uuid.UUID `json:"account_id"`
	ManifestID         uuid.UUID `json:"manifest_id"`
	ManifestSHA256     string    `json:"manifest_sha256"`
	QualityResultID    uuid.UUID `json:"quality_result_id"`
	QualitySHA256      string    `json:"quality_sha256"`
	PartitionSequences []int     `json:"partition_sequences"`
	ContentSHA256      []string  `json:"content_sha256"`
	Provider           string    `json:"provider"`
	Feed               string    `json:"feed"`
	Timeframe          string    `json:"timeframe"`
	AdjustmentPolicy   string    `json:"adjustment_policy"`
	EffectiveStart     time.Time `json:"effective_start"`
	EffectiveEnd       time.Time `json:"effective_end"`
	DecisionCutoff     time.Time `json:"decision_cutoff"`
}

type ManifestBoundHistoricalLoader interface {
	Load(context.Context, uuid.UUID, uuid.UUID, Timeframe, time.Time, time.Time) ([]domain.OHLCV, ManifestBindingReceipt, error)
}

type ManifestBoundOptionsReader interface {
	LoadOptions(context.Context, uuid.UUID, uuid.UUID, Timeframe, time.Time, time.Time) ([]domain.OHLCV, ManifestBindingReceipt, error)
}

type ManifestBoundSymbolLoader interface {
	LoadSymbol(context.Context, uuid.UUID, string, Timeframe, time.Time, time.Time) ([]domain.OHLCV, ManifestBindingReceipt, error)
}

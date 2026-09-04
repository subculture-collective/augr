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

// ManifestBoundOptionChainReader returns only immutable option observations
// that were both effective and available by an explicit decision time.
type ManifestBoundOptionChainReader interface {
	GetOptionsChainAt(context.Context, string, time.Time) ([]domain.OptionSnapshot, error)
}

// ManifestPayloadReceipt identifies one immutable manifest observation and its
// exact payload. Research code uses these fields to reconstruct experiment
// steps without guessing a partition or selecting "latest" evidence.
type ManifestPayloadReceipt struct {
	PayloadID              uuid.UUID `json:"payload_id"`
	PayloadKind            string    `json:"payload_kind"`
	PartitionSequence      int       `json:"partition_sequence"`
	PartitionContentSHA256 string    `json:"partition_content_sha256"`
	ObservationSequence    int       `json:"observation_sequence"`
	SourceKey              string    `json:"source_key"`
	ContentSHA256          string    `json:"content_sha256"`
	EffectiveAt            time.Time `json:"effective_at"`
	AvailableAt            time.Time `json:"available_at"`
}

// ManifestOptionChainReceipt binds a point-in-time chain to the complete
// canonical scope, manifest, quality result, and observation set that supplied
// it. The observation order is deterministic: contract, quote, then snapshot
// for each OCC symbol in chain order.
type ManifestOptionChainReceipt struct {
	ScopeID         uuid.UUID                `json:"scope_id"`
	AccountID       uuid.UUID                `json:"account_id"`
	ManifestID      uuid.UUID                `json:"manifest_id"`
	ManifestSHA256  string                   `json:"manifest_sha256"`
	QualityResultID uuid.UUID                `json:"quality_result_id"`
	QualitySHA256   string                   `json:"quality_sha256"`
	DecisionAt      time.Time                `json:"decision_at"`
	DecisionCutoff  time.Time                `json:"decision_cutoff"`
	Observations    []ManifestPayloadReceipt `json:"observations"`
}

// ManifestBoundOptionChainEvidenceReader is required for promotion-quality
// historical options research. The simpler reader remains available to
// point-in-time screeners that do not persist experiment evidence.
type ManifestBoundOptionChainEvidenceReader interface {
	ManifestBoundOptionChainReader
	GetOptionsChainAtWithReceipt(context.Context, string, time.Time) ([]domain.OptionSnapshot, ManifestOptionChainReceipt, error)
}

// ManifestBoundOptionFrameEvidenceReader also reconstructs the underlying bar
// consumed by an options decision. A chain receipt alone is insufficient when
// signals depend on underlying-price indicators.
type ManifestBoundOptionFrameEvidenceReader interface {
	ManifestBoundOptionChainEvidenceReader
	GetUnderlyingBarAtWithReceipt(context.Context, string, Timeframe, time.Time) (domain.OHLCV, ManifestPayloadReceipt, error)
}

type ManifestBoundSymbolLoader interface {
	LoadSymbol(context.Context, uuid.UUID, string, Timeframe, time.Time, time.Time) ([]domain.OHLCV, ManifestBindingReceipt, error)
}

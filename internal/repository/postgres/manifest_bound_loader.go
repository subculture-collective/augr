package postgres

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type ManifestBoundHistoricalLoader struct {
	pool      *pgxpool.Pool
	reports   *ReportArtifactRepo
	accountID uuid.UUID
}

func NewManifestBoundHistoricalLoader(pool *pgxpool.Pool, reports *ReportArtifactRepo, accountID uuid.UUID) *ManifestBoundHistoricalLoader {
	return &ManifestBoundHistoricalLoader{pool: pool, reports: reports, accountID: accountID}
}

// LoadResearchInterval reconstructs the same account-bound evidence graph used
// by Load. It never derives scope dates from a provider or the wall clock.
func (loader *ManifestBoundHistoricalLoader) LoadResearchInterval(ctx context.Context, scopeID uuid.UUID) (data.ResearchInterval, error) {
	if loader == nil || loader.pool == nil || loader.reports == nil || loader.accountID == uuid.Nil || scopeID == uuid.Nil {
		return data.ResearchInterval{}, fmt.Errorf("manifest-bound research interval requires scope and account")
	}
	report, err := loader.reports.DiscoveryDeploymentReadinessForScope(ctx, scopeID, loader.accountID)
	if err != nil {
		return data.ResearchInterval{}, fmt.Errorf("manifest-bound research interval: %w", err)
	}
	if !report.Stock.Ready {
		return data.ResearchInterval{}, fmt.Errorf("manifest-bound stock evidence: %s", report.Stock.Reason)
	}
	return data.ResearchInterval{Start: report.EvaluationStart, End: report.EvaluationEnd}, nil
}

func (loader *ManifestBoundHistoricalLoader) Load(ctx context.Context, scopeID, instrumentID uuid.UUID, timeframe data.Timeframe, start, end time.Time) ([]domain.OHLCV, data.ManifestBindingReceipt, error) {
	return loader.load(ctx, scopeID, instrumentID, timeframe, start, end, dataset.MarketPayloadStockBar)
}

func (loader *ManifestBoundHistoricalLoader) LoadOptions(ctx context.Context, scopeID, instrumentID uuid.UUID, timeframe data.Timeframe, start, end time.Time) ([]domain.OHLCV, data.ManifestBindingReceipt, error) {
	return loader.load(ctx, scopeID, instrumentID, timeframe, start, end, dataset.MarketPayloadOptionBar)
}

func (loader *ManifestBoundHistoricalLoader) LoadSymbol(ctx context.Context, scopeID uuid.UUID, symbol string, timeframe data.Timeframe, start, end time.Time) ([]domain.OHLCV, data.ManifestBindingReceipt, error) {
	return loader.loadSymbolKind(ctx, scopeID, symbol, timeframe, start, end, dataset.MarketPayloadStockBar)
}

func (loader *ManifestBoundHistoricalLoader) LoadOptionSymbol(ctx context.Context, scopeID uuid.UUID, symbol string, timeframe data.Timeframe, start, end time.Time) ([]domain.OHLCV, data.ManifestBindingReceipt, error) {
	return loader.loadSymbolKind(ctx, scopeID, symbol, timeframe, start, end, dataset.MarketPayloadOptionBar)
}

func (loader *ManifestBoundHistoricalLoader) loadSymbolKind(ctx context.Context, scopeID uuid.UUID, symbol string, timeframe data.Timeframe, start, end time.Time, kind dataset.MarketPayloadKind) ([]domain.OHLCV, data.ManifestBindingReceipt, error) {
	if loader == nil || loader.pool == nil || scopeID == uuid.Nil || symbol == "" {
		return nil, data.ManifestBindingReceipt{}, fmt.Errorf("manifest-bound symbol loader requires scope and symbol")
	}
	rows, err := loader.pool.Query(ctx, `SELECT DISTINCT payload.instrument_id
		FROM paper_evaluation_scopes scope
		JOIN dataset_manifests manifest ON manifest.sha256=scope.manifest_sha256
		JOIN dataset_manifest_payload_bindings binding ON binding.manifest_id=manifest.id
		JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
		WHERE scope.id=$1 AND scope.account_id=$2 AND payload.payload_kind=$3 AND payload.symbol=$4`,
		scopeID, loader.accountID, kind, symbol)
	if err != nil {
		return nil, data.ManifestBindingReceipt{}, fmt.Errorf("resolve manifest-bound symbol: %w", err)
	}
	defer rows.Close()
	var instrumentIDs []uuid.UUID
	for rows.Next() {
		var instrumentID uuid.UUID
		if err := rows.Scan(&instrumentID); err != nil {
			return nil, data.ManifestBindingReceipt{}, fmt.Errorf("scan manifest-bound symbol: %w", err)
		}
		instrumentIDs = append(instrumentIDs, instrumentID)
	}
	if err := rows.Err(); err != nil {
		return nil, data.ManifestBindingReceipt{}, err
	}
	if len(instrumentIDs) != 1 {
		return nil, data.ManifestBindingReceipt{}, fmt.Errorf("manifest-bound symbol %s resolves to %d instruments", symbol, len(instrumentIDs))
	}
	return loader.load(ctx, scopeID, instrumentIDs[0], timeframe, start, end, kind)
}

func (loader *ManifestBoundHistoricalLoader) load(ctx context.Context, scopeID, instrumentID uuid.UUID, timeframe data.Timeframe, start, end time.Time, kind dataset.MarketPayloadKind) ([]domain.OHLCV, data.ManifestBindingReceipt, error) {
	var receipt data.ManifestBindingReceipt
	if loader == nil || loader.pool == nil || loader.reports == nil || loader.accountID == uuid.Nil || scopeID == uuid.Nil || instrumentID == uuid.Nil ||
		timeframe == "" || start.Location() != time.UTC || end.Location() != time.UTC || start.After(end) ||
		!start.Equal(start.Truncate(time.Microsecond)) || !end.Equal(end.Truncate(time.Microsecond)) {
		return nil, receipt, fmt.Errorf("manifest-bound loader requires a canonical account, scope, instrument, timeframe, and UTC microsecond interval")
	}
	report, err := loader.reports.DiscoveryDeploymentReadinessForScope(ctx, scopeID, loader.accountID)
	if err != nil {
		return nil, receipt, fmt.Errorf("manifest-bound loader scope: %w", err)
	}
	if kind == dataset.MarketPayloadStockBar && !report.Stock.Ready {
		return nil, receipt, fmt.Errorf("manifest-bound stock evidence: %s", report.Stock.Reason)
	}
	if kind == dataset.MarketPayloadOptionBar && !report.Options.Ready {
		return nil, receipt, fmt.Errorf("manifest-bound options evidence: %s", report.Options.Reason)
	}
	if start.Before(report.EvaluationStart) || end.After(report.EvaluationEnd) {
		return nil, receipt, fmt.Errorf("manifest-bound requested interval %s..%s escapes evaluation scope %s..%s",
			start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano),
			report.EvaluationStart.Format(time.RFC3339Nano), report.EvaluationEnd.Format(time.RFC3339Nano))
	}
	rows, err := loader.pool.Query(ctx, `SELECT payload.id,payload.content_sha256,payload.canonical_bytes,binding.partition_sequence,
		payload.provider,payload.feed,payload.adjustment_policy
		FROM paper_evaluation_scopes scope
		JOIN dataset_manifests manifest ON manifest.sha256=scope.manifest_sha256
		JOIN dataset_manifest_payload_bindings binding ON binding.manifest_id=manifest.id
		JOIN dataset_market_payloads payload ON payload.id=binding.payload_id AND payload.content_sha256=binding.content_sha256
		WHERE scope.id=$1 AND scope.account_id=$2 AND payload.instrument_id=$3 AND payload.payload_kind=$4
		  AND payload.timeframe=$5 AND payload.effective_at BETWEEN $6 AND $7
		  AND payload.available_at<=manifest.decision_cutoff
		ORDER BY payload.effective_at,binding.partition_sequence,binding.observation_sequence`,
		scopeID, loader.accountID, instrumentID, kind, timeframe.String(), start, end)
	if err != nil {
		return nil, receipt, fmt.Errorf("query manifest-bound payloads: %w", err)
	}
	defer rows.Close()
	receipt = data.ManifestBindingReceipt{
		ScopeID: scopeID, AccountID: loader.accountID, ManifestID: report.ManifestID, ManifestSHA256: report.ManifestSHA256,
		QualityResultID: report.QualityResultID, QualitySHA256: report.QualitySHA256, Timeframe: timeframe.String(),
		DecisionCutoff: report.DecisionCutoff,
	}
	partitionSet := make(map[int]struct{})
	bars := make([]domain.OHLCV, 0)
	for rows.Next() {
		var payloadID uuid.UUID
		var digest string
		var raw []byte
		var partitionSequence int
		var provider, feed, adjustment string
		if err := rows.Scan(&payloadID, &digest, &raw, &partitionSequence, &provider, &feed, &adjustment); err != nil {
			return nil, data.ManifestBindingReceipt{}, fmt.Errorf("scan manifest-bound payload: %w", err)
		}
		payload, err := dataset.MarketPayloadFromCanonical(payloadID, digest, raw)
		if err != nil {
			return nil, data.ManifestBindingReceipt{}, fmt.Errorf("reconstruct manifest-bound payload: %w", err)
		}
		metadata := payload.Metadata()
		if metadata.Revision != "original" || metadata.CorrectionOfSHA256 != "" {
			return nil, data.ManifestBindingReceipt{}, fmt.Errorf("manifest-bound loader revision policy rejects %s", digest)
		}
		if len(bars) > 0 && !metadata.EffectiveAt.After(bars[len(bars)-1].Timestamp) {
			return nil, data.ManifestBindingReceipt{}, fmt.Errorf("manifest-bound loader found duplicate or unordered effective time")
		}
		bar := payload.Bar()
		converted, err := marketPayloadBar(metadata.EffectiveAt, bar)
		if err != nil {
			return nil, data.ManifestBindingReceipt{}, err
		}
		if receipt.Provider == "" {
			receipt.Provider, receipt.Feed, receipt.AdjustmentPolicy = provider, feed, adjustment
		} else if receipt.Provider != provider || receipt.Feed != feed || receipt.AdjustmentPolicy != adjustment {
			return nil, data.ManifestBindingReceipt{}, fmt.Errorf("manifest-bound loader found mixed provider, feed, or adjustment policy")
		}
		partitionSet[partitionSequence] = struct{}{}
		receipt.ContentSHA256 = append(receipt.ContentSHA256, digest)
		bars = append(bars, converted)
	}
	if err := rows.Err(); err != nil {
		return nil, data.ManifestBindingReceipt{}, fmt.Errorf("read manifest-bound payloads: %w", err)
	}
	if len(bars) == 0 {
		return nil, data.ManifestBindingReceipt{}, fmt.Errorf("manifest-bound payloads contain no observations in the requested interval")
	}
	for sequence := range partitionSet {
		receipt.PartitionSequences = append(receipt.PartitionSequences, sequence)
	}
	sort.Ints(receipt.PartitionSequences)
	receipt.EffectiveStart = bars[0].Timestamp
	receipt.EffectiveEnd = bars[len(bars)-1].Timestamp
	return bars, receipt, nil
}

func marketPayloadBar(at time.Time, value *dataset.BarPayload) (domain.OHLCV, error) {
	if value == nil {
		return domain.OHLCV{}, fmt.Errorf("manifest-bound payload has no bar body")
	}
	values := []*float64{new(float64), new(float64), new(float64), new(float64), new(float64)}
	for index, raw := range []string{value.Open, value.High, value.Low, value.Close, value.Volume} {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return domain.OHLCV{}, fmt.Errorf("manifest-bound bar decimal does not convert: %w", err)
		}
		*values[index] = parsed
	}
	return domain.OHLCV{Timestamp: at, Open: *values[0], High: *values[1], Low: *values[2], Close: *values[3], Volume: *values[4]}, nil
}

var (
	_ data.ManifestBoundHistoricalLoader = (*ManifestBoundHistoricalLoader)(nil)
	_ data.ManifestBoundOptionsReader    = (*ManifestBoundHistoricalLoader)(nil)
	_ data.ManifestBoundSymbolLoader     = (*ManifestBoundHistoricalLoader)(nil)
)

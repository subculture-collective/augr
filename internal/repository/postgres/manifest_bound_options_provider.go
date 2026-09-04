package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// ManifestBoundOptionsProvider implements the discovery options-data contract
// exclusively from immutable payloads reachable through one configured scope.
type ManifestBoundOptionsProvider struct {
	pool      *pgxpool.Pool
	reports   *ReportArtifactRepo
	loader    *ManifestBoundHistoricalLoader
	scopeID   uuid.UUID
	accountID uuid.UUID
}

type manifestOptionPayloadEvidence struct {
	id                  uuid.UUID
	digest              string
	raw                 []byte
	partitionSequence   int
	partitionSHA256     string
	observationSequence int
	sourceKey           string
	effectiveAt         time.Time
	availableAt         time.Time
}

func (value *manifestOptionPayloadEvidence) scanTargets() []any {
	return []any{&value.id, &value.digest, &value.raw, &value.partitionSequence, &value.partitionSHA256,
		&value.observationSequence, &value.sourceKey, &value.effectiveAt, &value.availableAt}
}

func (value manifestOptionPayloadEvidence) receipt(kind dataset.MarketPayloadKind) data.ManifestPayloadReceipt {
	return data.ManifestPayloadReceipt{
		PayloadID: value.id, PayloadKind: string(kind), PartitionSequence: value.partitionSequence,
		PartitionContentSHA256: value.partitionSHA256, ObservationSequence: value.observationSequence,
		SourceKey: value.sourceKey, ContentSHA256: value.digest, EffectiveAt: value.effectiveAt, AvailableAt: value.availableAt,
	}
}

func NewManifestBoundOptionsProvider(pool *pgxpool.Pool, reports *ReportArtifactRepo, scopeID, accountID uuid.UUID) *ManifestBoundOptionsProvider {
	return &ManifestBoundOptionsProvider{
		pool: pool, reports: reports, loader: NewManifestBoundHistoricalLoader(pool, reports, accountID), scopeID: scopeID, accountID: accountID,
	}
}

func (provider *ManifestBoundOptionsProvider) GetOptionsOHLCV(ctx context.Context, symbol string, timeframe data.Timeframe, from, to time.Time) ([]domain.OHLCV, error) {
	if provider == nil || provider.loader == nil {
		return nil, fmt.Errorf("manifest-bound options provider is not configured")
	}
	bars, _, err := provider.loader.LoadOptionSymbol(ctx, provider.scopeID, symbol, timeframe, from.UTC().Truncate(time.Microsecond), to.UTC().Truncate(time.Microsecond))
	return bars, err
}

func (provider *ManifestBoundOptionsProvider) GetOptionsChain(ctx context.Context, underlying string, expiry time.Time, optionType domain.OptionType) ([]domain.OptionSnapshot, error) {
	chain, _, err := provider.loadOptionsChain(ctx, underlying, expiry, optionType, time.Time{})
	return chain, err
}

// GetOptionsChainAt reconstructs the latest complete point-in-time chain that
// was knowable at decisionAt. It never selects a payload effective or available
// after that timestamp.
func (provider *ManifestBoundOptionsProvider) GetOptionsChainAt(ctx context.Context, underlying string, decisionAt time.Time) ([]domain.OptionSnapshot, error) {
	chain, _, err := provider.loadOptionsChain(ctx, underlying, time.Time{}, "", decisionAt)
	return chain, err
}

func (provider *ManifestBoundOptionsProvider) GetOptionsChainAtWithReceipt(ctx context.Context, underlying string, decisionAt time.Time) ([]domain.OptionSnapshot, data.ManifestOptionChainReceipt, error) {
	return provider.loadOptionsChain(ctx, underlying, time.Time{}, "", decisionAt)
}

func (provider *ManifestBoundOptionsProvider) loadOptionsChain(ctx context.Context, underlying string, expiry time.Time, optionType domain.OptionType, decisionAt time.Time) ([]domain.OptionSnapshot, data.ManifestOptionChainReceipt, error) {
	var receipt data.ManifestOptionChainReceipt
	if provider == nil || provider.pool == nil || provider.reports == nil || provider.scopeID == uuid.Nil || provider.accountID == uuid.Nil || underlying == "" {
		return nil, receipt, fmt.Errorf("manifest-bound options provider requires scope, account, and underlying")
	}
	report, err := provider.reports.DiscoveryDeploymentReadinessForScope(ctx, provider.scopeID, provider.accountID)
	if err != nil {
		return nil, receipt, err
	}
	if !report.Options.Ready {
		return nil, receipt, fmt.Errorf("manifest-bound options evidence: %s", report.Options.Reason)
	}
	cutoff := report.DecisionCutoff
	if !decisionAt.IsZero() {
		if decisionAt.Location() != time.UTC || !decisionAt.Equal(decisionAt.Truncate(time.Microsecond)) || decisionAt.Before(report.EvaluationStart) || decisionAt.After(report.EvaluationEnd) || decisionAt.After(report.DecisionCutoff) {
			return nil, receipt, fmt.Errorf("manifest-bound option chain decision time escapes the canonical evaluation interval or availability cutoff")
		}
		cutoff = decisionAt
	}
	receipt = data.ManifestOptionChainReceipt{
		ScopeID: provider.scopeID, AccountID: provider.accountID, ManifestID: report.ManifestID, ManifestSHA256: report.ManifestSHA256,
		QualityResultID: report.QualityResultID, QualitySHA256: report.QualitySHA256, DecisionAt: cutoff, DecisionCutoff: report.DecisionCutoff,
	}
	rows, err := provider.pool.Query(ctx, `SELECT snapshot.id,snapshot.content_sha256,snapshot.canonical_bytes,
		snapshot.binding_partition_sequence,snapshot.binding_partition_sha256,snapshot.binding_observation_sequence,snapshot.binding_source_key,snapshot.effective_at,snapshot.available_at,
		contract.id,contract.content_sha256,contract.canonical_bytes,
		contract.binding_partition_sequence,contract.binding_partition_sha256,contract.binding_observation_sequence,contract.binding_source_key,contract.effective_at,contract.available_at,
		quote.id,quote.content_sha256,quote.canonical_bytes,
		quote.binding_partition_sequence,quote.binding_partition_sha256,quote.binding_observation_sequence,quote.binding_source_key,quote.effective_at,quote.available_at
		FROM (
		  SELECT DISTINCT ON (payload.instrument_id) payload.*,
		    binding.partition_sequence AS binding_partition_sequence,observation.partition_content_sha256 AS binding_partition_sha256,
		    binding.observation_sequence AS binding_observation_sequence,observation.source_key AS binding_source_key
		  FROM dataset_manifest_payload_bindings binding
		  JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
		  JOIN dataset_manifest_observations observation ON observation.manifest_id=binding.manifest_id
		    AND observation.partition_sequence=binding.partition_sequence AND observation.sequence=binding.observation_sequence
		  WHERE binding.manifest_id=$1 AND payload.payload_kind='option_snapshot' AND payload.underlying_symbol=$2
		    AND payload.effective_at<=$3 AND payload.available_at<=$3
		  ORDER BY payload.instrument_id,payload.effective_at DESC,payload.available_at DESC,payload.id DESC
		) snapshot
		JOIN LATERAL (
		  SELECT payload.*,binding.partition_sequence AS binding_partition_sequence,observation.partition_content_sha256 AS binding_partition_sha256,
		    binding.observation_sequence AS binding_observation_sequence,observation.source_key AS binding_source_key
		  FROM dataset_manifest_payload_bindings binding
		  JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
		  JOIN dataset_manifest_observations observation ON observation.manifest_id=binding.manifest_id
		    AND observation.partition_sequence=binding.partition_sequence AND observation.sequence=binding.observation_sequence
		  WHERE binding.manifest_id=$1 AND payload.payload_kind='option_contract'
		    AND payload.instrument_id=snapshot.instrument_id AND payload.effective_at<=$3 AND payload.available_at<=$3
		    AND payload.available_at<=snapshot.available_at
		  ORDER BY payload.effective_at DESC,payload.available_at DESC,payload.id DESC LIMIT 1
		) contract ON true
		JOIN LATERAL (
		  SELECT payload.*,binding.partition_sequence AS binding_partition_sequence,observation.partition_content_sha256 AS binding_partition_sha256,
		    binding.observation_sequence AS binding_observation_sequence,observation.source_key AS binding_source_key
		  FROM dataset_manifest_payload_bindings binding
		  JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
		  JOIN dataset_manifest_observations observation ON observation.manifest_id=binding.manifest_id
		    AND observation.partition_sequence=binding.partition_sequence AND observation.sequence=binding.observation_sequence
		  WHERE binding.manifest_id=$1 AND payload.payload_kind='option_quote'
		    AND payload.instrument_id=snapshot.instrument_id AND payload.effective_at<=snapshot.effective_at
		    AND payload.effective_at<=$3 AND payload.available_at<=$3 AND payload.available_at<=snapshot.available_at
		  ORDER BY payload.effective_at DESC,payload.available_at DESC,payload.id DESC LIMIT 1
		) quote ON true
		ORDER BY snapshot.symbol`, report.ManifestID, underlying, cutoff)
	if err != nil {
		return nil, receipt, fmt.Errorf("query manifest-bound option chain: %w", err)
	}
	defer rows.Close()
	result := make([]domain.OptionSnapshot, 0)
	for rows.Next() {
		var snapshotEvidence, contractEvidence, quoteEvidence manifestOptionPayloadEvidence
		targets := append(snapshotEvidence.scanTargets(), contractEvidence.scanTargets()...)
		targets = append(targets, quoteEvidence.scanTargets()...)
		if err := rows.Scan(targets...); err != nil {
			return nil, receipt, fmt.Errorf("scan manifest-bound option chain: %w", err)
		}
		snapshotPayload, err := dataset.MarketPayloadFromCanonical(snapshotEvidence.id, snapshotEvidence.digest, snapshotEvidence.raw)
		if err != nil {
			return nil, receipt, fmt.Errorf("reconstruct option snapshot: %w", err)
		}
		contractPayload, err := dataset.MarketPayloadFromCanonical(contractEvidence.id, contractEvidence.digest, contractEvidence.raw)
		if err != nil {
			return nil, receipt, fmt.Errorf("reconstruct option contract: %w", err)
		}
		quotePayload, err := dataset.MarketPayloadFromCanonical(quoteEvidence.id, quoteEvidence.digest, quoteEvidence.raw)
		if err != nil {
			return nil, receipt, fmt.Errorf("reconstruct option quote: %w", err)
		}
		for _, evidence := range []struct {
			value   manifestOptionPayloadEvidence
			payload *dataset.MarketPayload
		}{{snapshotEvidence, snapshotPayload}, {contractEvidence, contractPayload}, {quoteEvidence, quotePayload}} {
			metadata := evidence.payload.Metadata()
			if metadata.EffectiveAt != evidence.value.effectiveAt || metadata.AvailableAt != evidence.value.availableAt ||
				metadata.AvailableAt.After(cutoff) || evidence.value.partitionSequence < 0 || evidence.value.observationSequence < 0 ||
				evidence.value.partitionSHA256 == "" || evidence.value.sourceKey == "" {
				return nil, receipt, fmt.Errorf("manifest-bound option observation receipt does not reconstruct")
			}
		}
		contractBody, snapshotBody, quoteBody := contractPayload.Contract(), snapshotPayload.Snapshot(), quotePayload.Quote()
		if contractBody == nil || snapshotBody == nil || contractPayload.InstrumentID() != snapshotPayload.InstrumentID() {
			return nil, receipt, fmt.Errorf("manifest-bound option snapshot and contract do not reconstruct")
		}
		if quoteBody == nil || quotePayload.InstrumentID() != snapshotPayload.InstrumentID() || *quoteBody != snapshotBody.Quote {
			return nil, receipt, fmt.Errorf("manifest-bound option snapshot and exact quote do not reconstruct")
		}
		parsedExpiry, err := time.Parse("2006-01-02", contractBody.Expiry)
		if err != nil {
			return nil, receipt, err
		}
		kind := domain.OptionType(contractBody.OptionType)
		if (!expiry.IsZero() && !parsedExpiry.Equal(expiry.UTC())) || (optionType != "" && kind != optionType) {
			continue
		}
		strike, err := parseDatasetFloat(contractBody.Strike)
		if err != nil {
			return nil, receipt, err
		}
		multiplier, err := parseDatasetFloat(contractBody.Multiplier)
		if err != nil {
			return nil, receipt, err
		}
		bid, err := parseDatasetFloat(snapshotBody.Quote.BidPrice)
		if err != nil {
			return nil, receipt, err
		}
		ask, err := parseDatasetFloat(snapshotBody.Quote.AskPrice)
		if err != nil {
			return nil, receipt, err
		}
		bidSize, err := parseDatasetFloat(snapshotBody.Quote.BidSize)
		if err != nil {
			return nil, receipt, err
		}
		askSize, err := parseDatasetFloat(snapshotBody.Quote.AskSize)
		if err != nil {
			return nil, receipt, err
		}
		last, err := parseDatasetFloat(snapshotBody.LastTradePrice)
		if err != nil {
			return nil, receipt, err
		}
		lastSize, err := parseDatasetFloat(snapshotBody.LastTradeSize)
		if err != nil {
			return nil, receipt, err
		}
		greeks, err := parseSnapshotGreeks(snapshotBody)
		if err != nil {
			return nil, receipt, err
		}
		result = append(result, domain.OptionSnapshot{
			Contract: domain.OptionContract{
				InstrumentID: snapshotPayload.InstrumentID(), OCCSymbol: snapshotPayload.Symbol(), Underlying: underlying, OptionType: kind, Strike: strike,
				Expiry: parsedExpiry, Multiplier: multiplier, Style: contractBody.Style,
			},
			ContractPayloadID: contractEvidence.id, ContractSHA256: contractEvidence.digest, QuotePayloadID: quoteEvidence.id, QuoteSHA256: quoteEvidence.digest,
			SnapshotPayloadID: snapshotEvidence.id, SnapshotSHA256: snapshotEvidence.digest,
			Greeks: greeks, Bid: bid, BidSize: bidSize, Ask: ask, AskSize: askSize, Mid: (bid + ask) / 2, Last: last, LastSize: lastSize,
			ObservedAt: snapshotPayload.EffectiveAt(), QuoteObservedAt: quotePayload.EffectiveAt(), LastTradeObservedAt: snapshotPayload.EffectiveAt(),
		})
		receipt.Observations = append(receipt.Observations,
			contractEvidence.receipt(dataset.MarketPayloadOptionContract),
			quoteEvidence.receipt(dataset.MarketPayloadOptionQuote),
			snapshotEvidence.receipt(dataset.MarketPayloadOptionSnapshot),
		)
	}
	if err := rows.Err(); err != nil {
		return nil, receipt, err
	}
	if len(result) == 0 {
		return nil, receipt, fmt.Errorf("manifest-bound option chain has no matching complete contracts")
	}
	return result, receipt, nil
}

func parseSnapshotGreeks(value *dataset.OptionSnapshotPayload) (domain.OptionGreeks, error) {
	values := make([]float64, 6)
	for index, raw := range []string{value.Delta, value.Gamma, value.Theta, value.Vega, value.Rho, value.ImpliedVolatility} {
		parsed, err := parseDatasetFloat(raw)
		if err != nil {
			return domain.OptionGreeks{}, err
		}
		values[index] = parsed
	}
	return domain.OptionGreeks{Delta: values[0], Gamma: values[1], Theta: values[2], Vega: values[3], Rho: values[4], IV: values[5]}, nil
}

func parseDatasetFloat(value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("convert immutable market decimal %q: %w", value, err)
	}
	return parsed, nil
}

var _ data.OptionsDataProvider = (*ManifestBoundOptionsProvider)(nil)
var _ data.ManifestBoundOptionChainReader = (*ManifestBoundOptionsProvider)(nil)
var _ data.ManifestBoundOptionChainEvidenceReader = (*ManifestBoundOptionsProvider)(nil)

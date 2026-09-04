package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

const generatedDailyStockEvidenceSchemaV2 = "generated-daily-stock-proposal-evidence-v2"

type generatedDailyStockInstrumentSummary struct {
	InstrumentID     string `json:"instrument_id"`
	Symbol           string `json:"symbol"`
	Provider         string `json:"provider"`
	Feed             string `json:"feed"`
	Timeframe        string `json:"timeframe"`
	Adjustment       string `json:"adjustment_policy"`
	ObservationCount int    `json:"observation_count"`
	EffectiveStart   string `json:"effective_start"`
	EffectiveEnd     string `json:"effective_end"`
	FirstSHA256      string `json:"first_sha256"`
	LatestSHA256     string `json:"latest_sha256"`
	ContentSetSHA256 string `json:"content_set_sha256"`
	LatestOpen       string `json:"latest_open"`
	LatestHigh       string `json:"latest_high"`
	LatestLow        string `json:"latest_low"`
	LatestClose      string `json:"latest_close"`
	LatestVolume     string `json:"latest_volume"`
	LatestTradeCount string `json:"latest_trade_count"`
	LatestVWAP       string `json:"latest_vwap"`
	VenueContractID  string `json:"venue_contract_id"`
}

type generatedDailyStockEvidenceSummary struct {
	Schema           string                                 `json:"schema"`
	TrustedSpecKey   string                                 `json:"trusted_spec_key"`
	AccountID        string                                 `json:"account_id"`
	ScopeID          string                                 `json:"scope_id"`
	ManifestID       string                                 `json:"manifest_id"`
	ManifestSHA256   string                                 `json:"manifest_sha256"`
	QualityResultID  string                                 `json:"quality_result_id"`
	QualitySHA256    string                                 `json:"quality_sha256"`
	DecisionCutoff   string                                 `json:"decision_cutoff"`
	ManifestCutoff   string                                 `json:"manifest_decision_cutoff"`
	EvaluationStart  string                                 `json:"evaluation_start"`
	EvaluationEnd    string                                 `json:"evaluation_end"`
	BenchmarkID      string                                 `json:"benchmark_instrument_id"`
	AllowedBarFields []string                               `json:"allowed_bar_fields"`
	Instruments      []generatedDailyStockInstrumentSummary `json:"instruments"`
}

type generatedDailyStockGroup struct {
	metadata []dataset.MarketPayloadMetadata
	payloads []*dataset.MarketPayload
}

func (r *GenerativeStrategyRepo) ListEligibleGeneratedProposalEvidence(
	ctx context.Context,
	accountID, scopeID, familyID uuid.UUID,
	limit int,
) ([]generativestrategy.ProposalEvidence, error) {
	if r == nil || r.pool == nil || accountID == uuid.Nil || scopeID == uuid.Nil || familyID == uuid.Nil || limit <= 0 || limit > generativestrategy.MaximumResearchBatchSize {
		return nil, fmt.Errorf("postgres: exact generated proposal account, scope, family, and bounded limit are required")
	}
	report, err := NewReportArtifactRepo(r.pool).DiscoveryDeploymentReadinessForScope(ctx, scopeID, accountID)
	if err != nil {
		return nil, fmt.Errorf("postgres: generated proposal scope: %w", err)
	}
	if !report.Stock.Ready {
		return nil, fmt.Errorf("postgres: generated proposal stock evidence: %s", report.Stock.Reason)
	}
	folds, err := generativestrategy.PlanReviewedResearchFolds(report.EvaluationStart, report.EvaluationEnd)
	if err != nil {
		return nil, fmt.Errorf("postgres: generated proposal fold plan: %w", err)
	}
	proposalCutoff := folds[0].TrainEnd
	payloads, err := NewDatasetRepo(r.pool).ListBoundMarketPayloads(ctx, report.ManifestID, dataset.MarketPayloadStockBar)
	if err != nil {
		return nil, fmt.Errorf("postgres: load generated proposal payloads: %w", err)
	}
	groups := make(map[uuid.UUID]*generatedDailyStockGroup)
	for _, payload := range payloads {
		metadata := payload.Metadata()
		if metadata.Timeframe != data.Timeframe1d.String() || metadata.EffectiveAt.Before(report.EvaluationStart) || metadata.EffectiveAt.After(report.EvaluationEnd) || payload.ReplayAvailableAt().After(proposalCutoff) {
			continue
		}
		if metadata.Revision != "original" || metadata.CorrectionOfSHA256 != "" || metadata.AvailableAt.After(report.DecisionCutoff) {
			return nil, fmt.Errorf("postgres: generated proposal payload revision or cutoff is ineligible")
		}
		group := groups[metadata.InstrumentID]
		if group == nil {
			group = &generatedDailyStockGroup{}
			groups[metadata.InstrumentID] = group
		}
		group.metadata = append(group.metadata, metadata)
		group.payloads = append(group.payloads, payload)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("postgres: generated proposal scope has no immutable 1d stock bars")
	}
	ids := make([]uuid.UUID, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	summaries := make([]generatedDailyStockInstrumentSummary, 0, len(ids))
	benchmarkID := uuid.Nil
	seenSymbols := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		group := groups[id]
		sort.Slice(group.payloads, func(i, j int) bool { return group.payloads[i].EffectiveAt().Before(group.payloads[j].EffectiveAt()) })
		group.metadata = group.metadata[:0]
		for _, payload := range group.payloads {
			group.metadata = append(group.metadata, payload.Metadata())
		}
		first, latest := group.metadata[0], group.metadata[len(group.metadata)-1]
		if first.EffectiveAt.After(report.EvaluationStart) || group.payloads[len(group.payloads)-1].ReplayAvailableAt().Before(proposalCutoff.Add(-48*time.Hour)) {
			return nil, fmt.Errorf("postgres: generated proposal instrument %s does not cover the calibration interval", id)
		}
		for index, metadata := range group.metadata {
			if metadata.Provider != first.Provider || metadata.Feed != first.Feed || metadata.Timeframe != first.Timeframe || metadata.AdjustmentPolicy != first.AdjustmentPolicy ||
				index > 0 && !metadata.EffectiveAt.After(group.metadata[index-1].EffectiveAt) {
				return nil, fmt.Errorf("postgres: generated proposal instrument %s has mixed or unordered evidence", id)
			}
		}
		if _, duplicate := seenSymbols[first.Symbol]; duplicate {
			return nil, fmt.Errorf("postgres: generated proposal symbol %q resolves to multiple instruments", first.Symbol)
		}
		seenSymbols[first.Symbol] = struct{}{}
		if first.Symbol == "SPY" {
			if benchmarkID != uuid.Nil {
				return nil, fmt.Errorf("postgres: generated proposal scope has multiple SPY benchmark instruments")
			}
			benchmarkID = id
		}
		var contractCount int
		var venueContractID uuid.UUID
		if err := r.pool.QueryRow(ctx, `SELECT count(*),COALESCE(min(id::text),'00000000-0000-0000-0000-000000000000')::uuid
			FROM venue_contracts WHERE instrument_id=$1 AND venue=$2 AND valid_from<=$3 AND (valid_to IS NULL OR valid_to>=$4)`,
			id, first.Provider, report.EvaluationStart, report.EvaluationEnd).Scan(&contractCount, &venueContractID); err != nil {
			return nil, fmt.Errorf("postgres: load generated proposal venue contract: %w", err)
		}
		if contractCount != 1 || venueContractID == uuid.Nil {
			return nil, fmt.Errorf("postgres: generated proposal instrument %s resolves to %d executable venue contracts", id, contractCount)
		}
		digests := make([]string, 0, len(group.payloads))
		for _, payload := range group.payloads {
			digests = append(digests, payload.Digest())
		}
		setDigest := sha256.Sum256([]byte(strings.Join(digests, "\n")))
		bar := group.payloads[len(group.payloads)-1].Bar()
		summaries = append(summaries, generatedDailyStockInstrumentSummary{
			InstrumentID: id.String(), Symbol: first.Symbol, Provider: first.Provider, Feed: first.Feed, Timeframe: first.Timeframe,
			Adjustment: first.AdjustmentPolicy, ObservationCount: len(group.payloads), EffectiveStart: formatProposalEvidenceTime(first.EffectiveAt),
			EffectiveEnd: formatProposalEvidenceTime(latest.EffectiveAt), FirstSHA256: digests[0], LatestSHA256: digests[len(digests)-1],
			ContentSetSHA256: hex.EncodeToString(setDigest[:]), LatestOpen: bar.Open, LatestHigh: bar.High, LatestLow: bar.Low,
			LatestClose: bar.Close, LatestVolume: bar.Volume, LatestTradeCount: bar.TradeCount, LatestVWAP: bar.VWAP,
			VenueContractID: venueContractID.String(),
		})
	}
	if benchmarkID == uuid.Nil {
		return nil, fmt.Errorf("postgres: generated proposal scope requires exactly one immutable SPY benchmark")
	}
	allowedNames := []string{"close", "high", "low", "open", "trade_count", "volume", "vwap"}
	allowed := make([]generativestrategy.AllowedDataField, 0, len(allowedNames))
	for _, field := range allowedNames {
		allowed = append(allowed, generativestrategy.AllowedDataField{DatasetKind: dataset.KindBars, Field: field, Type: "decimal"})
	}
	items := make([]generativestrategy.ProposalEvidence, 0, min(limit, len(ids)))
	for index, instrumentID := range ids {
		key, err := generativestrategy.ReviewedDailyStockSpecKey(scopeID, instrumentID)
		if err != nil {
			return nil, err
		}
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM generated_strategy_specs WHERE family_id=$1 AND spec_key=$2)`, familyID, key).Scan(&exists); err != nil {
			return nil, fmt.Errorf("postgres: check existing generated proposal: %w", err)
		}
		if exists {
			continue
		}
		summary := generatedDailyStockEvidenceSummary{
			Schema: generatedDailyStockEvidenceSchemaV2, TrustedSpecKey: key, AccountID: accountID.String(), ScopeID: scopeID.String(),
			ManifestID: report.ManifestID.String(), ManifestSHA256: report.ManifestSHA256, QualityResultID: report.QualityResultID.String(),
			QualitySHA256: report.QualitySHA256, DecisionCutoff: formatProposalEvidenceTime(proposalCutoff), ManifestCutoff: formatProposalEvidenceTime(report.DecisionCutoff),
			EvaluationStart: formatProposalEvidenceTime(report.EvaluationStart), EvaluationEnd: formatProposalEvidenceTime(proposalCutoff),
			BenchmarkID: benchmarkID.String(), AllowedBarFields: allowedNames, Instruments: []generatedDailyStockInstrumentSummary{summaries[index]},
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return nil, fmt.Errorf("postgres: encode generated proposal evidence: %w", err)
		}
		items = append(items, generativestrategy.ProposalEvidence{
			Key: key, Universe: generativestrategy.Universe{AssetClass: instrument.AssetClassEquity, Instruments: []uuid.UUID{instrumentID}, Benchmark: benchmarkID},
			AllowedDataFields: allowed, ImmutableSummary: string(raw),
		})
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

func formatProposalEvidenceTime(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}

var _ generativestrategy.ProposalEvidenceSource = (*GenerativeStrategyRepo)(nil)

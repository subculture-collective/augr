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
	if provider == nil || provider.pool == nil || provider.reports == nil || provider.scopeID == uuid.Nil || provider.accountID == uuid.Nil || underlying == "" {
		return nil, fmt.Errorf("manifest-bound options provider requires scope, account, and underlying")
	}
	report, err := provider.reports.DiscoveryDeploymentReadinessForScope(ctx, provider.scopeID, provider.accountID)
	if err != nil {
		return nil, err
	}
	if !report.Options.Ready {
		return nil, fmt.Errorf("manifest-bound options evidence: %s", report.Options.Reason)
	}
	rows, err := provider.pool.Query(ctx, `SELECT snapshot.id,snapshot.content_sha256,snapshot.canonical_bytes,
		contract.id,contract.content_sha256,contract.canonical_bytes,
		quote.id,quote.content_sha256,quote.canonical_bytes
		FROM (
		  SELECT DISTINCT ON (payload.instrument_id) payload.*
		  FROM dataset_manifest_payload_bindings binding
		  JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
		  WHERE binding.manifest_id=$1 AND payload.payload_kind='option_snapshot' AND payload.underlying_symbol=$2
		  ORDER BY payload.instrument_id,payload.effective_at DESC,payload.available_at DESC,payload.id DESC
		) snapshot
		JOIN LATERAL (
		  SELECT payload.* FROM dataset_manifest_payload_bindings binding
		  JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
		  WHERE binding.manifest_id=$1 AND payload.payload_kind='option_contract'
		    AND payload.instrument_id=snapshot.instrument_id AND payload.available_at<=snapshot.available_at
		  ORDER BY payload.effective_at DESC,payload.available_at DESC,payload.id DESC LIMIT 1
		) contract ON true
		JOIN LATERAL (
		  SELECT payload.* FROM dataset_manifest_payload_bindings binding
		  JOIN dataset_market_payloads payload ON payload.id=binding.payload_id
		  WHERE binding.manifest_id=$1 AND payload.payload_kind='option_quote'
		    AND payload.instrument_id=snapshot.instrument_id AND payload.effective_at<=snapshot.effective_at
		    AND payload.available_at<=snapshot.available_at
		  ORDER BY payload.effective_at DESC,payload.available_at DESC,payload.id DESC LIMIT 1
		) quote ON true
		ORDER BY snapshot.symbol`, report.ManifestID, underlying)
	if err != nil {
		return nil, fmt.Errorf("query manifest-bound option chain: %w", err)
	}
	defer rows.Close()
	result := make([]domain.OptionSnapshot, 0)
	for rows.Next() {
		var snapshotID, contractID, quoteID uuid.UUID
		var snapshotDigest, contractDigest, quoteDigest string
		var snapshotRaw, contractRaw, quoteRaw []byte
		if err := rows.Scan(&snapshotID, &snapshotDigest, &snapshotRaw, &contractID, &contractDigest, &contractRaw, &quoteID, &quoteDigest, &quoteRaw); err != nil {
			return nil, fmt.Errorf("scan manifest-bound option chain: %w", err)
		}
		snapshotPayload, err := dataset.MarketPayloadFromCanonical(snapshotID, snapshotDigest, snapshotRaw)
		if err != nil {
			return nil, fmt.Errorf("reconstruct option snapshot: %w", err)
		}
		contractPayload, err := dataset.MarketPayloadFromCanonical(contractID, contractDigest, contractRaw)
		if err != nil {
			return nil, fmt.Errorf("reconstruct option contract: %w", err)
		}
		quotePayload, err := dataset.MarketPayloadFromCanonical(quoteID, quoteDigest, quoteRaw)
		if err != nil {
			return nil, fmt.Errorf("reconstruct option quote: %w", err)
		}
		contractBody, snapshotBody, quoteBody := contractPayload.Contract(), snapshotPayload.Snapshot(), quotePayload.Quote()
		if contractBody == nil || snapshotBody == nil || contractPayload.InstrumentID() != snapshotPayload.InstrumentID() {
			return nil, fmt.Errorf("manifest-bound option snapshot and contract do not reconstruct")
		}
		if quoteBody == nil || quotePayload.InstrumentID() != snapshotPayload.InstrumentID() || *quoteBody != snapshotBody.Quote {
			return nil, fmt.Errorf("manifest-bound option snapshot and exact quote do not reconstruct")
		}
		parsedExpiry, err := time.Parse("2006-01-02", contractBody.Expiry)
		if err != nil {
			return nil, err
		}
		kind := domain.OptionType(contractBody.OptionType)
		if (!expiry.IsZero() && !parsedExpiry.Equal(expiry.UTC())) || (optionType != "" && kind != optionType) {
			continue
		}
		strike, err := parseDatasetFloat(contractBody.Strike)
		if err != nil {
			return nil, err
		}
		multiplier, err := parseDatasetFloat(contractBody.Multiplier)
		if err != nil {
			return nil, err
		}
		bid, err := parseDatasetFloat(snapshotBody.Quote.BidPrice)
		if err != nil {
			return nil, err
		}
		ask, err := parseDatasetFloat(snapshotBody.Quote.AskPrice)
		if err != nil {
			return nil, err
		}
		bidSize, err := parseDatasetFloat(snapshotBody.Quote.BidSize)
		if err != nil {
			return nil, err
		}
		askSize, err := parseDatasetFloat(snapshotBody.Quote.AskSize)
		if err != nil {
			return nil, err
		}
		last, err := parseDatasetFloat(snapshotBody.LastTradePrice)
		if err != nil {
			return nil, err
		}
		lastSize, err := parseDatasetFloat(snapshotBody.LastTradeSize)
		if err != nil {
			return nil, err
		}
		greeks, err := parseSnapshotGreeks(snapshotBody)
		if err != nil {
			return nil, err
		}
		result = append(result, domain.OptionSnapshot{
			Contract: domain.OptionContract{
				InstrumentID: snapshotPayload.InstrumentID(), OCCSymbol: snapshotPayload.Symbol(), Underlying: underlying, OptionType: kind, Strike: strike,
				Expiry: parsedExpiry, Multiplier: multiplier, Style: contractBody.Style,
			},
			ContractPayloadID: contractID, ContractSHA256: contractDigest, QuotePayloadID: quoteID, QuoteSHA256: quoteDigest,
			SnapshotPayloadID: snapshotID, SnapshotSHA256: snapshotDigest,
			Greeks: greeks, Bid: bid, BidSize: bidSize, Ask: ask, AskSize: askSize, Mid: (bid + ask) / 2, Last: last, LastSize: lastSize,
			ObservedAt: snapshotPayload.EffectiveAt(), QuoteObservedAt: quotePayload.EffectiveAt(), LastTradeObservedAt: snapshotPayload.EffectiveAt(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("manifest-bound option chain has no matching complete contracts")
	}
	return result, nil
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

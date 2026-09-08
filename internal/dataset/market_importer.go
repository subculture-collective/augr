package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	MarketImportOriginProviderAPI  = "provider_api"
	MarketImportOriginMutableCache = "mutable_cache"
)

type MarketImportRequest struct {
	Provider               string
	Feed                   string
	Timeframe              string
	AdjustmentPolicy       string
	From                   time.Time
	To                     time.Time
	DecisionCutoff         time.Time
	Universe               []string
	MaxPayloads            int
	ExpectedPayloadCount   *int
	ExpectedPartitionCount *int
	SourceName             string
	Namespace              string
	SymbologyVersion       string
	Timezone               string
	Calendar               string
	License                string
	RetentionPolicy        string
	DryRun                 bool
}

type MarketImportSourceResult struct {
	Origin             string
	Entitled           bool
	PaginationComplete bool
	Payloads           []*MarketPayload
}

// MarketImportSource must fetch from the named provider during this import.
// Mutable caches are explicitly represented so they can only be rejected, not
// accidentally relabeled as promotion evidence.
type MarketImportSource interface {
	FetchMarketPayloads(context.Context, MarketImportRequest) (MarketImportSourceResult, error)
}

type BoundMarketDatasetRecorder interface {
	RecordBoundMarketDataset(context.Context, *BoundMarketDataset, time.Time) (*Manifest, error)
}

type MarketImportSummary struct {
	DryRun         bool
	ManifestID     string
	ManifestSHA256 string
	PayloadCount   int
	PartitionCount int
	PayloadSHA256  []string
}

type MarketImporter struct {
	source   MarketImportSource
	recorder BoundMarketDatasetRecorder
}

func NewMarketImporter(source MarketImportSource, recorder BoundMarketDatasetRecorder) (*MarketImporter, error) {
	if source == nil {
		return nil, fmt.Errorf("market importer requires a direct provider source")
	}
	return &MarketImporter{source: source, recorder: recorder}, nil
}

func (importer *MarketImporter) Import(ctx context.Context, request MarketImportRequest, createdAt time.Time) (*MarketImportSummary, error) {
	if importer == nil || importer.source == nil {
		return nil, fmt.Errorf("market importer is not configured")
	}
	if err := validateMarketImportRequest(request, createdAt); err != nil {
		return nil, err
	}
	result, err := importer.source.FetchMarketPayloads(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("fetch immutable market payloads: %w", err)
	}
	if result.Origin != MarketImportOriginProviderAPI {
		return nil, fmt.Errorf("market import origin %q is promotion-ineligible", result.Origin)
	}
	if !result.Entitled {
		return nil, fmt.Errorf("market data entitlement was not verified")
	}
	if !result.PaginationComplete {
		return nil, fmt.Errorf("market data page sequence is incomplete")
	}
	if len(result.Payloads) == 0 || len(result.Payloads) > request.MaxPayloads {
		return nil, fmt.Errorf("market import payload count %d is outside bounds 1..%d", len(result.Payloads), request.MaxPayloads)
	}
	if request.ExpectedPayloadCount != nil && len(result.Payloads) != *request.ExpectedPayloadCount {
		return nil, fmt.Errorf("market import payload count %d differs from expected %d", len(result.Payloads), *request.ExpectedPayloadCount)
	}
	if request.DecisionCutoff.IsZero() {
		for _, payload := range result.Payloads {
			if payload != nil && payload.Metadata().AvailableAt.After(request.DecisionCutoff) {
				request.DecisionCutoff = payload.Metadata().AvailableAt
			}
		}
	}

	bound, err := buildImportedMarketDataset(request, result.Payloads)
	if err != nil {
		return nil, err
	}
	manifest := bound.Manifest()
	partitions := manifest.Partitions()
	if request.ExpectedPartitionCount != nil && len(partitions) != *request.ExpectedPartitionCount {
		return nil, fmt.Errorf("market import partition count %d differs from expected %d", len(partitions), *request.ExpectedPartitionCount)
	}
	summary := &MarketImportSummary{
		DryRun: request.DryRun, ManifestID: manifest.ID().String(), ManifestSHA256: manifest.Digest(),
		PayloadCount: len(result.Payloads), PartitionCount: len(partitions), PayloadSHA256: make([]string, 0, len(result.Payloads)),
	}
	for _, payload := range result.Payloads {
		summary.PayloadSHA256 = append(summary.PayloadSHA256, payload.Digest())
	}
	sort.Strings(summary.PayloadSHA256)
	if request.DryRun {
		return summary, nil
	}
	if importer.recorder == nil {
		return nil, fmt.Errorf("market importer persistence is not configured")
	}
	if _, err := importer.recorder.RecordBoundMarketDataset(ctx, bound, createdAt); err != nil {
		return nil, fmt.Errorf("persist immutable market import: %w", err)
	}
	return summary, nil
}

func validateMarketImportRequest(request MarketImportRequest, createdAt time.Time) error {
	if !canonicalRequired(request.Provider) || !canonicalRequired(request.Feed) || !canonicalRequired(request.Timeframe) ||
		!canonicalRequired(request.AdjustmentPolicy) || !canonicalTimeValue(request.From) || !canonicalTimeValue(request.To) ||
		request.From.After(request.To) || (!request.DecisionCutoff.IsZero() && (!canonicalTimeValue(request.DecisionCutoff) || request.To.After(request.DecisionCutoff))) ||
		len(request.Universe) == 0 || request.MaxPayloads <= 0 || !canonicalRequired(request.SourceName) ||
		!canonicalRequired(request.Namespace) || !canonicalRequired(request.SymbologyVersion) || !canonicalRequired(request.Timezone) ||
		!canonicalRequired(request.Calendar) || !canonicalRequired(request.License) || !canonicalRequired(request.RetentionPolicy) ||
		createdAt.Location() != time.UTC || !createdAt.Equal(createdAt.Truncate(time.Microsecond)) {
		return fmt.Errorf("market import request requires explicit canonical provider, range, universe, bounds, provenance, and creation time")
	}
	if request.ExpectedPayloadCount != nil && (*request.ExpectedPayloadCount < 0 || *request.ExpectedPayloadCount > request.MaxPayloads) {
		return fmt.Errorf("market import expected payload count is outside bounds")
	}
	if request.ExpectedPartitionCount != nil && *request.ExpectedPartitionCount <= 0 {
		return fmt.Errorf("market import expected partition count must be positive")
	}
	seen := make(map[string]struct{}, len(request.Universe))
	for _, symbol := range request.Universe {
		if !canonicalRequired(symbol) || symbol != strings.ToUpper(symbol) {
			return fmt.Errorf("market import universe symbol %q is not canonical", symbol)
		}
		if _, exists := seen[symbol]; exists {
			return fmt.Errorf("market import universe symbol %q is duplicated", symbol)
		}
		seen[symbol] = struct{}{}
	}
	return nil
}

func buildImportedMarketDataset(request MarketImportRequest, payloads []*MarketPayload) (*BoundMarketDataset, error) {
	universe := make(map[string]struct{}, len(request.Universe))
	for _, symbol := range request.Universe {
		universe[symbol] = struct{}{}
	}
	groups := make(map[string][]*MarketPayload)
	for _, payload := range payloads {
		if payload == nil {
			return nil, fmt.Errorf("market import returned a nil payload")
		}
		metadata := payload.Metadata()
		if metadata.Provider != request.Provider || metadata.Feed != request.Feed || metadata.Timeframe != request.Timeframe ||
			metadata.AdjustmentPolicy != request.AdjustmentPolicy || metadata.EffectiveAt.Before(request.From) ||
			metadata.EffectiveAt.After(request.To) || metadata.AvailableAt.After(request.DecisionCutoff) {
			return nil, fmt.Errorf("market payload %s escapes the requested provider, feed, timeframe, policy, range, or cutoff", payload.Digest())
		}
		universeSymbol := metadata.Symbol
		if metadata.UnderlyingSymbol != "" {
			universeSymbol = metadata.UnderlyingSymbol
		}
		if _, allowed := universe[universeSymbol]; !allowed {
			return nil, fmt.Errorf("market payload %s escapes the requested universe", payload.Digest())
		}
		groupKey := string(metadata.Kind) + "\x00" + metadata.Symbol
		groups[groupKey] = append(groups[groupKey], payload)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	partitions := make([]PartitionInput, 0, len(keys))
	for _, key := range keys {
		values := groups[key]
		metadata := values[0].Metadata()
		observations := make([]ObservationInput, 0, len(values))
		for _, payload := range values {
			item := payload.Metadata()
			observations = append(observations, ObservationInput{
				SourceKey: item.Symbol + "/" + item.Timeframe + "/" + formatTime(item.EffectiveAt), InstrumentID: item.InstrumentID,
				EffectiveAt: item.EffectiveAt, PublishedAt: item.PublishedAt, ObservedAt: item.ObservedAt, AvailableAt: item.AvailableAt,
				Revision: item.Revision, CorrectionOf: item.CorrectionOfSHA256, ContentSHA256: payload.Digest(),
			})
		}
		requestBytes, _ := json.Marshal(struct {
			Provider  string            `json:"provider"`
			Feed      string            `json:"feed"`
			Symbol    string            `json:"symbol"`
			Kind      MarketPayloadKind `json:"kind"`
			Timeframe string            `json:"timeframe"`
			From      string            `json:"from"`
			To        string            `json:"to"`
		}{request.Provider, request.Feed, metadata.Symbol, metadata.Kind, request.Timeframe, formatTime(request.From), formatTime(request.To)})
		digestBytes := sha256.Sum256(requestBytes)
		partitions = append(partitions, PartitionInput{
			Kind: marketPayloadPartitionKind(metadata.Kind), Provider: request.Provider, Source: request.SourceName,
			Namespace:     request.Namespace + "/" + string(metadata.Kind) + "/" + metadata.Symbol,
			RequestSHA256: hex.EncodeToString(digestBytes[:]), MediaType: "application/json",
			SymbologyVersion: request.SymbologyVersion, AdjustmentPolicy: request.AdjustmentPolicy,
			Timezone: request.Timezone, Calendar: request.Calendar, Revision: "original",
			License: request.License, RetentionPolicy: request.RetentionPolicy, Observations: observations,
		})
	}
	manifest, err := NewManifest(ManifestInput{DecisionCutoff: request.DecisionCutoff, Partitions: partitions})
	if err != nil {
		return nil, fmt.Errorf("build market import manifest: %w", err)
	}
	bound, err := NewBoundMarketDataset(manifest, payloads)
	if err != nil {
		return nil, fmt.Errorf("bind market import manifest: %w", err)
	}
	return bound, nil
}

func marketPayloadPartitionKind(kind MarketPayloadKind) Kind {
	switch kind {
	case MarketPayloadStockBar, MarketPayloadOptionBar:
		return KindBars
	case MarketPayloadOptionQuote:
		return KindQuotes
	case MarketPayloadOptionContract:
		return KindOptionContracts
	case MarketPayloadOptionSnapshot:
		return KindOptionChains
	case MarketPayloadOptionTrade:
		return KindExternalObject
	default:
		return ""
	}
}

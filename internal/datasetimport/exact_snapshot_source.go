package datasetimport

import (
	"context"
	"fmt"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// fetchExactSnapshots emits source-backed quotes and acquisition-time snapshots.
// Reference contracts must be imported separately from actual reference evidence;
// OCC-derived defaults are not a provider contract response.
func (source *ProviderSource) fetchExactSnapshots(ctx context.Context, request dataset.MarketImportRequest, resolveAt time.Time) (dataset.MarketImportSourceResult, error) {
	if request.Timeframe != "snapshot" || request.AdjustmentPolicy != "raw" || len(request.Universe) == 0 {
		return dataset.MarketImportSourceResult{}, fmt.Errorf("exact snapshots require snapshot timeframe, raw policy and universe")
	}
	provider, ok := source.Options.(data.ExactOptionsSnapshotProvider)
	if !ok {
		return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider cannot supply exact snapshot source evidence")
	}
	result := dataset.MarketImportSourceResult{Origin: dataset.MarketImportOriginProviderAPI, Entitled: true, PaginationComplete: true}
	seen := map[string]bool{}
	expanded := 0
	for _, underlyingSymbol := range request.Universe {
		fetched, err := provider.GetExactOptionsSnapshotsWithReceipt(ctx, underlyingSymbol, request.Feed)
		if err != nil {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch exact snapshots: %w", err)
		}
		observedAt := source.Clock().UTC().Truncate(time.Microsecond)
		if observedAt.Before(request.From) || observedAt.After(request.To) || (!request.DecisionCutoff.IsZero() && observedAt.After(request.DecisionCutoff)) {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("snapshot acquisition escapes requested observation window")
		}
		if err := validateReceipt(underlyingSymbol, fetched.Receipt, request); err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		if fetched.Receipt.Pages != len(fetched.Pages) || len(fetched.Snapshots) == 0 {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("exact snapshot source pages or snapshots missing")
		}
		for _, snapshot := range fetched.Snapshots {
			contract, err := domain.ParseStrictOCC(snapshot.Symbol)
			if err != nil || contract.Underlying != underlyingSymbol || seen[snapshot.Symbol] || snapshot.PageIndex < 0 || snapshot.PageIndex >= len(fetched.Pages) || snapshot.LatestTrade == nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("incomplete or invalid exact snapshot identity/page/trade")
			}
			seen[snapshot.Symbol] = true
			_, resolved, underlying, err := source.resolveOption(ctx, request, snapshot.Symbol, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, err
			}
			if snapshot.QuoteTimestamp.Before(request.From) || snapshot.QuoteTimestamp.After(request.To) {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("snapshot quote escapes requested event window")
			}
			page := fetched.Pages[snapshot.PageIndex]
			expanded += 2 * (len(page.Body) + len(snapshot.Raw))
			if expanded > 64*1024*1024 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("exact snapshot source expansion exceeds 64 MiB")
			}
			quote := dataset.QuotePayload{BidPrice: snapshot.BidPrice, BidSize: snapshot.BidSize, AskPrice: snapshot.AskPrice, AskSize: snapshot.AskSize}
			input := dataset.MarketPayloadInput{Kind: dataset.MarketPayloadOptionQuote, InstrumentID: resolved.ID, UnderlyingInstrumentID: underlying.ID, UnderlyingSymbol: underlyingSymbol, Symbol: snapshot.Symbol, Provider: request.Provider, Feed: request.Feed, Timeframe: request.Timeframe, AdjustmentPolicy: request.AdjustmentPolicy, EffectiveAt: snapshot.QuoteTimestamp, ObservedAt: observedAt, AvailableAt: observedAt, Revision: "original", Quote: &quote, SourceEvidence: &dataset.SourcePageEvidence{RequestPath: page.RequestPath, Query: page.Query, Page: page.Body, Row: snapshot.Raw, SymbolKey: snapshot.Symbol}}
			quotePayload, err := dataset.NewMarketPayload(input)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize exact snapshot quote: %w", err)
			}
			input.Kind, input.Quote, input.EffectiveAt = dataset.MarketPayloadOptionSnapshot, nil, observedAt
			input.Snapshot = &dataset.OptionSnapshotPayload{Quote: quote, LastTradePrice: snapshot.LatestTrade.Price, LastTradeSize: snapshot.LatestTrade.Size, ImpliedVolatility: snapshot.ImpliedVolatility, Delta: snapshot.Delta, Gamma: snapshot.Gamma, Theta: snapshot.Theta, Vega: snapshot.Vega, Rho: snapshot.Rho}
			payload, err := dataset.NewMarketPayload(input)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize exact snapshot aggregate: %w", err)
			}
			result.Payloads = append(result.Payloads, quotePayload, payload)
		}
	}
	return result, nil
}

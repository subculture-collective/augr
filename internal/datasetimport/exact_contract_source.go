package datasetimport

import (
	"context"
	"fmt"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

// fetchExactContracts does not create instruments or infer execution mechanics.
// Existing canonical references must match the current provider observation.
func (source *ProviderSource) fetchExactContracts(ctx context.Context, request dataset.MarketImportRequest) (dataset.MarketImportSourceResult, error) {
	var empty dataset.MarketImportSourceResult
	if source.Contracts == nil || request.Provider != "alpaca" || request.Feed != "reference" || request.Timeframe != "snapshot" || request.AdjustmentPolicy != "raw" || len(source.OptionSymbols) == 0 || len(source.OptionSymbols) > 100 || request.From.IsZero() || request.To.Before(request.From) {
		return empty, fmt.Errorf("exact contracts require bounded explicit symbols and current reference request")
	}
	seen := map[string]bool{}
	for _, symbol := range source.OptionSymbols {
		occ, err := domain.ParseStrictOCC(symbol)
		if err != nil || seen[symbol] || !contains(request.Universe, occ.Underlying) {
			return empty, fmt.Errorf("invalid, duplicate or out-of-universe contract symbol")
		}
		seen[symbol] = true
	}
	result := dataset.MarketImportSourceResult{Origin: dataset.MarketImportOriginProviderAPI, Entitled: true, PaginationComplete: true}
	for _, symbol := range source.OptionSymbols {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		before := source.Clock().UTC()
		if before.Before(request.From) || before.After(request.To) || (!request.DecisionCutoff.IsZero() && before.After(request.DecisionCutoff)) {
			return empty, fmt.Errorf("contract request is outside current acquisition window")
		}
		fetched, err := source.Contracts.GetExactOptionContract(ctx, symbol)
		if err != nil {
			return empty, fmt.Errorf("fetch exact contract: %w", err)
		}
		after := source.Clock().UTC()
		observed := fetched.ObservedAt
		if observed.Before(before) || observed.After(after) || observed.After(request.To) || (!request.DecisionCutoff.IsZero() && observed.After(request.DecisionCutoff)) {
			return empty, fmt.Errorf("contract observation time escapes acquisition bounds")
		}
		// Never backdate provider observation or availability when normalizing.
		observed = contractTimestampCeil(observed)
		available := contractTimestampCeil(after)
		if observed.After(request.To) || (!request.DecisionCutoff.IsZero() && available.After(request.DecisionCutoff)) {
			return empty, fmt.Errorf("canonical contract acquisition escapes cutoff")
		}
		// Whole-object binding and the 100-symbol cap limit source expansion.
		if len(fetched.Page.Body) > 65536 || len(fetched.Contract.Raw) > 65536 {
			return empty, fmt.Errorf("contract source exceeds bounded object size")
		}
		_, resolved, underlying, err := source.resolveOption(ctx, request, symbol, observed)
		if err != nil {
			return empty, err
		}
		contract := fetched.Contract
		if resolved.Validate() != nil || underlying.Validate() != nil || resolved.Status != instrument.StatusActive || underlying.Status != instrument.StatusActive || resolved.AssetClass != instrument.AssetClassOption || (underlying.AssetClass != instrument.AssetClassEquity && underlying.AssetClass != instrument.AssetClassETF) || resolved.CreatedAt.After(observed) || underlying.CreatedAt.After(observed) || resolved.Expiration == nil || resolved.Multiplier.String() != contract.Size || string(resolved.ExerciseStyle) != contract.Style || resolved.Expiration.Format("2006-01-02") != contract.ExpirationDate || contract.Symbol != symbol {
			return empty, fmt.Errorf("contract reference mechanics do not match provider observation")
		}
		payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{Kind: dataset.MarketPayloadOptionContract, InstrumentID: resolved.ID, UnderlyingInstrumentID: underlying.ID, Symbol: symbol, UnderlyingSymbol: contract.UnderlyingSymbol, Provider: request.Provider, Feed: request.Feed, Timeframe: request.Timeframe, AdjustmentPolicy: request.AdjustmentPolicy, EffectiveAt: observed, ObservedAt: observed, AvailableAt: available, Revision: "original", Contract: &dataset.OptionContractPayload{OptionType: contract.OptionType, Strike: contract.StrikePrice, Expiry: contract.ExpirationDate, Multiplier: contract.Size, Style: contract.Style}, SourceEvidence: &dataset.SourcePageEvidence{RequestPath: fetched.Page.RequestPath, Query: fetched.Page.Query, Page: fetched.Page.Body, Row: contract.Raw, SymbolKey: symbol}})
		if err != nil {
			return empty, fmt.Errorf("canonicalize exact contract: %w", err)
		}
		result.Payloads = append(result.Payloads, payload)
	}
	return result, nil
}

func contractTimestampCeil(value time.Time) time.Time {
	value = value.UTC()
	truncated := value.Truncate(time.Microsecond)
	if truncated.Before(value) {
		return truncated.Add(time.Microsecond)
	}
	return truncated
}

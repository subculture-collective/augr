// Package datasetimport adapts live provider clients to the immutable dataset
// importer. It deliberately has no dependency on the mutable historical cache.
package datasetimport

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

type Mode string

const (
	ModeStockBars           Mode = "stock_bars"
	ModeOptionBars          Mode = "option_bars"
	ModeOptionTrades        Mode = "option_trades"
	ModeOptionChainSnapshot Mode = "option_chain_snapshot"
	ModeOptionContracts     Mode = "option_contracts"
)

type InstrumentResolver interface {
	ResolveAlias(context.Context, string, instrument.AliasType, string, time.Time) (*instrument.Instrument, error)
}

type ProviderSource struct {
	Mode          Mode
	Stock         data.DataProvider
	Options       data.OptionsDataProvider
	Contracts     data.ExactOptionContractProvider
	Instruments   InstrumentResolver
	OptionSymbols []string
	Clock         func() time.Time
}

type fetchedExactStock struct {
	underlying   string
	underlyingID uuid.UUID
	symbol       string
	instrumentID uuid.UUID
	result       data.ExactHistoricalResult
}

type fetchedTrades struct {
	symbol       string
	underlying   string
	instrumentID uuid.UUID
	underlyingID uuid.UUID
	result       data.ExactOptionsTradeResult
}

func (source *ProviderSource) FetchMarketPayloads(ctx context.Context, request dataset.MarketImportRequest) (dataset.MarketImportSourceResult, error) {
	if source == nil || source.Instruments == nil || source.Clock == nil {
		return dataset.MarketImportSourceResult{}, fmt.Errorf("provider market import source is incomplete")
	}
	resolveAt := request.DecisionCutoff
	if resolveAt.IsZero() {
		resolveAt = source.Clock().UTC().Truncate(time.Microsecond)
	}
	var exactStocks []fetchedExactStock
	var tradeSets []fetchedTrades
	switch source.Mode {
	case ModeOptionContracts:
		return source.fetchExactContracts(ctx, request)
	case ModeStockBars:
		timeframe, err := parseTimeframe(request.Timeframe)
		if err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		if source.Stock == nil {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("stock provider is not configured")
		}
		verified, ok := source.Stock.(data.ExactHistoricalProvider)
		if !ok {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("stock provider cannot supply exact source evidence")
		}
		for _, symbol := range request.Universe {
			resolved, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasTicker, symbol, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("resolve stock %s: %w", symbol, err)
			}
			result, err := verified.GetExactOHLCVWithReceipt(ctx, symbol, timeframe, request.From, request.To, request.Feed, request.AdjustmentPolicy)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch stock bars for %s: %w", symbol, err)
			}
			if len(result.Bars) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no stock bars for %s", symbol)
			}
			exactStocks = append(exactStocks, fetchedExactStock{symbol: symbol, instrumentID: resolved.ID, result: result})
		}
	case ModeOptionBars:
		timeframe, err := parseTimeframe(request.Timeframe)
		if err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		if source.Options == nil || len(source.OptionSymbols) == 0 {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider and explicit OCC symbols are required")
		}
		verified, ok := source.Options.(data.ExactOptionsHistoricalProvider)
		if !ok {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider cannot supply exact source evidence")
		}
		optionSymbols := append([]string(nil), source.OptionSymbols...)
		sort.Strings(optionSymbols)
		for index, symbol := range optionSymbols {
			if index > 0 && symbol == optionSymbols[index-1] {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("OCC symbol %s is duplicated", symbol)
			}
			contract, err := domain.ParseOCC(symbol)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("parse OCC symbol %s: %w", symbol, err)
			}
			if !contains(request.Universe, contract.Underlying) {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("option %s escapes requested underlying universe", symbol)
			}
			resolved, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasOCC, symbol, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("resolve option %s: %w", symbol, err)
			}
			underlying, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasTicker, contract.Underlying, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("resolve option underlying %s: %w", contract.Underlying, err)
			}
			if resolved.UnderlyingID == nil || *resolved.UnderlyingID != underlying.ID {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("option %s canonical underlying binding does not reconstruct", symbol)
			}
			result, err := verified.GetExactOptionsOHLCVWithReceipt(ctx, symbol, timeframe, request.From, request.To, request.Feed, request.AdjustmentPolicy)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch option bars for %s: %w", symbol, err)
			}
			if len(result.Bars) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no option bars for %s", symbol)
			}
			exactStocks = append(exactStocks, fetchedExactStock{
				symbol: symbol, underlying: contract.Underlying, instrumentID: resolved.ID, underlyingID: underlying.ID, result: result,
			})
		}
	case ModeOptionTrades:
		if request.Timeframe != "trade" || request.AdjustmentPolicy != "raw" || source.Options == nil || len(source.OptionSymbols) == 0 {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("option trades require timeframe trade, raw adjustment policy, provider, and explicit OCC symbols")
		}
		verified, ok := source.Options.(data.ExactOptionsTradeProvider)
		if !ok {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider cannot supply exact trade source evidence")
		}
		optionSymbols := append([]string(nil), source.OptionSymbols...)
		sort.Strings(optionSymbols)
		for index, symbol := range optionSymbols {
			if index > 0 && symbol == optionSymbols[index-1] {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("OCC symbol %s is duplicated", symbol)
			}
			contract, resolved, underlying, err := source.resolveOption(ctx, request, symbol, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, err
			}
			result, err := verified.GetExactOptionsTradesWithReceipt(ctx, symbol, request.From, request.To, request.Feed)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch option trades for %s: %w", symbol, err)
			}
			if len(result.Trades) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no option trades for %s", symbol)
			}
			tradeSets = append(tradeSets, fetchedTrades{symbol: contract.OCCSymbol, underlying: contract.Underlying, instrumentID: resolved.ID, underlyingID: underlying.ID, result: result})
		}
	case ModeOptionChainSnapshot:
		return source.fetchExactSnapshots(ctx, request, resolveAt)
	default:
		return dataset.MarketImportSourceResult{}, fmt.Errorf("unsupported provider import mode %q", source.Mode)
	}

	observedAt := source.Clock().UTC().Truncate(time.Microsecond)
	if !request.DecisionCutoff.IsZero() && observedAt.After(request.DecisionCutoff) {
		return dataset.MarketImportSourceResult{}, fmt.Errorf("provider response time is after decision cutoff")
	}
	payloads := make([]*dataset.MarketPayload, 0)
	expandedSourceBytes := 0
	for _, fetched := range exactStocks {
		if err := validateReceipt(fetched.symbol, fetched.result.Receipt, request); err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		if fetched.result.Receipt.Pages != len(fetched.result.Pages) {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("exact source page count mismatch")
		}
		for _, bar := range fetched.result.Bars {
			if bar.PageIndex < 0 || bar.PageIndex >= len(fetched.result.Pages) || bar.TradeCount == "" || bar.VWAP == "" {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("exact bar source lacks bound page or required bar fields")
			}
			page := fetched.result.Pages[bar.PageIndex]
			expandedSourceBytes += len(page.Body) + len(bar.Raw)
			if expandedSourceBytes > 64*1024*1024 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("exact source expansion exceeds 64 MiB import bound; request smaller intervals")
			}
			effectiveAt := bar.Timestamp.UTC().Truncate(time.Microsecond)
			publishedAt, err := barPublicationAt(effectiveAt, request.Timeframe)
			if err != nil {
				return dataset.MarketImportSourceResult{}, err
			}
			kind := dataset.MarketPayloadStockBar
			symbolKey := ""
			if source.Mode == ModeOptionBars {
				kind, symbolKey = dataset.MarketPayloadOptionBar, fetched.symbol
			}
			payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
				Kind: kind, InstrumentID: fetched.instrumentID, UnderlyingInstrumentID: fetched.underlyingID, UnderlyingSymbol: fetched.underlying,
				Provider: request.Provider, Feed: request.Feed, Symbol: fetched.symbol, Timeframe: request.Timeframe, AdjustmentPolicy: request.AdjustmentPolicy,
				EffectiveAt: effectiveAt, PublishedAt: &publishedAt, ObservedAt: observedAt, AvailableAt: observedAt, Revision: "original",
				Bar:            &dataset.BarPayload{Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, TradeCount: bar.TradeCount, VWAP: bar.VWAP},
				SourceEvidence: &dataset.SourcePageEvidence{RequestPath: page.RequestPath, Query: page.Query, Page: page.Body, RowIndex: bar.RowIndex, Row: bar.Raw, SymbolKey: symbolKey},
			})
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize exact bar: %w", err)
			}
			payloads = append(payloads, payload)
		}
	}
	for _, result := range tradeSets {
		if err := validateReceipt(result.symbol, result.result.Receipt, request); err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		if result.result.Receipt.Pages != len(result.result.Pages) {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("exact trade source page count mismatch")
		}
		seenTrades := make(map[string]bool)
		for _, trade := range result.result.Trades {
			if trade.PageIndex < 0 || trade.PageIndex >= len(result.result.Pages) || seenTrades[trade.ProviderID] {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("exact trade source page or identity invalid")
			}
			seenTrades[trade.ProviderID] = true
			page := result.result.Pages[trade.PageIndex]
			expandedSourceBytes += len(page.Body) + len(trade.Raw)
			if expandedSourceBytes > 64*1024*1024 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("exact trade source expansion exceeds 64 MiB import bound")
			}
			effectiveAt := trade.Timestamp.UTC()
			providerTradeID, idErr := strconv.ParseInt(trade.ProviderID, 10, 64)
			if idErr != nil || providerTradeID <= 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("option trade %s lacks canonical provider identity", result.symbol)
			}
			if effectiveAt.Before(request.From) || effectiveAt.After(request.To) {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("option trade %s escapes requested interval", result.symbol)
			}
			payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
				Kind: dataset.MarketPayloadOptionTrade, InstrumentID: result.instrumentID, UnderlyingInstrumentID: result.underlyingID,
				Provider: request.Provider, Feed: request.Feed, Symbol: result.symbol, UnderlyingSymbol: result.underlying,
				Timeframe: request.Timeframe, AdjustmentPolicy: request.AdjustmentPolicy, EffectiveAt: effectiveAt,
				ObservedAt: observedAt, AvailableAt: observedAt, Revision: "trade_" + trade.ProviderID,
				Trade:          &dataset.TradePayload{Price: trade.Price, Size: trade.Size, Exchange: trade.Exchange},
				SourceEvidence: &dataset.SourcePageEvidence{RequestPath: page.RequestPath, Query: page.Query, Page: page.Body, RowIndex: trade.RowIndex, Row: trade.Raw, SymbolKey: result.symbol},
			})
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize option trade %s at %s: %w", result.symbol, effectiveAt, err)
			}
			payloads = append(payloads, payload)
		}
	}
	return dataset.MarketImportSourceResult{
		Origin: dataset.MarketImportOriginProviderAPI, Entitled: true, PaginationComplete: true, Payloads: payloads,
	}, nil
}

func (source *ProviderSource) resolveOption(ctx context.Context, request dataset.MarketImportRequest, symbol string, resolveAt time.Time) (*domain.OptionContract, *instrument.Instrument, *instrument.Instrument, error) {
	contract, err := domain.ParseOCC(symbol)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse OCC symbol %s: %w", symbol, err)
	}
	if !contains(request.Universe, contract.Underlying) {
		return nil, nil, nil, fmt.Errorf("option %s escapes requested underlying universe", symbol)
	}
	resolved, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasOCC, symbol, resolveAt)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve option %s: %w", symbol, err)
	}
	underlying, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasTicker, contract.Underlying, resolveAt)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve option underlying %s: %w", contract.Underlying, err)
	}
	if resolved == nil || underlying == nil || resolved.UnderlyingID == nil || *resolved.UnderlyingID != underlying.ID {
		return nil, nil, nil, fmt.Errorf("option %s canonical underlying binding does not reconstruct", symbol)
	}
	return contract, resolved, underlying, nil
}

func validateReceipt(symbol string, receipt data.HistoricalFetchReceipt, request dataset.MarketImportRequest) error {
	if !receipt.Entitled || !receipt.PaginationComplete || receipt.Pages <= 0 {
		return fmt.Errorf("provider receipt for %s is incomplete", symbol)
	}
	if receipt.Provider != request.Provider || receipt.Feed != request.Feed || receipt.AdjustmentPolicy != request.AdjustmentPolicy {
		return fmt.Errorf("provider receipt for %s does not match requested provenance", symbol)
	}
	return nil
}

func parseTimeframe(value string) (data.Timeframe, error) {
	switch data.Timeframe(value) {
	case data.Timeframe1m, data.Timeframe5m, data.Timeframe15m, data.Timeframe1h, data.Timeframe1d:
		return data.Timeframe(value), nil
	default:
		return "", fmt.Errorf("unsupported market import timeframe %q", value)
	}
}

func barPublicationAt(effectiveAt time.Time, timeframe string) (time.Time, error) {
	var duration time.Duration
	switch data.Timeframe(timeframe) {
	case data.Timeframe1m:
		duration = time.Minute
	case data.Timeframe5m:
		duration = 5 * time.Minute
	case data.Timeframe15m:
		duration = 15 * time.Minute
	case data.Timeframe1h:
		duration = time.Hour
	case data.Timeframe1d:
		duration = 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("unsupported market import timeframe %q", timeframe)
	}
	return effectiveAt.Add(duration).UTC().Truncate(time.Microsecond), nil
}

func contains(values []string, sought string) bool {
	for _, value := range values {
		if strings.EqualFold(value, sought) {
			return true
		}
	}
	return false
}

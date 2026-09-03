// Package datasetimport adapts live provider clients to the immutable dataset
// importer. It deliberately has no dependency on the mutable historical cache.
package datasetimport

import (
	"context"
	"fmt"
	"math"
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
)

type InstrumentResolver interface {
	ResolveAlias(context.Context, string, instrument.AliasType, string, time.Time) (*instrument.Instrument, error)
}

type ProviderSource struct {
	Mode          Mode
	Stock         data.DataProvider
	Options       data.OptionsDataProvider
	Instruments   InstrumentResolver
	OptionSymbols []string
	Clock         func() time.Time
}

type fetchedBars struct {
	symbol       string
	underlying   string
	instrumentID uuid.UUID
	underlyingID uuid.UUID
	bars         []domain.OHLCV
	receipt      data.HistoricalFetchReceipt
}

type fetchedSnapshot struct {
	snapshot     domain.OptionSnapshot
	instrumentID uuid.UUID
	underlyingID uuid.UUID
	receipt      data.HistoricalFetchReceipt
}

type fetchedTrades struct {
	symbol       string
	underlying   string
	instrumentID uuid.UUID
	underlyingID uuid.UUID
	trades       []data.OptionTradeObservation
	receipt      data.HistoricalFetchReceipt
}

func (source *ProviderSource) FetchMarketPayloads(ctx context.Context, request dataset.MarketImportRequest) (dataset.MarketImportSourceResult, error) {
	if source == nil || source.Instruments == nil || source.Clock == nil {
		return dataset.MarketImportSourceResult{}, fmt.Errorf("provider market import source is incomplete")
	}
	resolveAt := request.DecisionCutoff
	if resolveAt.IsZero() {
		resolveAt = source.Clock().UTC().Truncate(time.Microsecond)
	}
	var fetched []fetchedBars
	var snapshots []fetchedSnapshot
	var tradeSets []fetchedTrades
	switch source.Mode {
	case ModeStockBars:
		timeframe, err := parseTimeframe(request.Timeframe)
		if err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		if source.Stock == nil {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("stock provider is not configured")
		}
		verified, ok := source.Stock.(data.VerifiedStockHistoricalProvider)
		if !ok {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("stock provider cannot prove entitlement and pagination")
		}
		for _, symbol := range request.Universe {
			resolved, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasTicker, symbol, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("resolve stock %s: %w", symbol, err)
			}
			bars, receipt, err := verified.GetOHLCVWithReceipt(ctx, symbol, timeframe, request.From, request.To, request.Feed, request.AdjustmentPolicy)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch stock bars for %s: %w", symbol, err)
			}
			if len(bars) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no stock bars for %s", symbol)
			}
			fetched = append(fetched, fetchedBars{symbol: symbol, instrumentID: resolved.ID, bars: bars, receipt: receipt})
		}
	case ModeOptionBars:
		timeframe, err := parseTimeframe(request.Timeframe)
		if err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		if source.Options == nil || len(source.OptionSymbols) == 0 {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider and explicit OCC symbols are required")
		}
		verified, ok := source.Options.(data.VerifiedOptionsHistoricalProvider)
		if !ok {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider cannot prove entitlement and pagination")
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
			bars, receipt, err := verified.GetOptionsOHLCVWithReceipt(ctx, symbol, timeframe, request.From, request.To, request.Feed, request.AdjustmentPolicy)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch option bars for %s: %w", symbol, err)
			}
			if len(bars) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no option bars for %s", symbol)
			}
			fetched = append(fetched, fetchedBars{
				symbol: symbol, underlying: contract.Underlying, instrumentID: resolved.ID, underlyingID: underlying.ID, bars: bars, receipt: receipt,
			})
		}
	case ModeOptionTrades:
		if request.Timeframe != "trade" || request.AdjustmentPolicy != "raw" || source.Options == nil || len(source.OptionSymbols) == 0 {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("option trades require timeframe trade, raw adjustment policy, provider, and explicit OCC symbols")
		}
		verified, ok := source.Options.(data.VerifiedOptionsTradeProvider)
		if !ok {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider cannot prove trade entitlement and pagination")
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
			trades, receipt, err := verified.GetOptionsTradesWithReceipt(ctx, symbol, request.From, request.To, request.Feed)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch option trades for %s: %w", symbol, err)
			}
			if len(trades) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no option trades for %s", symbol)
			}
			tradeSets = append(tradeSets, fetchedTrades{symbol: contract.OCCSymbol, underlying: contract.Underlying, instrumentID: resolved.ID, underlyingID: underlying.ID, trades: trades, receipt: receipt})
		}
	case ModeOptionChainSnapshot:
		if request.Timeframe != "snapshot" || request.AdjustmentPolicy != "raw" {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("option chain snapshots require timeframe snapshot and raw adjustment policy")
		}
		verified, ok := source.Options.(data.VerifiedOptionsSnapshotProvider)
		if !ok {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider cannot prove snapshot entitlement and pagination")
		}
		for _, underlyingSymbol := range request.Universe {
			underlying, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasTicker, underlyingSymbol, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("resolve option underlying %s: %w", underlyingSymbol, err)
			}
			chain, receipt, err := verified.GetOptionsChainWithReceipt(ctx, underlyingSymbol, time.Time{}, "", request.Feed)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch option chain for %s: %w", underlyingSymbol, err)
			}
			if len(chain) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no option snapshots for %s", underlyingSymbol)
			}
			for _, snapshot := range chain {
				if snapshot.Contract.Underlying != underlyingSymbol || snapshot.ObservedAt.IsZero() {
					return dataset.MarketImportSourceResult{}, fmt.Errorf("option snapshot for %s lacks exact canonical identity or observation time", snapshot.Contract.OCCSymbol)
				}
				resolved, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasOCC, snapshot.Contract.OCCSymbol, resolveAt)
				if err != nil {
					return dataset.MarketImportSourceResult{}, fmt.Errorf("resolve option %s: %w", snapshot.Contract.OCCSymbol, err)
				}
				if resolved.UnderlyingID == nil || *resolved.UnderlyingID != underlying.ID {
					return dataset.MarketImportSourceResult{}, fmt.Errorf("option %s canonical underlying binding does not reconstruct", snapshot.Contract.OCCSymbol)
				}
				snapshots = append(snapshots, fetchedSnapshot{snapshot: snapshot, instrumentID: resolved.ID, underlyingID: underlying.ID, receipt: receipt})
			}
		}
	default:
		return dataset.MarketImportSourceResult{}, fmt.Errorf("unsupported provider import mode %q", source.Mode)
	}

	observedAt := source.Clock().UTC().Truncate(time.Microsecond)
	if !request.DecisionCutoff.IsZero() && observedAt.After(request.DecisionCutoff) {
		return dataset.MarketImportSourceResult{}, fmt.Errorf("provider response time is after decision cutoff")
	}
	payloads := make([]*dataset.MarketPayload, 0)
	for _, result := range fetched {
		if !result.receipt.Entitled || !result.receipt.PaginationComplete || result.receipt.Pages <= 0 {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("provider receipt for %s is incomplete", result.symbol)
		}
		if result.receipt.Provider != request.Provider || result.receipt.Feed != request.Feed || result.receipt.AdjustmentPolicy != request.AdjustmentPolicy {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("provider receipt for %s does not match requested provenance", result.symbol)
		}
		for _, bar := range result.bars {
			effectiveAt := bar.Timestamp.UTC().Truncate(time.Microsecond)
			payloadKind := dataset.MarketPayloadStockBar
			if source.Mode == ModeOptionBars {
				payloadKind = dataset.MarketPayloadOptionBar
			}
			payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
				Kind: payloadKind, InstrumentID: result.instrumentID, UnderlyingInstrumentID: result.underlyingID,
				Provider: request.Provider, Feed: request.Feed, Symbol: result.symbol, UnderlyingSymbol: result.underlying,
				Timeframe: request.Timeframe, AdjustmentPolicy: request.AdjustmentPolicy, EffectiveAt: effectiveAt,
				ObservedAt: observedAt, AvailableAt: observedAt, Revision: "original",
				Bar: &dataset.BarPayload{
					Open: canonicalFloat(bar.Open), High: canonicalFloat(bar.High), Low: canonicalFloat(bar.Low),
					Close: canonicalFloat(bar.Close), Volume: canonicalFloat(bar.Volume), TradeCount: "0", VWAP: "0",
				},
			})
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize provider bar %s at %s: %w", result.symbol, effectiveAt, err)
			}
			payloads = append(payloads, payload)
		}
	}
	for _, result := range snapshots {
		if err := validateReceipt(result.snapshot.Contract.OCCSymbol, result.receipt, request); err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		effectiveAt := result.snapshot.ObservedAt.UTC().Truncate(time.Microsecond)
		if result.snapshot.QuoteObservedAt.IsZero() {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("option snapshot %s lacks quote observation time", result.snapshot.Contract.OCCSymbol)
		}
		if effectiveAt.Before(request.From) || effectiveAt.After(request.To) || (!request.DecisionCutoff.IsZero() && effectiveAt.After(request.DecisionCutoff)) {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("option snapshot %s escapes requested observation window", result.snapshot.Contract.OCCSymbol)
		}
		common := dataset.MarketPayloadInput{
			InstrumentID: result.instrumentID, UnderlyingInstrumentID: result.underlyingID,
			Provider: request.Provider, Feed: request.Feed, Symbol: result.snapshot.Contract.OCCSymbol,
			UnderlyingSymbol: result.snapshot.Contract.Underlying, Timeframe: request.Timeframe,
			AdjustmentPolicy: request.AdjustmentPolicy, EffectiveAt: effectiveAt, ObservedAt: observedAt,
			AvailableAt: observedAt, Revision: "original",
		}
		contractInput := common
		contractInput.Kind = dataset.MarketPayloadOptionContract
		contractInput.Contract = &dataset.OptionContractPayload{
			OptionType: string(result.snapshot.Contract.OptionType), Strike: canonicalFloat(result.snapshot.Contract.Strike),
			Expiry: result.snapshot.Contract.Expiry.UTC().Format("2006-01-02"), Multiplier: canonicalFloat(result.snapshot.Contract.Multiplier),
			Style: result.snapshot.Contract.Style,
		}
		contractPayload, err := dataset.NewMarketPayload(contractInput)
		if err != nil {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize option contract %s: %w", result.snapshot.Contract.OCCSymbol, err)
		}
		quoteInput := common
		quoteInput.Kind = dataset.MarketPayloadOptionQuote
		quoteInput.EffectiveAt = result.snapshot.QuoteObservedAt.UTC().Truncate(time.Microsecond)
		quoteInput.Quote = &dataset.QuotePayload{
			BidPrice: canonicalFloat(result.snapshot.Bid), BidSize: canonicalFloat(result.snapshot.BidSize),
			AskPrice: canonicalFloat(result.snapshot.Ask), AskSize: canonicalFloat(result.snapshot.AskSize),
		}
		quotePayload, err := dataset.NewMarketPayload(quoteInput)
		if err != nil {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize option quote %s: %w", result.snapshot.Contract.OCCSymbol, err)
		}
		snapshotInput := common
		snapshotInput.Kind = dataset.MarketPayloadOptionSnapshot
		snapshotInput.Snapshot = &dataset.OptionSnapshotPayload{
			Quote:          dataset.QuotePayload{BidPrice: canonicalFloat(result.snapshot.Bid), BidSize: canonicalFloat(result.snapshot.BidSize), AskPrice: canonicalFloat(result.snapshot.Ask), AskSize: canonicalFloat(result.snapshot.AskSize)},
			LastTradePrice: canonicalFloat(result.snapshot.Last), LastTradeSize: canonicalFloat(result.snapshot.LastSize),
			ImpliedVolatility: canonicalFloat(result.snapshot.Greeks.IV), Delta: canonicalFloat(result.snapshot.Greeks.Delta),
			Gamma: canonicalFloat(result.snapshot.Greeks.Gamma), Theta: canonicalFloat(result.snapshot.Greeks.Theta),
			Vega: canonicalFloat(result.snapshot.Greeks.Vega), Rho: canonicalFloat(result.snapshot.Greeks.Rho),
		}
		snapshotPayload, err := dataset.NewMarketPayload(snapshotInput)
		if err != nil {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize option snapshot %s: %w", result.snapshot.Contract.OCCSymbol, err)
		}
		payloads = append(payloads, contractPayload, quotePayload)
		if result.snapshot.Last > 0 && result.snapshot.LastSize > 0 &&
			!result.snapshot.LastTradeObservedAt.Before(request.From) && !result.snapshot.LastTradeObservedAt.After(request.To) {
			if result.snapshot.LastTradeObservedAt.IsZero() {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("option snapshot %s has a trade without its event time", result.snapshot.Contract.OCCSymbol)
			}
			tradeInput := common
			tradeInput.Kind = dataset.MarketPayloadOptionTrade
			tradeInput.EffectiveAt = result.snapshot.LastTradeObservedAt.UTC().Truncate(time.Microsecond)
			tradeInput.Trade = &dataset.TradePayload{Price: canonicalFloat(result.snapshot.Last), Size: canonicalFloat(result.snapshot.LastSize)}
			tradePayload, err := dataset.NewMarketPayload(tradeInput)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("canonicalize option trade %s: %w", result.snapshot.Contract.OCCSymbol, err)
			}
			payloads = append(payloads, tradePayload)
		}
		payloads = append(payloads, snapshotPayload)
	}
	for _, result := range tradeSets {
		if err := validateReceipt(result.symbol, result.receipt, request); err != nil {
			return dataset.MarketImportSourceResult{}, err
		}
		for _, trade := range result.trades {
			effectiveAt := trade.Timestamp.UTC().Truncate(time.Microsecond)
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
				Trade: &dataset.TradePayload{Price: canonicalFloat(trade.Price), Size: canonicalFloat(trade.Size), Exchange: trade.Exchange},
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
	if resolved.UnderlyingID == nil || *resolved.UnderlyingID != underlying.ID {
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

func canonicalFloat(value float64) string {
	if value == 0 {
		return "0"
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "invalid"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func contains(values []string, sought string) bool {
	for _, value := range values {
		if strings.EqualFold(value, sought) {
			return true
		}
	}
	return false
}

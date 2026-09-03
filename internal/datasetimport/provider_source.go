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
	ModeStockBars  Mode = "stock_bars"
	ModeOptionBars Mode = "option_bars"
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
}

func (source *ProviderSource) FetchMarketPayloads(ctx context.Context, request dataset.MarketImportRequest) (dataset.MarketImportSourceResult, error) {
	if source == nil || source.Instruments == nil || source.Clock == nil {
		return dataset.MarketImportSourceResult{}, fmt.Errorf("provider market import source is incomplete")
	}
	timeframe, err := parseTimeframe(request.Timeframe)
	if err != nil {
		return dataset.MarketImportSourceResult{}, err
	}
	resolveAt := request.DecisionCutoff
	if resolveAt.IsZero() {
		resolveAt = source.Clock().UTC().Truncate(time.Microsecond)
	}
	var fetched []fetchedBars
	switch source.Mode {
	case ModeStockBars:
		if source.Stock == nil {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("stock provider is not configured")
		}
		for _, symbol := range request.Universe {
			resolved, err := source.Instruments.ResolveAlias(ctx, request.Provider, instrument.AliasTicker, symbol, resolveAt)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("resolve stock %s: %w", symbol, err)
			}
			bars, err := source.Stock.GetOHLCV(ctx, symbol, timeframe, request.From, request.To)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch stock bars for %s: %w", symbol, err)
			}
			if len(bars) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no stock bars for %s", symbol)
			}
			fetched = append(fetched, fetchedBars{symbol: symbol, instrumentID: resolved.ID, bars: bars})
		}
	case ModeOptionBars:
		if source.Options == nil || len(source.OptionSymbols) == 0 {
			return dataset.MarketImportSourceResult{}, fmt.Errorf("options provider and explicit OCC symbols are required")
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
			bars, err := source.Options.GetOptionsOHLCV(ctx, symbol, timeframe, request.From, request.To)
			if err != nil {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("fetch option bars for %s: %w", symbol, err)
			}
			if len(bars) == 0 {
				return dataset.MarketImportSourceResult{}, fmt.Errorf("provider returned no option bars for %s", symbol)
			}
			fetched = append(fetched, fetchedBars{
				symbol: symbol, underlying: contract.Underlying, instrumentID: resolved.ID, underlyingID: underlying.ID, bars: bars,
			})
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
	return dataset.MarketImportSourceResult{
		Origin: dataset.MarketImportOriginProviderAPI, Entitled: true, PaginationComplete: true, Payloads: payloads,
	}, nil
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

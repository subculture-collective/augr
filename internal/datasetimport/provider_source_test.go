package datasetimport

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

type stockProviderStub struct {
	bars    []domain.OHLCV
	receipt *data.HistoricalFetchReceipt
}

func (stub stockProviderStub) GetOHLCV(context.Context, string, data.Timeframe, time.Time, time.Time) ([]domain.OHLCV, error) {
	return stub.bars, nil
}
func (stockProviderStub) GetFundamentals(context.Context, string) (data.Fundamentals, error) {
	return data.Fundamentals{}, nil
}
func (stockProviderStub) GetNews(context.Context, string, time.Time, time.Time) ([]data.NewsArticle, error) {
	return nil, nil
}
func (stockProviderStub) GetSocialSentiment(context.Context, string, time.Time, time.Time) ([]data.SocialSentiment, error) {
	return nil, nil
}
func (stub stockProviderStub) GetOHLCVWithReceipt(_ context.Context, _ string, _ data.Timeframe, _, _ time.Time, feed, adjustment string) ([]domain.OHLCV, data.HistoricalFetchReceipt, error) {
	receipt := data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: adjustment, Pages: 1, Entitled: true, PaginationComplete: true}
	if stub.receipt != nil {
		receipt = *stub.receipt
	}
	return stub.bars, receipt, nil
}

type optionsProviderStub struct {
	bars      []domain.OHLCV
	snapshots []domain.OptionSnapshot
	receipt   *data.HistoricalFetchReceipt
}

func (stub optionsProviderStub) GetOptionsChain(context.Context, string, time.Time, domain.OptionType) ([]domain.OptionSnapshot, error) {
	return stub.snapshots, nil
}
func (stub optionsProviderStub) GetOptionsChainWithReceipt(_ context.Context, _ string, _ time.Time, _ domain.OptionType, feed string) ([]domain.OptionSnapshot, data.HistoricalFetchReceipt, error) {
	receipt := data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: "raw", Pages: 1, Entitled: true, PaginationComplete: true}
	if stub.receipt != nil {
		receipt = *stub.receipt
	}
	return stub.snapshots, receipt, nil
}
func (stub optionsProviderStub) GetOptionsOHLCV(context.Context, string, data.Timeframe, time.Time, time.Time) ([]domain.OHLCV, error) {
	return stub.bars, nil
}
func (stub optionsProviderStub) GetOptionsOHLCVWithReceipt(_ context.Context, _ string, _ data.Timeframe, _, _ time.Time, feed, adjustment string) ([]domain.OHLCV, data.HistoricalFetchReceipt, error) {
	receipt := data.HistoricalFetchReceipt{Provider: "alpaca", Feed: feed, AdjustmentPolicy: adjustment, Pages: 1, Entitled: true, PaginationComplete: true}
	if stub.receipt != nil {
		receipt = *stub.receipt
	}
	return stub.bars, receipt, nil
}

type resolverStub struct {
	stockID  uuid.UUID
	optionID uuid.UUID
}

func (stub resolverStub) ResolveAlias(_ context.Context, _ string, kind instrument.AliasType, _ string, _ time.Time) (*instrument.Instrument, error) {
	if kind == instrument.AliasOCC {
		underlying := stub.stockID
		return &instrument.Instrument{ID: stub.optionID, UnderlyingID: &underlying}, nil
	}
	return &instrument.Instrument{ID: stub.stockID}, nil
}

func TestProviderSourceFetchesCanonicalStockAndOptionBars(t *testing.T) {
	barAt := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	observedAt := barAt.Add(time.Hour)
	bars := []domain.OHLCV{{Timestamp: barAt, Open: 10, High: 12, Low: 9, Close: 11, Volume: 100}}
	resolver := resolverStub{stockID: uuid.New(), optionID: uuid.New()}
	base := dataset.MarketImportRequest{
		Provider: "alpaca", Feed: "sip", Timeframe: "1d", AdjustmentPolicy: "raw",
		From: barAt, To: barAt, DecisionCutoff: observedAt, Universe: []string{"AAPL"},
	}
	for name, source := range map[string]*ProviderSource{
		"stock": {
			Mode: ModeStockBars, Stock: stockProviderStub{bars: bars}, Instruments: resolver, Clock: func() time.Time { return observedAt },
		},
		"option": {
			Mode: ModeOptionBars, Options: optionsProviderStub{bars: bars}, Instruments: resolver,
			OptionSymbols: []string{"AAPL260116C00150000"}, Clock: func() time.Time { return observedAt },
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := source.FetchMarketPayloads(context.Background(), base)
			if err != nil {
				t.Fatalf("FetchMarketPayloads() error = %v", err)
			}
			if result.Origin != dataset.MarketImportOriginProviderAPI || !result.Entitled || !result.PaginationComplete || len(result.Payloads) != 1 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestProviderSourceRejectsEmptyEntitlementResultAndLateResponse(t *testing.T) {
	barAt := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	request := dataset.MarketImportRequest{
		Provider: "alpaca", Feed: "sip", Timeframe: "1d", AdjustmentPolicy: "raw",
		From: barAt, To: barAt, DecisionCutoff: barAt.Add(time.Hour), Universe: []string{"AAPL"},
	}
	resolver := resolverStub{stockID: uuid.New()}
	source := &ProviderSource{Mode: ModeStockBars, Stock: stockProviderStub{}, Instruments: resolver, Clock: func() time.Time { return request.DecisionCutoff }}
	if _, err := source.FetchMarketPayloads(context.Background(), request); err == nil {
		t.Fatal("FetchMarketPayloads() accepted empty provider result")
	}
	source.Stock = stockProviderStub{bars: []domain.OHLCV{{Timestamp: barAt, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}}}
	source.Clock = func() time.Time { return request.DecisionCutoff.Add(time.Microsecond) }
	if _, err := source.FetchMarketPayloads(context.Background(), request); err == nil {
		t.Fatal("FetchMarketPayloads() accepted response after cutoff")
	}
}

func TestProviderSourceRejectsUnverifiedOrMismatchedReceipt(t *testing.T) {
	barAt := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	request := dataset.MarketImportRequest{
		Provider: "alpaca", Feed: "sip", Timeframe: "1d", AdjustmentPolicy: "raw",
		From: barAt, To: barAt, DecisionCutoff: barAt.Add(time.Hour), Universe: []string{"AAPL"},
	}
	resolver := resolverStub{stockID: uuid.New()}
	bars := []domain.OHLCV{{Timestamp: barAt, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}}
	for name, receipt := range map[string]data.HistoricalFetchReceipt{
		"not entitled":       {Provider: "alpaca", Feed: "sip", AdjustmentPolicy: "raw", Pages: 1, PaginationComplete: true},
		"incomplete pages":   {Provider: "alpaca", Feed: "sip", AdjustmentPolicy: "raw", Pages: 1, Entitled: true},
		"wrong feed":         {Provider: "alpaca", Feed: "opra", AdjustmentPolicy: "raw", Pages: 1, Entitled: true, PaginationComplete: true},
		"wrong adjustment":   {Provider: "alpaca", Feed: "sip", AdjustmentPolicy: "adjusted", Pages: 1, Entitled: true, PaginationComplete: true},
		"missing page count": {Provider: "alpaca", Feed: "sip", AdjustmentPolicy: "raw", Entitled: true, PaginationComplete: true},
	} {
		t.Run(name, func(t *testing.T) {
			source := &ProviderSource{Mode: ModeStockBars, Stock: stockProviderStub{bars: bars, receipt: &receipt}, Instruments: resolver, Clock: func() time.Time { return request.DecisionCutoff }}
			if _, err := source.FetchMarketPayloads(context.Background(), request); err == nil {
				t.Fatal("FetchMarketPayloads() accepted invalid provider receipt")
			}
		})
	}
}

func TestProviderSourceCapturesPointInTimeOptionContractAndSnapshot(t *testing.T) {
	observedAt := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	resolver := resolverStub{stockID: uuid.New(), optionID: uuid.New()}
	contract, err := domain.ParseOCC("AAPL260116C00150000")
	if err != nil {
		t.Fatal(err)
	}
	source := &ProviderSource{
		Mode: ModeOptionChainSnapshot,
		Options: optionsProviderStub{snapshots: []domain.OptionSnapshot{{
			Contract: *contract, ObservedAt: observedAt, QuoteObservedAt: observedAt, LastTradeObservedAt: observedAt, Bid: 5, BidSize: 10, Ask: 5.2, AskSize: 8,
			Last: 5.1, LastSize: 2, Greeks: domain.OptionGreeks{IV: .3, Delta: .5, Gamma: .02, Theta: -.04, Vega: .1},
		}}},
		Instruments: resolver, Clock: func() time.Time { return observedAt },
	}
	result, err := source.FetchMarketPayloads(context.Background(), dataset.MarketImportRequest{
		Provider: "alpaca", Feed: "opra", Timeframe: "snapshot", AdjustmentPolicy: "raw",
		From: observedAt, To: observedAt, DecisionCutoff: observedAt, Universe: []string{"AAPL"},
	})
	if err != nil {
		t.Fatalf("FetchMarketPayloads() error = %v", err)
	}
	if len(result.Payloads) != 4 || result.Payloads[0].Kind() != dataset.MarketPayloadOptionContract ||
		result.Payloads[1].Kind() != dataset.MarketPayloadOptionQuote || result.Payloads[2].Kind() != dataset.MarketPayloadOptionTrade ||
		result.Payloads[3].Kind() != dataset.MarketPayloadOptionSnapshot {
		t.Fatalf("payloads = %#v, want contract, quote, trade, and snapshot", result.Payloads)
	}
}

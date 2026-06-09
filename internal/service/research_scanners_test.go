package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/optionsresearch"
	"github.com/PatrickFanella/get-rich-quick/internal/polymarketresearch"
)

type fakeOptionsProvider struct {
	chain []domain.OptionSnapshot
	err   error
}

func (f fakeOptionsProvider) GetOptionsChain(context.Context, string, time.Time, domain.OptionType) ([]domain.OptionSnapshot, error) {
	return append([]domain.OptionSnapshot(nil), f.chain...), f.err
}

func (f fakeOptionsProvider) GetOptionsOHLCV(context.Context, string, data.Timeframe, time.Time, time.Time) ([]domain.OHLCV, error) {
	return nil, errors.New("not implemented")
}

type fakePolymarketFetcher struct {
	market *agent.PredictionMarketData
	err    error
}

func (f fakePolymarketFetcher) GetMarketData(context.Context, string) (*agent.PredictionMarketData, error) {
	return f.market, f.err
}

func TestResearchScannerOptionsReturnsAcceptedOpportunities(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	expiry := now.AddDate(0, 0, 60)
	chain := []domain.OptionSnapshot{
		{Contract: domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 90, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 9.80, Ask: 10.20, Mid: 10.00, Volume: 500, OpenInterest: 2000},
		{Contract: domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypeCall, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 4.00, Ask: 4.50, Mid: 4.25, Volume: 450, OpenInterest: 1800},
		{Contract: domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypePut, 90, expiry), Underlying: "AAPL", OptionType: domain.OptionTypePut, Strike: 90, Expiry: expiry, Multiplier: 100, Style: "european"}, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 1.90, Ask: 2.20, Mid: 2.05, Volume: 500, OpenInterest: 2000},
		{Contract: domain.OptionContract{OCCSymbol: domain.FormatOCC("AAPL", domain.OptionTypePut, 100, expiry), Underlying: "AAPL", OptionType: domain.OptionTypePut, Strike: 100, Expiry: expiry, Multiplier: 100, Style: "european"}, Greeks: domain.OptionGreeks{IV: 0.25}, Bid: 3.90, Ask: 4.10, Mid: 4.00, Volume: 450, OpenInterest: 1800},
	}
	svc := &ResearchScanner{optionsProvider: fakeOptionsProvider{chain: chain}, optionsScanner: optionsresearch.NewScanner(optionsresearch.Config{MinOpenInterest: 100, MinVolume: 50, MaxBidAskSpread: 1, MinNetEdge: 0.01, MaxThetaExposure: 50_000, MaxUnderlyingExposure: 50_000, Bankroll: 10_000, FractionalKellyCap: 0.20, RiskFreeRate: 0.02, DividendYield: 0, PeriodsPerYear: 252}), polymarketConfig: polymarketresearch.DefaultScanConfig(), nowFunc: func() time.Time { return now }}

	got, err := svc.ScanOptions(context.Background(), OptionsOpportunityRequest{Underlying: "AAPL", StrategyID: uuidPtr("11111111-1111-1111-1111-111111111111"), Limit: 10})
	if err != nil {
		t.Fatalf("ScanOptions() error = %v", err)
	}
	if len(got) == 0 {
		t.Fatal("ScanOptions() returned no accepted opportunities")
	}
	if got[0].Decision.StrategyID == nil || got[0].Decision.StrategyID.String() != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("StrategyID = %v, want injected strategy", got[0].Decision.StrategyID)
	}
	if math.IsNaN(got[0].Decision.ExecutablePrice) || math.IsInf(got[0].Decision.ExecutablePrice, 0) {
		t.Fatalf("decision contains non-finite executable price: %+v", got[0].Decision)
	}
}

func TestResearchScannerOptionsReturnsEmptyWithoutProvider(t *testing.T) {
	svc := &ResearchScanner{optionsScanner: optionsresearch.NewScanner(optionsresearch.Config{MinOpenInterest: 100, MinVolume: 50, MaxBidAskSpread: 1, MinNetEdge: 0.01, MaxThetaExposure: 50_000, MaxUnderlyingExposure: 50_000, Bankroll: 10_000, FractionalKellyCap: 0.20, RiskFreeRate: 0.02, DividendYield: 0, PeriodsPerYear: 252}), polymarketConfig: polymarketresearch.DefaultScanConfig(), nowFunc: time.Now}
	got, err := svc.ScanOptions(context.Background(), OptionsOpportunityRequest{Underlying: "AAPL"})
	if err != nil {
		t.Fatalf("ScanOptions() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ScanOptions() len = %d, want 0", len(got))
	}
}

func TestResearchScannerOptionsReturnsProviderError(t *testing.T) {
	svc := &ResearchScanner{optionsProvider: fakeOptionsProvider{err: data.ErrNoProviders}, optionsScanner: optionsresearch.NewScanner(optionsresearch.Config{MinOpenInterest: 100, MinVolume: 50, MaxBidAskSpread: 1, MinNetEdge: 0.01, MaxThetaExposure: 50_000, MaxUnderlyingExposure: 50_000, Bankroll: 10_000, FractionalKellyCap: 0.20, RiskFreeRate: 0.02, DividendYield: 0, PeriodsPerYear: 252}), polymarketConfig: polymarketresearch.DefaultScanConfig(), nowFunc: time.Now}
	_, err := svc.ScanOptions(context.Background(), OptionsOpportunityRequest{Underlying: "AAPL"})
	if !errors.Is(err, data.ErrNoProviders) {
		t.Fatalf("ScanOptions() error = %v, want ErrNoProviders", err)
	}
}

func TestResearchScannerPolymarketQuerySnapshotAcceptedAndRejected(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	svc := &ResearchScanner{polymarketConfig: polymarketresearch.DefaultScanConfig(), nowFunc: func() time.Time { return now }}

	accepted, err := svc.ScanPolymarket(context.Background(), PolymarketOpportunityRequest{Slug: "will-it-happen", TokenID: "token-123", Outcome: "YES", Probability: floatPtr(0.65), BestBid: floatPtr(0.50), BestAsk: floatPtr(0.55), AskDepthUSD: floatPtr(500)})
	if err != nil {
		t.Fatalf("accepted ScanPolymarket() error = %v", err)
	}
	if len(accepted) != 1 {
		t.Fatalf("accepted len = %d, want 1", len(accepted))
	}

	rejected, err := svc.ScanPolymarket(context.Background(), PolymarketOpportunityRequest{Slug: "will-it-happen", TokenID: "token-123", Outcome: "YES", Probability: floatPtr(0), BestBid: floatPtr(0.50), BestAsk: floatPtr(0.55), AskDepthUSD: floatPtr(500)})
	if err != nil {
		t.Fatalf("rejected ScanPolymarket() error = %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected len = %d, want 0", len(rejected))
	}
}

func TestResearchScannerPolymarketProviderOutcomeNoReturnsEmpty(t *testing.T) {
	now := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	svc := &ResearchScanner{
		polymarketFetcher: fakePolymarketFetcher{market: &agent.PredictionMarketData{Slug: "will-it-happen", YesTokenID: "token-yes", YesPrice: 0.65, BestBidYes: 0.50, BestAskYes: 0.55, Liquidity: 500}},
		polymarketConfig:  polymarketresearch.DefaultScanConfig(),
		nowFunc:           func() time.Time { return now },
	}

	got, err := svc.ScanPolymarket(context.Background(), PolymarketOpportunityRequest{Slug: "will-it-happen", Outcome: "NO"})
	if err != nil {
		t.Fatalf("ScanPolymarket() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ScanPolymarket() len = %d, want 0", len(got))
	}
}

func floatPtr(v float64) *float64 { return &v }

func uuidPtr(raw string) *uuid.UUID {
	id := uuid.MustParse(raw)
	return &id
}

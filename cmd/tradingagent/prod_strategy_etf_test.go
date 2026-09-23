package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/agent"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/data/ssga"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func runtimeETFFixture(now time.Time) *data.ETFFundamentals {
	fee := 0.000945
	source := data.ETFSource{SHA256: strings.Repeat("a", 64), AsOf: now.Add(-time.Hour), FetchedAt: now}
	f := &data.ETFFundamentals{Contract: data.SPYETFContractV1, Ticker: "SPY", ISIN: "US78462F1030", Currency: "USD", NetAssetsUSD: 1e9, GrossExpenseRatio: &fee, Holdings: []data.ETFHolding{{Symbol: "AAA", Weight: 1}}, ProfileSource: source, HoldingsSource: source}
	f.ProfileSource.URL = ssga.ProfileURL
	f.HoldingsSource.URL = ssga.HoldingsURL
	return f
}

type etfStub struct {
	fund  *data.ETFFundamentals
	err   error
	calls int
}

func (s *etfStub) GetETFFundamentals(context.Context, string) (data.Fundamentals, error) {
	s.calls++
	return data.Fundamentals{Ticker: "SPY", FetchedAt: time.Now().UTC(), ETF: s.fund}, s.err
}

func TestETFPreparationIsOptInAndPreservesOtherRequiredGates(t *testing.T) {
	now := time.Now().UTC()
	fund := runtimeETFFixture(now)
	seed := agent.InitialStateSeed{Fundamentals: &data.Fundamentals{Ticker: "SPY", FetchedAt: now, ETF: fund}, Market: &agent.MarketData{Bars: []domain.OHLCV{{Timestamp: now, Close: 100}}}, News: []data.NewsArticle{{Relevance: 1, PublishedAt: now}, {Relevance: 1, PublishedAt: now}, {Relevance: 1, PublishedAt: now}}}
	strategy := domain.Strategy{Ticker: "SPY", MarketType: domain.MarketTypeStock, IsPaper: true}
	roles := []agent.AgentRole{agent.AgentRoleMarketAnalyst, agent.AgentRoleFundamentalsAnalyst, agent.AgentRoleNewsAnalyst}
	if err := validateRequiredAnalysisInputs(strategy, roles, seed, now); err == nil {
		t.Fatal("ETF evidence bypassed corporate default")
	}
	if err := validateRequiredAnalysisInputsWithContract(strategy, roles, seed, now, data.SPYETFContractV1); err != nil {
		t.Fatal(err)
	}
	seed.News = nil
	if err := validateRequiredAnalysisInputsWithContract(strategy, roles, seed, now, data.SPYETFContractV1); err == nil {
		t.Fatal("ETF bypassed required news")
	}
	seed.Fundamentals.ETF.HoldingsSource.AsOf = now.Add(-8 * 24 * time.Hour)
	err := validateRequiredAnalysisInputsWithContract(strategy, roles, seed, now, data.SPYETFContractV1)
	if err == nil || strategyPreparationFailureReason(err) != "etf_fundamentals_invalid" {
		t.Fatalf("stale ETF error=%v", err)
	}
}

func TestLoadETFInputsRejectsLiveWrongTickerAndUnavailableSource(t *testing.T) {
	now := time.Now().UTC()
	source := &etfStub{fund: runtimeETFFixture(now), err: errors.New("issuer unavailable")}
	runner := &realStrategyRunner{dataService: &stubMarketDataService{ohlcv: []domain.OHLCV{{Timestamp: now, Close: 100}}}, etfSource: source, logger: slog.Default()}
	cfg := agent.ResolveConfig(&agent.StrategyConfig{FundamentalsContract: data.SPYETFContractV1, RequiredAnalystRoles: []agent.AgentRole{agent.AgentRoleMarketAnalyst, agent.AgentRoleFundamentalsAnalyst, agent.AgentRoleNewsAnalyst}}, agent.GlobalSettings{})
	for _, strategy := range []domain.Strategy{{Ticker: "SPY", MarketType: domain.MarketTypeStock, IsPaper: false}, {Ticker: "AAPL", MarketType: domain.MarketTypeStock, IsPaper: true}} {
		if _, err := runner.loadInitialState(context.Background(), strategy, cfg); err == nil {
			t.Fatal("ineligible ETF strategy accepted")
		}
	}
	if source.calls != 0 {
		t.Fatal("ineligible strategies fetched ETF data")
	}
	if _, err := runner.loadInitialState(context.Background(), domain.Strategy{Ticker: "SPY", MarketType: domain.MarketTypeStock, IsPaper: true}, cfg); err == nil || !strings.Contains(err.Error(), "issuer unavailable") {
		t.Fatalf("provider failure lost: %v", err)
	}
	if source.calls != 1 {
		t.Fatal("unexpected fetch count")
	}
}

func TestETFRuntimeContractCannotBeIgnoredByAlternateDispatch(t *testing.T) {
	raw := json.RawMessage(`{"fundamentals_contract":"spy-ssga-etf-v1","required_analyst_roles":["market_analyst","fundamentals_analyst","news_analyst"]}`)
	valid := domain.Strategy{Ticker: "SPY", MarketType: domain.MarketTypeStock, IsPaper: true, Config: raw}
	if err := validateETFStrategyContract(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.Strategy){
		func(s *domain.Strategy) { s.IsPaper = false },
		func(s *domain.Strategy) { s.MarketType = domain.MarketTypeKalshi },
		func(s *domain.Strategy) { s.Ticker = "QQQ" },
		func(s *domain.Strategy) {
			s.Config = json.RawMessage(`{"fundamentals_contract":"spy-ssga-etf-v1","required_analyst_roles":["market_analyst","fundamentals_analyst","news_analyst"],"generated_strategy":{}}`)
		},
		func(s *domain.Strategy) {
			s.Config = json.RawMessage(`{"fundamentals_contract":"spy-ssga-etf-v1","required_analyst_roles":[]}`)
		},
		func(s *domain.Strategy) { s.Config = json.RawMessage(`{"fundamentals_contract":"unknown"}`) },
	} {
		strategy := valid
		change(&strategy)
		if err := validateETFStrategyContract(strategy); err == nil {
			t.Fatal("ineligible contract ignored")
		}
	}
	if err := validateETFStrategyContract(domain.Strategy{Ticker: "QQQ", MarketType: domain.MarketTypeStock, Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal("corporate default changed", err)
	}
}

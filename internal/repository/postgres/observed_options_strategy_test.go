package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/optionsstrategy"
)

func TestStrategyRepoResolvesNativeObservedOptionsVersion(t *testing.T) {
	fixture := newStrategyCatalogFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `ALTER TABLE strategies ADD COLUMN execution_strategy_version_id UUID REFERENCES strategy_versions(id) ON DELETE RESTRICT`); err != nil {
		t.Fatal(err)
	}
	config := observedOptionsConfig()
	family, version, err := optionsstrategy.Compile(config, strings.Repeat("a", 40), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.RegisterStrategyFamily(fixture.ctx, family); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.RegisterStrategyVersion(fixture.ctx, version); err != nil {
		t.Fatal(err)
	}
	runtimeConfig, err := optionsstrategy.RuntimeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	strategyID := uuid.New()
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO strategies(id,name,description,ticker,market_type,schedule_cron,config,status,skip_next_run,is_paper,is_active,execution_strategy_version_id)
		VALUES($1,'native options','test','AAPL','options','',$2::jsonb,'inactive',false,true,false,$3)`, strategyID, string(runtimeConfig), version.ID()); err != nil {
		t.Fatal(err)
	}
	resolved, err := NewStrategyRepo(fixture.pool).ResolveExecutionVersionID(fixture.ctx, strategyID)
	if err != nil || resolved != version.ID() {
		t.Fatalf("resolved = %s, %v", resolved, err)
	}
}

func TestObservedOptionsStrategyRepoRejectsMissingScope(t *testing.T) {
	if _, _, err := NewObservedOptionsStrategyRepo(nil).RegisterCandidate(t.Context(), uuid.Nil, uuid.Nil, observedOptionsConfig(), time.Time{}, time.Time{}, strings.Repeat("a", 40), strings.Repeat("b", 64)); err == nil {
		t.Fatal("missing database and scope accepted")
	}
}

func observedOptionsConfig() rules.OptionsRulesConfig {
	zero := 0.0
	return rules.OptionsRulesConfig{
		Version: 1, StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL",
		Entry: rules.ConditionGroup{Operator: "AND", Conditions: []rules.Condition{{Field: "close", Op: "gt", Value: &zero}}},
		Exit:  rules.ConditionGroup{Operator: "AND", Conditions: []rules.Condition{{Field: "pnl_pct", Op: "gt", Value: &zero}}},
		LegSelection: map[string]rules.LegSelector{
			"long":  {OptionType: domain.OptionTypeCall, DeltaTarget: 0.6, DTEMin: 20, DTEMax: 60, Side: domain.OrderSideBuy, Intent: domain.PositionIntentBuyToOpen, Ratio: 1},
			"short": {OptionType: domain.OptionTypeCall, DeltaTarget: 0.3, DTEMin: 20, DTEMax: 60, Side: domain.OrderSideSell, Intent: domain.PositionIntentSellToOpen, Ratio: 1},
		},
		PositionSizing: rules.OptionsSizingConfig{Method: "max_risk", MaxRiskUSD: 500},
	}
}

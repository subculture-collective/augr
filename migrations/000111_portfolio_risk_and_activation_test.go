package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestPortfolioRiskAndActivationMigrationContainsFailClosedActivationGraph(t *testing.T) {
	raw, err := os.ReadFile("000111_portfolio_risk_and_activation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(raw))
	for _, required := range []string{
		"create table portfolio_risk_policy_artifacts",
		"create table account_portfolio_risk_policy_bindings",
		"create table portfolio_account_snapshots",
		"create table portfolio_opportunity_option_legs",
		"create table allocation_risk_caps",
		"create table strategy_promotion_activations",
		"decision_id uuid not null unique",
		"source_version_id uuid not null",
		"runtime_version_id uuid not null",
		"scope_id uuid not null",
		"capital_binding_id uuid not null",
		"validate_strategy_promotion_activation",
		"not exists(select 1 from promotion_retirement_decisions child",
		"reject_promotion_mutation",
		"create unique index orders_stock_allocation_effect_once",
		"create unique index orders_option_allocation_leg_once",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration 111 missing %q", required)
		}
	}
}

func TestPortfolioRiskAndActivationRollbackRefusesEveryEvidenceClass(t *testing.T) {
	raw, err := os.ReadFile("000111_portfolio_risk_and_activation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(raw))
	for _, required := range []string{
		"exists(select 1 from strategy_promotion_activations)",
		"exists(select 1 from portfolio_risk_policy_artifacts)",
		"exists(select 1 from account_portfolio_risk_policy_bindings)",
		"exists(select 1 from portfolio_account_snapshots)",
		"exists(select 1 from portfolio_opportunity_option_legs)",
		"exists(select 1 from allocation_risk_caps)",
		"portfolio_opportunities where execution_version_id is not null",
		"allocation_decisions where risk_policy_id is not null",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration 111 rollback missing refusal for %q", required)
		}
	}
}

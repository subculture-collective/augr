package migrations_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/portfolio"
	"github.com/jackc/pgx/v5/pgconn"
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
		"create table generated_strategy_scenarios",
		"create table generated_strategy_scenario_frames",
		"create table generated_strategy_scenario_bindings",
		"validate_generated_strategy_scenario_graph",
		"dataset_manifest_observations",
		"payload.content_sha256=binding.payload_sha256",
		"coalesce(payload.published_at,payload.available_at)",
		"coalesce(observation.published_at,observation.available_at)=binding.available_at",
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
		"exists(select 1 from generated_strategy_scenarios)",
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

func TestPortfolioRiskMigrationRejectsForgeryMutationAndRollback(t *testing.T) {
	ctx, pool := newCanonicalExpansionPool(t)
	for _, name := range []string{
		"000108_canonical_account_expansion.up.sql", "000109_enforce_canonical_account.up.sql",
		"000110_immutable_market_payloads.up.sql", "000111_portfolio_risk_and_activation.up.sql",
		"000111_portfolio_risk_and_activation.down.sql", "000111_portfolio_risk_and_activation.up.sql",
	} {
		if _, err := pool.Exec(ctx, readMigrationFile(t, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	policy, err := portfolio.ReviewedPortfolioRiskPolicyV1()
	if err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO portfolio_risk_policy_artifacts(id,schema_name,version,sha256,canonical_bytes,canonical_json,created_at)
        VALUES($1,$2,$3,$4,$5,$6::jsonb,$7)`
	created := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	_, err = pool.Exec(ctx, insert, policy.ID(), policy.Schema, policy.Version, strings.Repeat("f", 64), policy.CanonicalBytes(), string(policy.CanonicalBytes()), created)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23514" {
		t.Fatalf("forged digest must violate a check constraint: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, policy.ID(), policy.Schema, policy.Version, policy.Digest(), policy.CanonicalBytes(), string(policy.CanonicalBytes()), created); err != nil {
		t.Fatalf("valid policy: %v", err)
	}
	for _, statement := range []string{
		`UPDATE portfolio_risk_policy_artifacts SET version=version WHERE id=$1`,
		`DELETE FROM portfolio_risk_policy_artifacts WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, policy.ID()); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("policy mutation: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, readMigrationFile(t, "000111_portfolio_risk_and_activation.down.sql")); err == nil || !strings.Contains(err.Error(), "cannot roll back migration 111") {
		t.Fatalf("nonempty rollback: %v", err)
	}
	var digest string
	if err := pool.QueryRow(ctx, `SELECT sha256 FROM portfolio_risk_policy_artifacts WHERE id=$1`, policy.ID()).Scan(&digest); err != nil || digest != policy.Digest() {
		t.Fatalf("rollback did not preserve exact policy: %s/%v", digest, err)
	}
}

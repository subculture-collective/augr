package migrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/capital"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

func TestPaperEvaluationScopesMigrationEnforcesScopedEvidenceEndToEnd(t *testing.T) {
	ctx, pool := newDatasetMigrationPool(t)
	for _, filename := range sortedUpMigrationsThrough(t, "000105_prediction_native_snapshot_types.up.sql") {
		if filename <= "000076_dataset_manifests_quality.up.sql" {
			continue
		}
		if _, err := pool.Exec(ctx, readMigrationFile(t, filename)); err != nil {
			t.Fatalf("apply %s: %v", filename, err)
		}
	}

	strategyID := insertScopeMigrationStrategy(t, ctx, pool)
	var legacyConfigID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO backtest_configs(strategy_id,name,start_date,end_date,simulation_params)
		VALUES($1,'legacy','2026-01-01','2026-02-01','{"initial_capital":500}'::JSONB) RETURNING id`, strategyID).Scan(&legacyConfigID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO report_artifacts(strategy_id,report_type,time_bucket,status)
		VALUES($1,'paper_validation','2026-08-23','pending')`, strategyID); err != nil {
		t.Fatal(err)
	}
	var legacyRunID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO backtest_runs(backtest_config_id,metrics,trade_log,equity_curve,run_timestamp,duration_ns,prompt_version,prompt_version_hash)
		VALUES($1,'{}','[]','[]',now(),0,'rules-v1','hash') RETURNING id`, legacyConfigID).Scan(&legacyRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, readMigrationFile(t, "000106_paper_evaluation_scopes.up.sql")); err != nil {
		t.Fatalf("apply migration 106: %v", err)
	}
	var legacyNulls int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM backtest_configs WHERE id=$1 AND scope_id IS NULL)+
		(SELECT count(*) FROM report_artifacts WHERE strategy_id=$2 AND scope_id IS NULL)`, legacyConfigID, strategyID).Scan(&legacyNulls); err != nil || legacyNulls != 2 {
		t.Fatalf("legacy rows changed: count=%d err=%v", legacyNulls, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO backtest_configs(strategy_id,name,start_date,end_date,simulation_params)
		VALUES($1,'new-null','2026-01-01','2026-02-01','{}')`, strategyID); err == nil || !strings.Contains(err.Error(), "requires scope_id") {
		t.Fatalf("new null config error=%v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO backtest_runs(backtest_config_id,metrics,trade_log,equity_curve,run_timestamp,duration_ns,prompt_version,prompt_version_hash)
		VALUES($1,'{}','[]','[]',now(),0,'rules-v1','hash')`, legacyConfigID); err == nil || !strings.Contains(err.Error(), "requires scope_id") {
		t.Fatalf("new null run error=%v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO report_artifacts(strategy_id,report_type,time_bucket,status)
		VALUES($1,'other','2026-08-23','pending')`, strategyID); err == nil || !strings.Contains(err.Error(), "requires scope_id") {
		t.Fatalf("new null report error=%v", err)
	}

	account, binding, policy := seedScopeCapitalBinding(t, ctx, pool)
	scope, err := pgrepo.NewPaperEvaluationScope(pgrepo.PaperEvaluationScope{
		AccountID: account.ID, CapitalBindingID: binding.ID,
		ManifestSHA256: strings.Repeat("1", 64), QualitySHA256: strings.Repeat("2", 64),
		SimulationPolicySHA256: strings.Repeat("3", 64), CapitalPolicySHA256: policy.Digest(),
		EvaluationStart: time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC),
		EvaluationEnd:   time.Date(2026, 2, 1, 0, 0, 0, 123456000, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Dataset/policy fixture registration is covered by migrations 72/74/76. Disable only
	// the cross-artifact lookup here so this test can focus on migration 106's graph.
	if _, err := pool.Exec(ctx, `ALTER TABLE paper_evaluation_scopes DISABLE TRIGGER trg_paper_evaluation_scopes_validate`); err != nil {
		t.Fatal(err)
	}
	reportRepo := pgrepo.NewReportArtifactRepo(pool)
	if err := reportRepo.RegisterScope(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE paper_evaluation_scopes ENABLE TRIGGER trg_paper_evaluation_scopes_validate`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE backtest_configs SET scope_id=$1 WHERE id=$2`, scope.ID, legacyConfigID); err == nil || !strings.Contains(err.Error(), "cannot be relabeled") {
		t.Fatalf("legacy relabel error=%v", err)
	}

	config := &domain.BacktestConfig{ScopeID: &scope.ID, StrategyID: strategyID, Name: "scoped", StartDate: scope.EvaluationStart,
		EndDate: scope.EvaluationEnd, Simulation: domain.BacktestSimulationParameters{InitialCapital: 500}}
	// The fixture intentionally bypasses cross-artifact scope registration above;
	// disable config fact validation only while inserting that synthetic identity.
	if _, err := pool.Exec(ctx, `ALTER TABLE backtest_configs DISABLE TRIGGER trg_backtest_configs_validate_scope`); err != nil {
		t.Fatal(err)
	}
	if err := pgrepo.NewBacktestConfigRepo(pool).Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO backtest_configs(strategy_id,scope_id,name,start_date,end_date,simulation_params)
		VALUES($1,$2,'duplicate',$3,$4,'{"initial_capital":500}'::JSONB)`, strategyID, scope.ID, scope.EvaluationStart, scope.EvaluationEnd); err == nil || !strings.Contains(err.Error(), "uq_backtest_configs_strategy_scope") {
		t.Fatalf("duplicate strategy/scope config error=%v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE backtest_configs ENABLE TRIGGER trg_backtest_configs_validate_scope`); err != nil {
		t.Fatal(err)
	}
	run := &domain.BacktestRun{BacktestConfigID: config.ID, Metrics: json.RawMessage(`{"total_return":0.1}`), TradeLog: json.RawMessage(`[]`),
		EquityCurve: json.RawMessage(`[]`), RunTimestamp: scope.EvaluationEnd, PromptVersion: "rules-v1", PromptVersionHash: "hash"}
	if err := pgrepo.NewBacktestRunRepo(pool).Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	if run.ScopeID == nil || *run.ScopeID != scope.ID {
		t.Fatalf("run scope=%v want=%s", run.ScopeID, scope.ID)
	}
	if _, err := pool.Exec(ctx, `UPDATE backtest_runs SET scope_id=$1 WHERE id=$2`, uuid.New(), run.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("run scope mutation error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE backtest_runs SET metrics='{"forged":true}' WHERE id=$1`, run.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("run evidence mutation error=%v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM backtest_runs WHERE id=$1`, run.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("run deletion error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE backtest_runs SET metrics='{"legacy":true}' WHERE id=$1`, legacyRunID); err != nil {
		t.Fatalf("legacy run update changed behavior: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM backtest_runs WHERE id=$1`, legacyRunID); err != nil {
		t.Fatalf("legacy run delete changed behavior: %v", err)
	}
	reportBytes := []byte(`{"decision":"GO"}`)
	reportHash := sha256.Sum256(reportBytes)
	completed := scope.EvaluationEnd
	artifact := &pgrepo.ReportArtifact{StrategyID: strategyID, ScopeID: &scope.ID, BacktestRunID: &run.ID,
		ReportType: "paper_validation", TimeBucket: scope.EvaluationEnd.Truncate(24 * time.Hour), Status: "completed",
		ReportJSON: reportBytes, ReportBytes: reportBytes, ReportSHA256: hex.EncodeToString(reportHash[:]), CompletedAt: &completed}
	if err := reportRepo.Upsert(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	rows, err := reportRepo.List(ctx, pgrepo.ReportArtifactFilter{StrategyID: &strategyID, ScopeID: &scope.ID, AccountID: &account.ID}, 10, 0)
	if err != nil || len(rows) != 1 || rows[0].ReportSHA256 != artifact.ReportSHA256 || string(rows[0].ReportBytes) != string(reportBytes) {
		t.Fatalf("scoped report rows=%+v err=%v", rows, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE backtest_configs SET strategy_id=$1 WHERE id=$2`, insertScopeMigrationStrategy(t, ctx, pool), config.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("config identity mutation error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE report_artifacts SET report_bytes=report_bytes||decode('20','hex') WHERE id=$1`, artifact.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("report mutation error=%v", err)
	}
	if _, err := pool.Exec(ctx, readMigrationFile(t, "000106_paper_evaluation_scopes.down.sql")); err == nil || !strings.Contains(err.Error(), "while scoped paper evidence exists") {
		t.Fatalf("nonempty rollback error=%v", err)
	}
}

func insertScopeMigrationStrategy(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO strategies(name,ticker,market_type,is_paper,status) VALUES($1,'SPY','stock',true,'active') RETURNING id`, "scope-"+uuid.NewString()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedScopeCapitalBinding(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (*domain.Account, *capital.Binding, *capital.Policy) {
	t.Helper()
	account, err := domain.NewAccount(domain.AccountInput{Name: "scope account", Environment: domain.AccountEnvironmentPaperScored,
		Venue: "internal", BaseCurrency: "USD", StorageNamespace: "paper_scored/" + uuid.NewString(), StartingCapital: decimal.NewFromInt(500),
		BuyingPowerMultiplier: decimal.NewFromInt(2), MarginProfile: domain.MarginProfileRegT, CreatedBy: "migration-106", CreationMetadata: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := pgrepo.NewAccountRepo(pool).Create(ctx, account); err != nil {
		t.Fatal(err)
	}
	policy, err := capital.NewPolicy(capital.ReviewedPolicyV1Input())
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := policy.NewArtifact(time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	capitalRepo := pgrepo.NewCapitalPolicyRepo(pool)
	if _, err := capitalRepo.RegisterCapitalPolicy(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	binding, err := capital.NewBinding(*account, policy, account.StartingCapital, account.MarginProfile, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capitalRepo.BindCapitalPolicy(ctx, binding); err != nil {
		t.Fatal(err)
	}
	return account, binding, policy
}

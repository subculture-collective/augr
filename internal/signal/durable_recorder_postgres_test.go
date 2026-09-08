package signal_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/signal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the real recorder/repository against migration 109's exact trigger,
// in an explicitly disposable database with a minimal agent-event schema.
func TestSignalRecorderCanonicalPostgresGuard(t *testing.T) {
	dsn := os.Getenv("AUGR_SIGNAL_SCOPE_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set AUGR_SIGNAL_SCOPE_TEST_DB_URL to a disposable augr_signal_scope_test database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("requires an explicitly disposable database ending in _test")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "signal_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ddl := `CREATE TABLE accounts(id UUID PRIMARY KEY, environment TEXT, status TEXT);
CREATE TABLE agent_events(id UUID DEFAULT gen_random_uuid(), account_id UUID, environment TEXT,
origin_type TEXT CHECK (origin_type IN ('strategy_version','copy_subscription','portfolio_rebalance','risk_reduction','operator','settlement','reconciliation')),
origin_id TEXT, pipeline_run_id UUID, pipeline_run_trade_date DATE, strategy_id UUID, agent_role TEXT,
event_kind TEXT, title TEXT, summary TEXT, tags TEXT[], metadata JSONB, created_at TIMESTAMPTZ DEFAULT now());`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/000109_enforce_canonical_account.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	start, end := strings.Index(text, "CREATE FUNCTION canonical_parent_owned"), strings.Index(text, "DO $triggers$")
	if start < 0 || end <= start {
		t.Fatal("migration guard boundaries missing")
	}
	if _, err := pool.Exec(ctx, text[start:end]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER canonical_guard BEFORE INSERT OR UPDATE ON agent_events FOR EACH ROW EXECUTE FUNCTION enforce_canonical_account_row()`); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts VALUES ($1,'paper_scored','active')`, id); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewAgentEventRepo(pool, id)
	// The original unscoped event must reproduce the actual production failure.
	if err := repo.Create(ctx, &domain.AgentEvent{EventKind: "signal.evaluated", Title: "fixture"}); err == nil || !strings.Contains(err.Error(), "environment does not match account") {
		t.Fatalf("unscoped write = %v, want production environment guard", err)
	}
	binding, err := domain.NewExecutionAccountBinding(id, domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	recorder := signal.NewAgentEventRecorder(repo, binding)
	evaluated := signal.EvaluatedSignal{Raw: signal.RawSignalEvent{Source: "reddit", Title: "fixture", ReceivedAt: time.Now().UTC()}}
	trigger := signal.TriggerEvent{Signal: evaluated, StrategyID: uuid.New()}
	if err := recorder.RecordEvaluated(ctx, evaluated); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordTriggerRequest(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordTriggerOutcome(ctx, trigger, domain.StrategyTriggerAdmitted); err != nil {
		t.Fatal(err)
	}
	var total, scoped, origins int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE account_id=$1 AND environment='paper_scored' AND origin_type='operator' AND origin_id LIKE 'signal:%'),count(DISTINCT origin_id) FROM agent_events`, id).Scan(&total, &scoped, &origins); err != nil {
		t.Fatal(err)
	}
	if total != 3 || scoped != 3 || origins != 1 {
		t.Fatalf("rows=%d scoped=%d origins=%d", total, scoped, origins)
	}
	wrong, err := domain.NewExecutionAccountBinding(id, domain.AccountEnvironmentLive)
	if err != nil {
		t.Fatal(err)
	}
	if err := signal.NewAgentEventRecorder(repo, wrong).RecordEvaluated(ctx, evaluated); err == nil {
		t.Fatal("mismatched environment bypassed database guard")
	}
}

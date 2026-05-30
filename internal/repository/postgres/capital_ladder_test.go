package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestCapitalLadderRepoIntegration_CRUD(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newCapitalLadderIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewCapitalLadderRepo(pool)
	now := time.Now().UTC().Truncate(time.Second)
	entry := domain.CapitalLadderEntry{StrategyID: "s1", StepPct: 0.1, FillRate: 0.8, WinRate: 0.7, DrawdownPct: 0.05, BaselineFillRate: 0.75, BaselineWinRate: 0.65, AdvancedAt: &now}
	if err := repo.Upsert(ctx, entry); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StepPct != 0.1 || got.StrategyID != "s1" {
		t.Fatalf("got %#v", got)
	}
	if err := repo.UpdateMetrics(ctx, "s1", 0.9, 0.8, 0.02); err != nil {
		t.Fatal(err)
	}
	if err := repo.AdvanceStep(ctx, "s1", 0.2, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StepPct != 0.2 || got.BaselineFillRate != 0.9 || got.BaselineWinRate != 0.8 {
		t.Fatalf("got %#v", got)
	}
}

func TestCapitalLadderRepoListAndMissing(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newCapitalLadderIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewCapitalLadderRepo(pool)
	if _, err := repo.Get(ctx, "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := repo.Upsert(ctx, domain.CapitalLadderEntry{StrategyID: "s2"}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].StrategyID != "s2" {
		t.Fatalf("list %#v", list)
	}
}

func newCapitalLadderIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	connString := os.Getenv("DB_URL")
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		t.Skip("skipping integration test: DB_URL or DATABASE_URL is not set")
	}
	adminPool, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatalf("failed to create admin pool: %v", err)
	}
	schemaName := "integration_capital_ladder_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	if _, err := adminPool.Exec(ctx, `CREATE SCHEMA `+pqQuoteIdent(schemaName)); err != nil {
		adminPool.Close()
		t.Fatalf("failed to create test schema: %v", err)
	}
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to parse pool config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to create test pool: %v", err)
	}
	ddl := `CREATE TABLE capital_ladder (
		strategy_id TEXT PRIMARY KEY,
		step_pct DOUBLE PRECISION NOT NULL DEFAULT 0.10,
		fill_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
		win_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
		drawdown_pct DOUBLE PRECISION NOT NULL DEFAULT 0,
		baseline_fill_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
		baseline_win_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
		advanced_at TIMESTAMPTZ,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed ddl: %v", err)
	}
	return pool, func() {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
	}
}

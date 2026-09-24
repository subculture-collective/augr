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

func TestRiskBreakerRepoIntegration_CRUD(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newRiskBreakerIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewRiskBreakerRepo(pool)
	tripAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if err := repo.Trip(ctx, domain.RiskBreakerScopeGlobal, "initial", tripAt); err != nil {
		t.Fatalf("Trip() error = %v", err)
	}
	got, err := repo.Get(ctx, domain.RiskBreakerScopeGlobal)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Scope != domain.RiskBreakerScopeGlobal || got.Reason != "initial" {
		t.Fatalf("Get() = %#v", got)
	}
	if !got.TrippedAt.Equal(tripAt) {
		t.Fatalf("TrippedAt = %v, want %v", got.TrippedAt, tripAt)
	}
	resetAt := time.Now().UTC()
	if err := repo.Reset(ctx, domain.RiskBreakerScopeGlobal, resetAt); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	_, err = repo.Get(ctx, domain.RiskBreakerScopeGlobal)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get() after Reset error = %v, want ErrNotFound", err)
	}
	var storedResetAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT reset_at FROM risk_breaker_state WHERE scope = $1`, domain.RiskBreakerScopeGlobal).Scan(&storedResetAt); err != nil {
		t.Fatalf("row after Reset error = %v, want retained audit row", err)
	}
	if storedResetAt == nil || !storedResetAt.Equal(resetAt.Truncate(time.Microsecond)) {
		t.Fatalf("reset_at = %v, want %v", storedResetAt, resetAt)
	}
	open, err := repo.HasOpenBreaker(ctx, domain.RiskBreakerScopeGlobal, domain.RiskBreakerScopeStrategy("none"))
	if err != nil || open {
		t.Fatalf("HasOpenBreaker() after Reset = %t, %v; want false", open, err)
	}
	if err := repo.Trip(ctx, domain.RiskBreakerScopeGlobal, "again", time.Now().UTC()); err != nil {
		t.Fatalf("re-Trip() error = %v", err)
	}
	open, err = repo.HasOpenBreaker(ctx, domain.RiskBreakerScopeGlobal)
	if err != nil || !open {
		t.Fatalf("HasOpenBreaker() after re-trip = %t, %v; want true", open, err)
	}
	list, err := repo.ListTripped(ctx)
	if err != nil || len(list) != 1 || list[0].Reason != "again" {
		t.Fatalf("ListTripped() after re-trip = %+v, %v", list, err)
	}
}

func TestRiskBreakerRepo_HasOpenBreakerWithoutScopes(t *testing.T) {
	open, err := (&RiskBreakerRepo{}).HasOpenBreaker(context.Background())
	if err != nil || open {
		t.Fatalf("HasOpenBreaker() = %t, %v; want false without scopes", open, err)
	}
}

func TestRiskBreakerRepo_ResetMissingScopeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newRiskBreakerIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewRiskBreakerRepo(pool)
	if err := repo.Reset(ctx, domain.RiskBreakerScopeGlobal, time.Now().UTC()); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
}

func TestRiskBreakerRepoIntegration_UpsertAndList(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newRiskBreakerIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewRiskBreakerRepo(pool)
	first := time.Now().UTC().Add(-2 * time.Minute)
	second := time.Now().UTC().Add(-time.Minute)
	if err := repo.Trip(ctx, domain.RiskBreakerScopeStrategy("s1"), "one", first); err != nil {
		t.Fatal(err)
	}
	if err := repo.Trip(ctx, domain.RiskBreakerScopeStrategy("s1"), "two", second); err != nil {
		t.Fatal(err)
	}
	if err := repo.Trip(ctx, domain.RiskBreakerScopeGlobal, "global", second); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListTripped(ctx)
	if err != nil {
		t.Fatalf("ListTripped() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListTripped() len = %d, want 2", len(list))
	}
	if list[0].Scope != domain.RiskBreakerScopeStrategy("s1") || list[0].Reason != "two" {
		t.Fatalf("first list item = %#v", list[0])
	}
	if list[1].Scope != domain.RiskBreakerScopeGlobal {
		t.Fatalf("second list item = %#v", list[1])
	}
}

func newRiskBreakerIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
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
	schemaName := "integration_risk_breaker_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	ddl := `CREATE TABLE risk_breaker_state (
		scope TEXT PRIMARY KEY,
		tripped_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		reason TEXT NOT NULL,
		reset_at TIMESTAMPTZ
	)`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to apply test schema DDL: %v", err)
	}
	return pool, func() {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
	}
}

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

func TestPolymarketMarketDataRepoIntegration_TicksRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPolymarketMarketDataIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPolymarketMarketDataRepo(pool)
	now := time.Now().UTC().Truncate(time.Second)
	ticks := []domain.PolymarketTick{{Slug: "btc", Side: "yes", Price: 0.51, Size: 12.3, ReceivedAt: now.Add(-2 * time.Minute), SeqHint: 11, ConnID: 1}, {Slug: "btc", Side: "no", Price: 0.49, Size: 3.2, ReceivedAt: now.Add(-time.Minute), SeqHint: 12, ConnID: 1}}
	if err := repo.InsertTicks(ctx, ticks); err != nil {
		t.Fatalf("InsertTicks() error = %v", err)
	}
	got, err := repo.QueryTicks(ctx, "btc", now.Add(-10*time.Minute), now, 10)
	if err != nil {
		t.Fatalf("QueryTicks() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("QueryTicks() len = %d, want 2", len(got))
	}
	if got[0].SeqHint != 12 || got[1].SeqHint != 11 {
		t.Fatalf("QueryTicks() order = %#v, want desc by received_at", got)
	}
}

func TestPolymarketMarketDataRepoIntegration_BookSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPolymarketMarketDataIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPolymarketMarketDataRepo(pool)
	now := time.Now().UTC().Truncate(time.Second)
	snaps := []domain.PolymarketBookSnapshot{{Slug: "eth", BestBid: 0.45, BestAsk: 0.55, Bids: []domain.PolymarketBookLevel{{Price: 0.45, Size: 100}}, Asks: []domain.PolymarketBookLevel{{Price: 0.55, Size: 90}}, ReceivedAt: now.Add(-2 * time.Minute), ConnID: 7}, {Slug: "eth", BestBid: 0.46, BestAsk: 0.54, Bids: []domain.PolymarketBookLevel{{Price: 0.46, Size: 110}}, Asks: []domain.PolymarketBookLevel{{Price: 0.54, Size: 80}}, ReceivedAt: now.Add(-time.Minute), ConnID: 7}}
	if err := repo.InsertBookSnapshots(ctx, snaps); err != nil {
		t.Fatalf("InsertBookSnapshots() error = %v", err)
	}
	got, err := repo.QueryBookAt(ctx, "eth", now)
	if err != nil {
		t.Fatalf("QueryBookAt() error = %v", err)
	}
	if got.BestBid != 0.46 || len(got.Bids) != 1 || got.Bids[0].Price != 0.46 {
		t.Fatalf("QueryBookAt() = %#v, want latest snapshot", got)
	}
}

func TestPolymarketMarketDataRepoIntegration_QueryBookAtNotFound(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newPolymarketMarketDataIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewPolymarketMarketDataRepo(pool)
	_, err := repo.QueryBookAt(ctx, "missing", time.Now().UTC())
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("QueryBookAt() error = %v, want ErrNotFound", err)
	}
}

func newPolymarketMarketDataIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
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
	if _, err := adminPool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto`); err != nil {
		adminPool.Close()
		t.Fatalf("failed to ensure pgcrypto extension: %v", err)
	}
	schemaName := "integration_polymarket_market_data_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	ddl := `CREATE TABLE polymarket_ticks (slug TEXT NOT NULL, side TEXT NOT NULL, price DOUBLE PRECISION NOT NULL, size DOUBLE PRECISION NOT NULL, received_at TIMESTAMPTZ NOT NULL, seq_hint BIGINT NOT NULL, conn_id INTEGER NOT NULL)`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to apply ticks DDL: %v", err)
	}
	ddl = `CREATE TABLE polymarket_book_snapshots (slug TEXT NOT NULL, best_bid DOUBLE PRECISION NOT NULL, best_ask DOUBLE PRECISION NOT NULL, bids JSONB NOT NULL, asks JSONB NOT NULL, received_at TIMESTAMPTZ NOT NULL, conn_id INTEGER NOT NULL)`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to apply book DDL: %v", err)
	}
	return pool, func() {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+pqQuoteIdent(schemaName)+` CASCADE`)
		adminPool.Close()
	}
}

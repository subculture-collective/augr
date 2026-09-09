package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOpportunityOptionsListReleasesCursorBeforeLegQuery(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newOpportunityIntegrationPool(t, ctx)
	defer cleanup()
	// This isolated reader fixture tests connection ownership, not financial
	// evidence admission. Production graph validation is covered separately.
	for _, sql := range []string{
		`ALTER TYPE market_type ADD VALUE 'options'`,
		`ALTER TABLE portfolio_opportunity_option_legs
		 ADD COLUMN contract_payload_id UUID, ADD COLUMN contract_sha256 TEXT,
		 ADD COLUMN quote_payload_id UUID, ADD COLUMN quote_sha256 TEXT,
		 ADD COLUMN snapshot_payload_id UUID, ADD COLUMN snapshot_sha256 TEXT`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	strategyID := createTestStrategy(t, ctx, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO portfolio_opportunities
		(account_id,environment,strategy_id,market_type,ticker,side,signal,status,expires_at,dedupe_key,evaluation_scope_id)
		VALUES($1,'paper_scored',$2,'options','AAPL','buy','buy','queued',now()+interval '1 hour','pool-test',$3)`,
		canonicalRepositoryTestAccountID, strategyID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	config := pool.Config()
	config.MaxConns = 1
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	listPool, err := pgxpool.NewWithConfig(listCtx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer listPool.Close()
	values, err := NewOpportunityRepo(listPool, canonicalRepositoryTestAccountID).List(listCtx, repository.OpportunityFilter{}, 10, 0)
	if err != nil || len(values) != 1 {
		t.Fatalf("options list=%d/%v", len(values), err)
	}
}

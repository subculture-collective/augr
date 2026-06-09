package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestReplayEventRepoIntegration_CreateAndList(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newReplayEventIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewReplayEventRepo(pool)
	decisionID := insertReplayDecisionRow(t, ctx, pool)

	event := &domain.ReplayEvent{
		TradeDecisionID: decisionID,
		EventType:       domain.ReplayEventTypePaperOrdered,
		Source:          "operator",
		Payload:         json.RawMessage(`{"price":0.51}`),
		OccurredAt:      time.Date(2026, time.June, 9, 10, 0, 0, 0, time.UTC),
	}

	if err := repo.CreateReplayEvent(ctx, event); err != nil {
		t.Fatalf("CreateReplayEvent() error = %v", err)
	}
	if event.ID == uuid.Nil {
		t.Fatal("expected CreateReplayEvent() to populate ID")
	}
	if event.CreatedAt.IsZero() {
		t.Fatal("expected CreateReplayEvent() to populate CreatedAt")
	}

	got, err := repo.ListReplayEvents(ctx, decisionID)
	if err != nil {
		t.Fatalf("ListReplayEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 replay event, got %d", len(got))
	}
	if got[0].ID != event.ID || got[0].TradeDecisionID != decisionID || got[0].EventType != event.EventType {
		t.Fatalf("unexpected replay event: %+v", got[0])
	}
	if got[0].Source != event.Source {
		t.Fatalf("source = %q, want %q", got[0].Source, event.Source)
	}
	if !jsonBytesEqual(got[0].Payload, event.Payload) {
		t.Fatalf("payload = %s, want %s", got[0].Payload, event.Payload)
	}
}

func TestReplayEventRepoIntegration_ListOrdersDeterministically(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newReplayEventIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewReplayEventRepo(pool)
	decisionID := insertReplayDecisionRow(t, ctx, pool)
	occurred := time.Date(2026, time.June, 9, 12, 0, 0, 0, time.UTC)
	created := time.Date(2026, time.June, 9, 12, 5, 0, 0, time.UTC)

	firstID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	secondID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	thirdID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	insertReplayEventRow(t, ctx, pool, replayEventRow{
		ID:              thirdID,
		TradeDecisionID: decisionID,
		EventType:       domain.ReplayEventTypeOutcomeResolved,
		Source:          "system",
		Payload:         json.RawMessage(`{"outcome":"YES"}`),
		OccurredAt:      occurred,
		CreatedAt:       created,
	})
	insertReplayEventRow(t, ctx, pool, replayEventRow{
		ID:              secondID,
		TradeDecisionID: decisionID,
		EventType:       domain.ReplayEventTypeFillObserved,
		Source:          "system",
		OccurredAt:      occurred,
		CreatedAt:       created,
	})
	insertReplayEventRow(t, ctx, pool, replayEventRow{
		ID:              firstID,
		TradeDecisionID: decisionID,
		EventType:       domain.ReplayEventTypePaperOrdered,
		Source:          "system",
		OccurredAt:      occurred,
		CreatedAt:       created,
	})

	got, err := repo.ListReplayEvents(ctx, decisionID)
	if err != nil {
		t.Fatalf("ListReplayEvents() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 replay events, got %d", len(got))
	}
	if got[0].ID != firstID || got[1].ID != secondID || got[2].ID != thirdID {
		t.Fatalf("unexpected order: %s, %s, %s", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestReplayEventRepoIntegration_ListReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newReplayEventIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewReplayEventRepo(pool)
	decisionID := insertReplayDecisionRow(t, ctx, pool)

	got, err := repo.ListReplayEvents(ctx, decisionID)
	if err != nil {
		t.Fatalf("ListReplayEvents() error = %v", err)
	}
	if got == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 replay events, got %d", len(got))
	}
}

type replayEventRow struct {
	ID              uuid.UUID
	TradeDecisionID uuid.UUID
	EventType       domain.ReplayEventType
	Source          string
	Payload         json.RawMessage
	OccurredAt      time.Time
	CreatedAt       time.Time
}

func insertReplayDecisionRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO trade_decisions DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert trade decision: %v", err)
	}
	return id
}

func insertReplayEventRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, row replayEventRow) {
	t.Helper()
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	if row.Source == "" {
		row.Source = "system"
	}
	if len(row.Payload) == 0 {
		row.Payload = json.RawMessage(`{}`)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO replay_events (
		id, trade_decision_id, event_type, source, payload, occurred_at, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7)`, row.ID, row.TradeDecisionID, row.EventType, row.Source, row.Payload, row.OccurredAt, row.CreatedAt); err != nil {
		t.Fatalf("insert replay event: %v", err)
	}
}

func newReplayEventIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
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

	schemaName := "integration_replay_event_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	sanitizedSchemaName := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, `CREATE SCHEMA `+sanitizedSchemaName); err != nil {
		adminPool.Close()
		t.Fatalf("failed to create test schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+sanitizedSchemaName+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to parse pool config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName + ",public"

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+sanitizedSchemaName+` CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to create test pool: %v", err)
	}

	ddl := []string{
		`CREATE TABLE trade_decisions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid()
		)`,
		`CREATE TABLE replay_events (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			trade_decision_id UUID NOT NULL REFERENCES trade_decisions(id) ON DELETE CASCADE,
			event_type TEXT NOT NULL CHECK (event_type IN (
				'decision_created', 'risk_reviewed', 'paper_ordered', 'live_ordered',
				'fill_observed', 'position_updated', 'outcome_resolved'
			)),
			source TEXT NOT NULL DEFAULT 'system',
			payload JSONB NOT NULL DEFAULT '{}'::jsonb,
			occurred_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX idx_replay_events_trade_decision_occurred ON replay_events(trade_decision_id, occurred_at)`,
	}

	for _, stmt := range ddl {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			pool.Close()
			_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+sanitizedSchemaName+` CASCADE`)
			adminPool.Close()
			t.Fatalf("failed to apply test schema DDL: %v", err)
		}
	}

	cleanup := func() {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+sanitizedSchemaName+` CASCADE`)
		adminPool.Close()
	}

	return pool, cleanup
}

package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// ReplayEventRepo implements repository.ReplayEventRepository using PostgreSQL.
type ReplayEventRepo struct {
	pool *pgxpool.Pool
}

// Compile-time check that ReplayEventRepo satisfies ReplayEventRepository.
var _ repository.ReplayEventRepository = (*ReplayEventRepo)(nil)

// NewReplayEventRepo returns a replay-event repository backed by the given pool.
func NewReplayEventRepo(pool *pgxpool.Pool) *ReplayEventRepo {
	return &ReplayEventRepo{pool: pool}
}

const replayEventSelectSQL = `SELECT id, trade_decision_id, event_type, source, payload, occurred_at, created_at FROM replay_events`

// CreateReplayEvent inserts a new replay event and populates generated fields.
func (r *ReplayEventRepo) CreateReplayEvent(ctx context.Context, event *domain.ReplayEvent) error {
	payload, err := marshalReplayEventPayload(event.Payload)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}

	source := event.Source
	if source == "" {
		source = "system"
	}

	row := r.pool.QueryRow(ctx,
		`INSERT INTO replay_events (
			trade_decision_id, event_type, source, payload, occurred_at
		)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at`,
		event.TradeDecisionID,
		event.EventType,
		source,
		payload,
		event.OccurredAt,
	)

	if err := row.Scan(&event.ID, &event.CreatedAt); err != nil {
		return fmt.Errorf("postgres: create replay event: %w", err)
	}

	event.Source = source
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}

	return nil
}

// ListReplayEvents returns replay events for a decision ordered deterministically.
func (r *ReplayEventRepo) ListReplayEvents(ctx context.Context, tradeDecisionID uuid.UUID) ([]domain.ReplayEvent, error) {
	rows, err := r.pool.Query(ctx, replayEventSelectSQL+` WHERE trade_decision_id = $1 ORDER BY occurred_at ASC, created_at ASC, id ASC`, tradeDecisionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list replay events: %w", err)
	}
	defer rows.Close()

	events := make([]domain.ReplayEvent, 0)
	for rows.Next() {
		event, err := scanReplayEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: list replay events scan: %w", err)
		}
		events = append(events, *event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list replay events rows: %w", err)
	}

	return events, nil
}

func scanReplayEvent(sc scanner) (*domain.ReplayEvent, error) {
	var event domain.ReplayEvent
	var payload []byte

	if err := sc.Scan(
		&event.ID,
		&event.TradeDecisionID,
		&event.EventType,
		&event.Source,
		&payload,
		&event.OccurredAt,
		&event.CreatedAt,
	); err != nil {
		return nil, err
	}

	if len(payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	} else {
		event.Payload = json.RawMessage(payload)
	}
	if event.Source == "" {
		event.Source = "system"
	}

	return &event, nil
}

func marshalReplayEventPayload(data json.RawMessage) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("postgres: replay event payload is not valid JSON")
	}
	return data, nil
}

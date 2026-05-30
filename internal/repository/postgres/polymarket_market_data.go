package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type PolymarketMarketDataRepo struct{ pool *pgxpool.Pool }

var _ repository.PolymarketMarketDataRepository = (*PolymarketMarketDataRepo)(nil)

func NewPolymarketMarketDataRepo(pool *pgxpool.Pool) *PolymarketMarketDataRepo {
	return &PolymarketMarketDataRepo{pool: pool}
}

func (r *PolymarketMarketDataRepo) InsertTicks(ctx context.Context, ticks []domain.PolymarketTick) error {
	if len(ticks) == 0 {
		return nil
	}
	_, err := r.pool.CopyFrom(ctx, pgx.Identifier{"polymarket_ticks"}, []string{"slug", "side", "price", "size", "received_at", "seq_hint", "conn_id"}, pgx.CopyFromSlice(len(ticks), func(i int) ([]any, error) {
		t := ticks[i]
		return []any{t.Slug, t.Side, t.Price, t.Size, t.ReceivedAt, t.SeqHint, t.ConnID}, nil
	}))
	if err != nil {
		return fmt.Errorf("postgres: insert polymarket ticks: %w", err)
	}
	return nil
}

func (r *PolymarketMarketDataRepo) InsertBookSnapshots(ctx context.Context, snaps []domain.PolymarketBookSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	_, err := r.pool.CopyFrom(ctx, pgx.Identifier{"polymarket_book_snapshots"}, []string{"slug", "best_bid", "best_ask", "bids", "asks", "received_at", "conn_id"}, pgx.CopyFromSlice(len(snaps), func(i int) ([]any, error) {
		s := snaps[i]
		bids, _ := json.Marshal(s.Bids)
		asks, _ := json.Marshal(s.Asks)
		return []any{s.Slug, s.BestBid, s.BestAsk, bids, asks, s.ReceivedAt, s.ConnID}, nil
	}))
	if err != nil {
		return fmt.Errorf("postgres: insert polymarket book snapshots: %w", err)
	}
	return nil
}

func (r *PolymarketMarketDataRepo) QueryTicks(ctx context.Context, slug string, from, to time.Time, limit int) ([]domain.PolymarketTick, error) {
	rows, err := r.pool.Query(ctx, `SELECT slug, side, price, size, received_at, seq_hint, conn_id FROM polymarket_ticks WHERE slug=$1 AND received_at BETWEEN $2 AND $3 ORDER BY received_at DESC LIMIT $4`, slug, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PolymarketTick
	for rows.Next() {
		var t domain.PolymarketTick
		if err := rows.Scan(&t.Slug, &t.Side, &t.Price, &t.Size, &t.ReceivedAt, &t.SeqHint, &t.ConnID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *PolymarketMarketDataRepo) QueryBookAt(ctx context.Context, slug string, at time.Time) (*domain.PolymarketBookSnapshot, error) {
	row := r.pool.QueryRow(ctx, `SELECT slug, best_bid, best_ask, bids, asks, received_at, conn_id FROM polymarket_book_snapshots WHERE slug=$1 AND received_at <= $2 ORDER BY received_at DESC LIMIT 1`, slug, at)
	var s domain.PolymarketBookSnapshot
	var bids, asks []byte
	if err := row.Scan(&s.Slug, &s.BestBid, &s.BestAsk, &bids, &asks, &s.ReceivedAt, &s.ConnID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal(bids, &s.Bids)
	_ = json.Unmarshal(asks, &s.Asks)
	return &s, nil
}

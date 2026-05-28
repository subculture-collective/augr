package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// PolymarketAccountRepo implements repository.PolymarketAccountRepository.
type PolymarketAccountRepo struct {
	pool *pgxpool.Pool
}

var _ repository.PolymarketAccountRepository = (*PolymarketAccountRepo)(nil)

// NewPolymarketAccountRepo returns a PolymarketAccountRepo backed by the given pool.
func NewPolymarketAccountRepo(pool *pgxpool.Pool) *PolymarketAccountRepo {
	return &PolymarketAccountRepo{pool: pool}
}

// UpsertAccount inserts or updates a Polymarket account profile.
func (r *PolymarketAccountRepo) UpsertAccount(ctx context.Context, acc *domain.PolymarketAccount) error {
	statsJSON, _ := json.Marshal(acc.CategoryStats)

	_, err := r.pool.Exec(ctx, `
		INSERT INTO polymarket_accounts (
			address, display_name, first_seen, last_active,
			total_trades, total_volume, markets_entered, markets_won, markets_lost,
			win_rate, category_stats, avg_position, max_position,
			avg_entry_hours_before_resolution, early_entry_rate, tags, tracked, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,NOW())
		ON CONFLICT (address) DO UPDATE SET
			display_name       = EXCLUDED.display_name,
			last_active        = EXCLUDED.last_active,
			total_trades       = EXCLUDED.total_trades,
			total_volume       = EXCLUDED.total_volume,
			markets_entered    = EXCLUDED.markets_entered,
			markets_won        = EXCLUDED.markets_won,
			markets_lost       = EXCLUDED.markets_lost,
			win_rate           = EXCLUDED.win_rate,
			category_stats     = EXCLUDED.category_stats,
			avg_position       = EXCLUDED.avg_position,
			max_position       = EXCLUDED.max_position,
			avg_entry_hours_before_resolution = EXCLUDED.avg_entry_hours_before_resolution,
			early_entry_rate   = EXCLUDED.early_entry_rate,
			tags               = EXCLUDED.tags,
			tracked            = EXCLUDED.tracked,
			updated_at         = NOW()`,
		acc.Address,
		acc.DisplayName,
		acc.FirstSeen,
		acc.LastActive,
		acc.TotalTrades,
		acc.TotalVolume,
		acc.MarketsEntered,
		acc.MarketsWon,
		acc.MarketsLost,
		acc.WinRate,
		json.RawMessage(statsJSON),
		acc.AvgPosition,
		acc.MaxPosition,
		acc.AvgEntryHoursBeforeResolution,
		acc.EarlyEntryRate,
		acc.Tags,
		acc.Tracked,
	)
	if err != nil {
		return fmt.Errorf("postgres: upsert polymarket account: %w", err)
	}
	return nil
}

// GetAccount returns a single account by wallet address.
func (r *PolymarketAccountRepo) GetAccount(ctx context.Context, address string) (*domain.PolymarketAccount, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT address, display_name, first_seen, last_active,
		       total_trades, total_volume, markets_entered, markets_won, markets_lost,
		       win_rate, category_stats, avg_position, max_position,
		       avg_entry_hours_before_resolution, early_entry_rate, tags, tracked, updated_at
		FROM polymarket_accounts
		WHERE address = $1`, address)

	acc, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: get polymarket account: %w", err)
	}
	return acc, nil
}

// ListTrackedAccounts returns tracked accounts with win_rate >= minWinRate.
func (r *PolymarketAccountRepo) ListTrackedAccounts(ctx context.Context, minWinRate float64, limit int) ([]domain.PolymarketAccount, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT address, display_name, first_seen, last_active,
		       total_trades, total_volume, markets_entered, markets_won, markets_lost,
		       win_rate, category_stats, avg_position, max_position,
		       avg_entry_hours_before_resolution, early_entry_rate, tags, tracked, updated_at
		FROM polymarket_accounts
		WHERE tracked = true AND win_rate >= $1
		ORDER BY win_rate DESC
		LIMIT $2`, minWinRate, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list tracked accounts: %w", err)
	}
	defer rows.Close()

	var accounts []domain.PolymarketAccount
	for rows.Next() {
		acc, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan account: %w", err)
		}
		accounts = append(accounts, *acc)
	}
	return accounts, rows.Err()
}

// ListAccounts returns Polymarket accounts matching the provided filter.
func (r *PolymarketAccountRepo) ListAccounts(ctx context.Context, filter repository.PolymarketAccountFilter) ([]domain.PolymarketAccount, error) {
	limit := filter.Limit
	if limit <= 0 { limit = 100 }
	offset := filter.Offset
	sortCol := map[string]string{"volume":"total_volume","win_rate":"win_rate","last_active":"last_active","trades":"total_trades"}[strings.ToLower(strings.TrimSpace(filter.Sort))]
	if sortCol == "" { sortCol = "last_active" }
	query := `SELECT address, display_name, first_seen, last_active, total_trades, total_volume, markets_entered, markets_won, markets_lost, win_rate, category_stats, avg_position, max_position, avg_entry_hours_before_resolution, early_entry_rate, tags, tracked, updated_at FROM polymarket_accounts WHERE 1=1`
	args := []any{}
	if filter.Tracked != nil { query += fmt.Sprintf(" AND tracked = $%d", len(args)+1); args = append(args, *filter.Tracked) }
	if filter.MinWinRate > 0 { query += fmt.Sprintf(" AND win_rate >= $%d", len(args)+1); args = append(args, filter.MinWinRate) }
	if filter.MinVolume > 0 { query += fmt.Sprintf(" AND total_volume >= $%d", len(args)+1); args = append(args, filter.MinVolume) }
	if filter.MinTrades > 0 { query += fmt.Sprintf(" AND total_trades >= $%d", len(args)+1); args = append(args, filter.MinTrades) }
	query += fmt.Sprintf(" ORDER BY %s DESC LIMIT $%d OFFSET $%d", sortCol, len(args)+1, len(args)+2)
	args = append(args, limit, offset)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.PolymarketAccount
	for rows.Next() { acc, err := scanAccount(rows); if err != nil { return nil, err }; out = append(out, *acc) }
	return out, rows.Err()
}

// InsertTrades bulk-inserts trade records, ignoring duplicates.
func (r *PolymarketAccountRepo) InsertTrades(ctx context.Context, trades []domain.PolymarketAccountTrade) error {
	filtered, _ := filterSupportedPolymarketTrades(trades)
	if len(filtered) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, t := range filtered {
		normalizedSide, err := normalizePolymarketTradeSide(t.Side)
		if err != nil {
			return fmt.Errorf("postgres: normalize trade side for %s: %w", t.AccountAddress, err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO polymarket_account_trades
				(account_address, market_slug, side, action, price, size_usdc, timestamp, outcome, pnl)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT DO NOTHING`,
			t.AccountAddress, t.MarketSlug, normalizedSide, t.Action,
			t.Price, t.SizeUSDC, t.Timestamp, nilStr(t.Outcome), t.PnL,
		)
		if err != nil {
			return fmt.Errorf("postgres: insert trade for %s: %w", t.AccountAddress, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit trades: %w", err)
	}
	return nil
}

func filterSupportedPolymarketTrades(trades []domain.PolymarketAccountTrade) ([]domain.PolymarketAccountTrade, []domain.PolymarketAccountTrade) {
	filtered := make([]domain.PolymarketAccountTrade, 0, len(trades))
	skipped := make([]domain.PolymarketAccountTrade, 0)
	for _, trade := range trades {
		if _, err := normalizePolymarketTradeSide(trade.Side); err != nil {
			skipped = append(skipped, trade)
			continue
		}
		filtered = append(filtered, trade)
	}
	return filtered, skipped
}

// ListTradesByAccount returns trades for a given address within [from, to].
func (r *PolymarketAccountRepo) ListTradesByAccount(ctx context.Context, address string, from, to time.Time, limit int) ([]domain.PolymarketAccountTrade, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_address, market_slug, side, action, price, size_usdc,
		       timestamp, COALESCE(outcome,''), pnl, created_at
		FROM polymarket_account_trades
		WHERE account_address = $1 AND timestamp BETWEEN $2 AND $3
		ORDER BY timestamp DESC
		LIMIT $4`, address, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list trades by account: %w", err)
	}
	defer rows.Close()

	var trades []domain.PolymarketAccountTrade
	for rows.Next() {
		var t domain.PolymarketAccountTrade
		if err := rows.Scan(
			&t.ID, &t.AccountAddress, &t.MarketSlug, &t.Side, &t.Action,
			&t.Price, &t.SizeUSDC, &t.Timestamp, &t.Outcome, &t.PnL, &t.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan trade: %w", err)
		}
		trades = append(trades, t)
	}
	return trades, rows.Err()
}

func (r *PolymarketAccountRepo) ListAllTradesBySlug(ctx context.Context, slug string, limit int) ([]domain.PolymarketAccountTrade, error) {
	if limit <= 0 { limit = 1000 }
	rows, err := r.pool.Query(ctx, `SELECT id, account_address, market_slug, side, action, price, size_usdc, timestamp, COALESCE(outcome,''), pnl, created_at FROM polymarket_account_trades WHERE market_slug=$1 ORDER BY timestamp DESC LIMIT $2`, slug, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.PolymarketAccountTrade
	for rows.Next() { var t domain.PolymarketAccountTrade; if err := rows.Scan(&t.ID,&t.AccountAddress,&t.MarketSlug,&t.Side,&t.Action,&t.Price,&t.SizeUSDC,&t.Timestamp,&t.Outcome,&t.PnL,&t.CreatedAt); err != nil { return nil, err }; out = append(out, t) }
	return out, rows.Err()
}

func (r *PolymarketAccountRepo) ListRecentTrades(ctx context.Context, limit int) ([]domain.PolymarketAccountTrade, error) {
	if limit <= 0 { limit = 200 }
	rows, err := r.pool.Query(ctx, `SELECT id, account_address, market_slug, side, action, price, size_usdc, timestamp, COALESCE(outcome,''), pnl, created_at FROM polymarket_account_trades ORDER BY timestamp DESC LIMIT $1`, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []domain.PolymarketAccountTrade
	for rows.Next() { var t domain.PolymarketAccountTrade; if err := rows.Scan(&t.ID,&t.AccountAddress,&t.MarketSlug,&t.Side,&t.Action,&t.Price,&t.SizeUSDC,&t.Timestamp,&t.Outcome,&t.PnL,&t.CreatedAt); err != nil { return nil, err }; out = append(out, t) }
	return out, rows.Err()
}

// MarkTracked auto-flags accounts with high win rate as tracked.
func (r *PolymarketAccountRepo) MarkTracked(ctx context.Context, minWinRate float64, minResolved int) (int64, error) {
	result, err := r.pool.Exec(ctx, `
		UPDATE polymarket_accounts
		SET tracked = true, updated_at = NOW()
		WHERE tracked = false
		  AND win_rate >= $1
		  AND (markets_won + markets_lost) >= $2`,
		minWinRate, minResolved)
	if err != nil {
		return 0, fmt.Errorf("postgres: mark tracked: %w", err)
	}
	return result.RowsAffected(), nil
}

func (r *PolymarketAccountRepo) SetTracked(ctx context.Context, address string, tracked bool) error { res, err := r.pool.Exec(ctx, `UPDATE polymarket_accounts SET tracked=$2, updated_at=NOW() WHERE address=$1`, address, tracked); if err != nil { return err }; if res.RowsAffected()==0 { return repository.ErrNotFound }; return nil }

func (r *PolymarketAccountRepo) UpdateAccountResolutionStats(ctx context.Context, address string, won, lost int, winRate float64) error { _, err := r.pool.Exec(ctx, `UPDATE polymarket_accounts SET markets_won=$2, markets_lost=$3, win_rate=$4, updated_at=NOW() WHERE address=$1`, address, won, lost, winRate); if err != nil { return err }; return nil }

func (r *PolymarketAccountRepo) IncrementAccountResolutionStats(ctx context.Context, address string, wonDelta, lostDelta int) error { _, err := r.pool.Exec(ctx, `UPDATE polymarket_accounts SET markets_won = markets_won + $2, markets_lost = markets_lost + $3, win_rate = CASE WHEN (markets_won + $2 + markets_lost + $3) = 0 THEN 0 ELSE (markets_won + $2)::float / NULLIF((markets_won + $2 + markets_lost + $3),0) END, updated_at=NOW() WHERE address=$1`, address, wonDelta, lostDelta); return err }

func normalizePolymarketTradeSide(side string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "YES":
		return "YES", nil
	case "NO":
		return "NO", nil
	case "UP":
		return "Up", nil
	case "DOWN":
		return "Down", nil
	case "OVER":
		return "Over", nil
	case "UNDER":
		return "Under", nil
	default:
		return "", fmt.Errorf("unsupported side %q", side)
	}
}

// scanAccount scans one row into a PolymarketAccount.
type accountScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row accountScanner) (*domain.PolymarketAccount, error) {
	var acc domain.PolymarketAccount
	var statsJSON []byte
	if err := row.Scan(
		&acc.Address,
		&acc.DisplayName,
		&acc.FirstSeen,
		&acc.LastActive,
		&acc.TotalTrades,
		&acc.TotalVolume,
		&acc.MarketsEntered,
		&acc.MarketsWon,
		&acc.MarketsLost,
		&acc.WinRate,
		&statsJSON,
		&acc.AvgPosition,
		&acc.MaxPosition,
		&acc.AvgEntryHoursBeforeResolution,
		&acc.EarlyEntryRate,
		&acc.Tags,
		&acc.Tracked,
		&acc.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(statsJSON) > 0 {
		_ = json.Unmarshal(statsJSON, &acc.CategoryStats)
	}
	return &acc, nil
}

func nilStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

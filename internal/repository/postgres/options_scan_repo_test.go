package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type optionsScanTxStub struct {
	execs      int
	failExecAt int
	commitErr  error
	committed  bool
	rolledBack bool
}

func (s *optionsScanTxStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	s.execs++
	if s.execs == s.failExecAt {
		return pgconn.CommandTag{}, errors.New("forced write failure")
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (s *optionsScanTxStub) Commit(context.Context) error {
	if s.commitErr != nil {
		return s.commitErr
	}
	s.committed = true
	return nil
}

func TestUpsertScanAndHistoryRollsBackCommitFailure(t *testing.T) {
	t.Parallel()
	tx := &optionsScanTxStub{commitErr: errors.New("forced commit failure")}
	repo := &OptionsScanRepo{begin: func(context.Context) (optionsScanTx, error) { return tx, nil }}
	date := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	err := repo.UpsertScanAndHistory(t.Context(), &OptionsScanResult{Ticker: "AAPL", ScanDate: date}, &IVHistoryRecord{Ticker: "AAPL", Date: date})
	if err == nil || tx.committed || !tx.rolledBack {
		t.Fatalf("UpsertScanAndHistory() err=%v committed=%v rolledBack=%v", err, tx.committed, tx.rolledBack)
	}
}

func (s *optionsScanTxStub) Rollback(context.Context) error {
	s.rolledBack = true
	return nil
}

func TestUpsertScanAndHistoryRollsBackEitherWrite(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			tx := &optionsScanTxStub{failExecAt: failAt}
			repo := &OptionsScanRepo{begin: func(context.Context) (optionsScanTx, error) { return tx, nil }}
			err := repo.UpsertScanAndHistory(t.Context(), &OptionsScanResult{Ticker: "AAPL", ScanDate: date}, &IVHistoryRecord{Ticker: "AAPL", Date: date})
			if err == nil || tx.committed || !tx.rolledBack {
				t.Fatalf("UpsertScanAndHistory() err=%v committed=%v rolledBack=%v", err, tx.committed, tx.rolledBack)
			}
		})
	}
}

func TestUpsertScanAndHistoryCommitsBothWrites(t *testing.T) {
	t.Parallel()
	tx := &optionsScanTxStub{}
	repo := &OptionsScanRepo{begin: func(context.Context) (optionsScanTx, error) { return tx, nil }}
	date := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	if err := repo.UpsertScanAndHistory(t.Context(), &OptionsScanResult{Ticker: "AAPL", ScanDate: date}, &IVHistoryRecord{Ticker: "AAPL", Date: date}); err != nil {
		t.Fatalf("UpsertScanAndHistory() error = %v", err)
	}
	if tx.execs != 2 || !tx.committed {
		t.Fatalf("writes=%d committed=%v, want two committed writes", tx.execs, tx.committed)
	}
}

func TestUpsertScanAndHistoryIntegrationRollbackAndIdempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := t.Context()
	pool, cleanup := newOptionsScanIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewOptionsScanRepo(pool)
	date := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)

	err := repo.UpsertScanAndHistory(ctx,
		&OptionsScanResult{Ticker: "ROLLBACK", ScanDate: date, ATMIV: 0.4},
		&IVHistoryRecord{Ticker: "ROLLBACK", Date: date, ATMIV: -1},
	)
	if err == nil {
		t.Fatal("UpsertScanAndHistory(invalid history) = nil, want error")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM options_scan_results WHERE ticker = 'ROLLBACK'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back scan count=%d err=%v, want 0", count, err)
	}

	for _, iv := range []float64{0.4, 0.5} {
		if err := repo.UpsertScanAndHistory(ctx,
			&OptionsScanResult{Ticker: "AAPL", ScanDate: date, ATMIV: iv},
			&IVHistoryRecord{Ticker: "AAPL", Date: date, ATMIV: iv},
		); err != nil {
			t.Fatalf("UpsertScanAndHistory(idempotent) error = %v", err)
		}
	}
	var scanCount, historyCount int
	var scanIV, historyIV float64
	if err := pool.QueryRow(ctx, `SELECT count(*), max(atm_iv) FROM options_scan_results WHERE ticker = 'AAPL'`).Scan(&scanCount, &scanIV); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*), max(atm_iv) FROM iv_history WHERE ticker = 'AAPL'`).Scan(&historyCount, &historyIV); err != nil {
		t.Fatal(err)
	}
	if scanCount != 1 || historyCount != 1 || scanIV != 0.5 || historyIV != 0.5 {
		t.Fatalf("scan=(%d, %.2f) history=(%d, %.2f), want one updated row each", scanCount, scanIV, historyCount, historyIV)
	}
}

func newOptionsScanIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	connString := os.Getenv("DB_URL")
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		t.Skip("skipping integration test: DB_URL or DATABASE_URL is not set")
	}
	admin, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatal(err)
	}
	schema := "options_scan_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	ddl := `CREATE TABLE options_scan_results (
		ticker text NOT NULL, scan_date timestamptz NOT NULL, close_price double precision NOT NULL DEFAULT 0,
		adv double precision NOT NULL DEFAULT 0, iv_rank double precision NOT NULL DEFAULT 0,
		iv_percentile double precision NOT NULL DEFAULT 0, atm_iv double precision NOT NULL DEFAULT 0,
		put_call_ratio double precision NOT NULL DEFAULT 0, volume_ratio double precision NOT NULL DEFAULT 0,
		chain_depth integer NOT NULL DEFAULT 0, atm_oi double precision NOT NULL DEFAULT 0,
		score double precision NOT NULL DEFAULT 0, PRIMARY KEY (ticker, scan_date));
	CREATE TABLE iv_history (
		ticker text NOT NULL, date timestamptz NOT NULL, atm_iv double precision NOT NULL CHECK (atm_iv >= 0),
		iv_rank double precision NOT NULL DEFAULT 0, iv_percentile double precision NOT NULL DEFAULT 0,
		PRIMARY KEY (ticker, date));`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	}
}

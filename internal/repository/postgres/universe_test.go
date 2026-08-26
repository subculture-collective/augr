package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/universe"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCanonicalUniverseTicker(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"already canonical": "AAPL",
		"case variant":      "FDXW",
		"class symbol":      "BF.B",
		"empty":             "",
	}
	inputs := map[string]string{
		"already canonical": "AAPL",
		"case variant":      "  FDXw ",
		"class symbol":      " bf.b ",
		"empty":             " \t ",
	}

	for name, want := range tests {
		name, input, want := name, inputs[name], want
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := canonicalUniverseTicker(input); got != want {
				t.Fatalf("canonicalUniverseTicker(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestUniverseWatchlistQueryCanonicalizesAndDeduplicates(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"DISTINCT ON (upper(trim(ticker)))",
		"upper(trim(ticker)) AS normalized_ticker",
		"(ticker = upper(trim(ticker))) DESC",
	} {
		if !strings.Contains(universeWatchlistQuery, want) {
			t.Fatalf("universeWatchlistQuery missing %q", want)
		}
	}
}

func TestBuildUniverseListQueryCanonicalizesBeforePagination(t *testing.T) {
	t.Parallel()

	active := true
	query, args := buildUniverseListQuery(universe.ListFilter{
		IndexGroup: "nasdaq",
		Active:     &active,
		Search:     "fdxw",
	}, 25, 50)
	for _, want := range []string{
		"DISTINCT ON (upper(trim(ticker)))",
		"upper(trim(ticker)) AS normalized_ticker",
		"(ticker = upper(trim(ticker))) DESC",
		"ORDER BY watch_score DESC, normalized_ticker",
		"LIMIT $5 OFFSET $6",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("buildUniverseListQuery() missing %q in %q", want, query)
		}
	}
	if len(args) != 6 || args[0] != "nasdaq" || args[4] != 25 || args[5] != 50 {
		t.Fatalf("buildUniverseListQuery() args = %#v, want filters then 25/50 pagination", args)
	}
}

func TestUniverseCountQueryUsesCanonicalIdentity(t *testing.T) {
	t.Parallel()

	if !strings.Contains(universeCountQuery, "COUNT(DISTINCT upper(trim(ticker)))") {
		t.Fatalf("universeCountQuery = %q, want canonical distinct count", universeCountQuery)
	}
}

func TestReplaceConstituentsPreparationOrder(t *testing.T) {
	t.Parallel()

	wantNames := []string{"lock", "snapshot provenance", "deactivate"}
	gotNames := make([]string, 0, len(replaceConstituentsPreparation))
	for _, step := range replaceConstituentsPreparation {
		gotNames = append(gotNames, step.name)
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("replace preparation order = %v, want %v", gotNames, wantNames)
	}
	if !strings.Contains(replaceConstituentsPreparation[0].query, "pg_advisory_xact_lock") ||
		!strings.Contains(replaceConstituentsPreparation[0].query, "hashtextextended('augr:universe:replace-constituents', 0)") {
		t.Fatalf("first replacement query does not acquire the namespaced transaction lock: %q", replaceConstituentsPreparation[0].query)
	}
	if !strings.Contains(replaceConstituentsPreparation[1].query, "prior_universe_provenance") {
		t.Fatalf("second replacement query does not snapshot provenance: %q", replaceConstituentsPreparation[1].query)
	}
	if !strings.Contains(replaceConstituentsPreparation[2].query, "SET active = false") {
		t.Fatalf("third replacement query does not deactivate prior constituents: %q", replaceConstituentsPreparation[2].query)
	}
}

func TestUniverseReplaceConstituentsIntegration(t *testing.T) {
	pool, cleanup := newUniverseIntegrationPool(t, t.Context())
	defer cleanup()
	ctx := t.Context()
	repo := NewUniverseRepo(pool)
	lastScanned := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	earlierScan := lastScanned.Add(-48 * time.Hour)
	laterScan := lastScanned.Add(48 * time.Hour)
	_, err := pool.Exec(ctx, `INSERT INTO universe_tickers
		(ticker, name, exchange, index_group, watch_score, last_scanned, active)
		VALUES ('KEEP', 'Old name', 'OTC', 'other', 42.5, $1, true),
		       (' lower ', 'Old lower', 'OTC', 'other', 63, $1, true),
		       ('merge', 'High score', 'OTC', 'other', 90, $2, true),
		       (' MERGE ', 'Late scan', 'OTC', 'other', 20, $3, true),
		       ('DROP', 'Removed', 'XNAS', 'nasdaq', 7, $1, true)`, lastScanned, earlierScan, laterScan)
	if err != nil {
		t.Fatal(err)
	}

	replacement := []universe.TrackedTicker{
		{Ticker: " keep ", Name: "New name", Exchange: "XNYS", IndexGroup: "nyse", Active: true},
		{Ticker: "LOWER", Name: "Authoritative lower", Exchange: "XNYS", IndexGroup: "nyse", Active: true},
		{Ticker: "MERGE", Name: "Authoritative merge", Exchange: "XNAS", IndexGroup: "nasdaq", Active: true},
		{Ticker: "NEW", Name: "New ticker", Exchange: "XNAS", IndexGroup: "nasdaq", Active: true},
	}
	if err := repo.ReplaceConstituents(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceConstituents(ctx, replacement); err != nil {
		t.Fatalf("idempotent replacement: %v", err)
	}

	var name, exchange, group string
	var score float64
	var scanned time.Time
	var active bool
	if err := pool.QueryRow(ctx, `SELECT name, exchange, index_group, watch_score, last_scanned, active FROM universe_tickers WHERE ticker = 'KEEP'`).
		Scan(&name, &exchange, &group, &score, &scanned, &active); err != nil {
		t.Fatal(err)
	}
	if name != "New name" || exchange != "XNYS" || group != "nyse" || score != 42.5 || !scanned.Equal(lastScanned) || !active {
		t.Fatalf("retained row = (%q, %q, %q, %v, %v, %v)", name, exchange, group, score, scanned, active)
	}
	if err := pool.QueryRow(ctx, `SELECT name, exchange, index_group, watch_score, last_scanned, active FROM universe_tickers WHERE ticker = 'LOWER'`).
		Scan(&name, &exchange, &group, &score, &scanned, &active); err != nil {
		t.Fatal(err)
	}
	if name != "Authoritative lower" || exchange != "XNYS" || group != "nyse" || score != 63 || !scanned.Equal(lastScanned) || !active {
		t.Fatalf("canonicalized variant row = (%q, %q, %q, %v, %v, %v)", name, exchange, group, score, scanned, active)
	}
	if err := pool.QueryRow(ctx, `SELECT name, exchange, index_group, watch_score, last_scanned, active FROM universe_tickers WHERE ticker = 'MERGE'`).
		Scan(&name, &exchange, &group, &score, &scanned, &active); err != nil {
		t.Fatal(err)
	}
	if name != "Authoritative merge" || exchange != "XNAS" || group != "nasdaq" || score != 90 || !scanned.Equal(laterScan) || !active {
		t.Fatalf("merged variant row = (%q, %q, %q, %v, %v, %v)", name, exchange, group, score, scanned, active)
	}
	var activeVariants int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM universe_tickers
		WHERE ticker IN (' lower ', 'merge', ' MERGE ') AND active`).Scan(&activeVariants); err != nil || activeVariants != 0 {
		t.Fatalf("active duplicate variants = %d, error = %v; want 0", activeVariants, err)
	}
	if err := pool.QueryRow(ctx, `SELECT active FROM universe_tickers WHERE ticker = 'DROP'`).Scan(&active); err != nil || active {
		t.Fatalf("removed active = %v, error = %v; want false", active, err)
	}
	var scannedNew *time.Time
	if err := pool.QueryRow(ctx, `SELECT watch_score, last_scanned, active FROM universe_tickers WHERE ticker = 'NEW'`).Scan(&score, &scannedNew, &active); err != nil {
		t.Fatal(err)
	}
	if score != 0 || scannedNew != nil || !active {
		t.Fatalf("new row = score %v, scanned %v, active %v", score, scannedNew, active)
	}
}

func TestUniverseReplaceConstituentsRejectsEmptyAndRollsBack(t *testing.T) {
	pool, cleanup := newUniverseIntegrationPool(t, t.Context())
	defer cleanup()
	ctx := t.Context()
	repo := NewUniverseRepo(pool)
	if _, err := pool.Exec(ctx, `INSERT INTO universe_tickers (ticker, name, active) VALUES ('KEEP', 'Keep', true)`); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceConstituents(ctx, nil); err == nil {
		t.Fatal("ReplaceConstituents(nil) error = nil")
	}
	if err := repo.ReplaceConstituents(ctx, []universe.TrackedTicker{{Ticker: "BAD", Name: "FAIL", Active: true}}); err == nil {
		t.Fatal("ReplaceConstituents(failing row) error = nil")
	}
	var active bool
	if err := pool.QueryRow(ctx, `SELECT active FROM universe_tickers WHERE ticker = 'KEEP'`).Scan(&active); err != nil || !active {
		t.Fatalf("retained active after failures = %v, error = %v", active, err)
	}
}

func TestUniverseReplaceConstituentsConcurrentIntegration(t *testing.T) {
	pool, cleanup := newUniverseIntegrationPool(t, t.Context())
	defer cleanup()
	ctx := t.Context()
	repo := NewUniverseRepo(pool)
	scanned := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `INSERT INTO universe_tickers
		(ticker, name, exchange, index_group, watch_score, last_scanned, active)
		VALUES (' alpha ', 'Old alpha', 'OLD', 'other', 11, $1, true),
		       ('BETA', 'Old beta', 'OLD', 'other', 12, $1, true),
		       ('GAMMA', 'Old gamma', 'OLD', 'other', 21, $1, true),
		       ('DELTA', 'Old delta', 'OLD', 'other', 22, $1, true)`, scanned); err != nil {
		t.Fatal(err)
	}

	// Hold the same advisory lock until both replacements are waiting, making
	// their overlap deterministic instead of relying on goroutine scheduling.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, replaceConstituentsPreparation[0].query); err != nil {
		t.Fatal(err)
	}

	sets := [][]universe.TrackedTicker{
		{
			{Ticker: "ALPHA", Name: "Set A alpha", Exchange: "XNYS", IndexGroup: "nyse", Active: true},
			{Ticker: "BETA", Name: "Set A beta", Exchange: "XNYS", IndexGroup: "nyse", Active: true},
		},
		{
			{Ticker: "GAMMA", Name: "Set B gamma", Exchange: "XNAS", IndexGroup: "nasdaq", Active: true},
			{Ticker: "DELTA", Name: "Set B delta", Exchange: "XNAS", IndexGroup: "nasdaq", Active: true},
		},
	}
	start := make(chan struct{})
	errs := make(chan error, len(sets))
	var ready sync.WaitGroup
	ready.Add(len(sets))
	for _, replacement := range sets {
		replacement := replacement
		go func() {
			ready.Done()
			<-start
			errs <- repo.ReplaceConstituents(ctx, replacement)
		}()
	}
	ready.Wait()
	close(start)
	waitForAdvisoryLockWaiters(t, ctx, pool, len(sets))
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range sets {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	rows, err := pool.Query(ctx, `SELECT ticker, name, watch_score, last_scanned
		FROM universe_tickers WHERE active ORDER BY ticker`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tickers, names []string
	for rows.Next() {
		var ticker, name string
		var score float64
		var lastScanned time.Time
		if err := rows.Scan(&ticker, &name, &score, &lastScanned); err != nil {
			t.Fatal(err)
		}
		tickers = append(tickers, ticker)
		names = append(names, name)
		wantScore := map[string]float64{"ALPHA": 11, "BETA": 12, "DELTA": 22, "GAMMA": 21}[ticker]
		if score != wantScore || !lastScanned.Equal(scanned) {
			t.Fatalf("%s provenance = (%v, %v), want (%v, %v)", ticker, score, lastScanned, wantScore, scanned)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	setA := slices.Equal(tickers, []string{"ALPHA", "BETA"}) && slices.Equal(names, []string{"Set A alpha", "Set A beta"})
	setB := slices.Equal(tickers, []string{"DELTA", "GAMMA"}) && slices.Equal(names, []string{"Set B delta", "Set B gamma"})
	if !setA && !setB {
		t.Fatalf("active canonical constituents = %v (%v), want exactly complete set A or B", tickers, names)
	}
}

func TestUniverseReplaceConstituentsLockWaitCancellationIntegration(t *testing.T) {
	pool, cleanup := newUniverseIntegrationPool(t, t.Context())
	defer cleanup()
	ctx := t.Context()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, replaceConstituentsPreparation[0].query); err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	err = NewUniverseRepo(pool).ReplaceConstituents(waitCtx, []universe.TrackedTicker{{Ticker: "AFTER", Name: "After"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait error = %v, want context deadline exceeded", err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := NewUniverseRepo(pool).ReplaceConstituents(ctx, []universe.TrackedTicker{{Ticker: "AFTER", Name: "After"}}); err != nil {
		t.Fatalf("replacement after lock rollback: %v", err)
	}
}

func waitForAdvisoryLockWaiters(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE query LIKE '%augr:universe:replace-constituents%'
			  AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d concurrent advisory-lock waiters", want)
}

func newUniverseIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	connString := os.Getenv("DB_URL")
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		t.Skip("skipping universe integration test: DB_URL or DATABASE_URL is not set")
	}
	admin, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatal(err)
	}
	schema := "universe_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	_, err = pool.Exec(ctx, `CREATE TABLE universe_tickers (
		ticker text PRIMARY KEY, name text CHECK (name <> 'FAIL'), exchange text,
		index_group text NOT NULL DEFAULT 'other', watch_score numeric(10,4) NOT NULL DEFAULT 0,
		last_scanned timestamptz, active boolean NOT NULL DEFAULT true,
		created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now())`)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	}
}

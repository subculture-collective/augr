package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
	"github.com/PatrickFanella/get-rich-quick/internal/testsupport"
)

func TestBuildListQuery_NoFilters(t *testing.T) {
	query, args := buildListQuery(repository.StrategyFilter{}, 10, 0)

	// offset=0 is omitted, so only limit is parameterised.
	if len(args) != 1 {
		t.Fatalf("expected 1 arg (limit), got %d", len(args))
	}

	if args[0] != 10 {
		t.Errorf("expected limit=10, got %v", args[0])
	}

	assertContains(t, query, "FROM strategies")
	assertContains(t, query, "LEFT JOIN LATERAL")
	assertContains(t, query, "latest_run_summary.latest_run_summary")
	assertContains(t, query, "FROM strategies s")
	assertContains(t, query, "ORDER BY s.created_at DESC")
	assertContains(t, query, "LIMIT $1")
	assertNotContains(t, query, "OFFSET")
	assertNotContains(t, query, " WHERE s.")
}

func TestResolveExecutionVersionQueryReadsOneJoinedSnapshot(t *testing.T) {
	for _, fragment := range []string{
		"s.execution_strategy_version_id", "v.id", "v.family_id", "s.market_type",
		"strategy_legacy_snapshot_sha(s.id)", "strategy_canonical_json(s.config)",
		"v.sha256", "v.canonical_bytes", "JOIN strategy_versions v",
	} {
		assertContains(t, resolveExecutionVersionSQL, fragment)
	}
}

func TestStrategyReuseKeyIsPostgresTextSafeAndUnambiguous(t *testing.T) {
	first := strategyReuseKey(domain.Strategy{Name: "B", Ticker: "A|1", MarketType: domain.MarketTypeStock})
	second := strategyReuseKey(domain.Strategy{Name: "1|B", Ticker: "A", MarketType: domain.MarketTypeStock})
	if strings.ContainsRune(first, '\x00') || strings.ContainsRune(second, '\x00') {
		t.Fatal("strategy reuse key contains a PostgreSQL text NUL")
	}
	if first == second {
		t.Fatalf("strategy reuse keys collide: %q", first)
	}
}

func TestBuildListQuery_AllFilters(t *testing.T) {
	paper := false

	filter := repository.StrategyFilter{
		Ticker:     "AAPL",
		MarketType: domain.MarketTypeStock,
		Status:     domain.StrategyStatusActive,
		IsPaper:    &paper,
	}

	query, args := buildListQuery(filter, 25, 50)

	// 4 filter args + limit + offset = 6
	if len(args) != 6 {
		t.Fatalf("expected 6 args, got %d: %v", len(args), args)
	}

	assertContains(t, query, "s.ticker = $1")
	assertContains(t, query, "s.market_type = $2")
	assertContains(t, query, "s.status = $3")
	assertContains(t, query, "s.is_paper = $4")
	assertContains(t, query, "LIMIT $5 OFFSET $6")

	if args[0] != "AAPL" {
		t.Errorf("expected ticker arg AAPL, got %v", args[0])
	}

	if args[1] != domain.MarketTypeStock {
		t.Errorf("expected market_type arg stock, got %v", args[1])
	}

	if args[2] != domain.StrategyStatusActive {
		t.Errorf("expected status arg active, got %v", args[2])
	}

	if args[3] != false {
		t.Errorf("expected is_paper arg false, got %v", args[3])
	}
}

func TestBuildListQuery_PartialFilters(t *testing.T) {
	filter := repository.StrategyFilter{
		Ticker: "BTC",
		Status: domain.StrategyStatusActive,
	}

	query, args := buildListQuery(filter, 10, 0)

	// 2 filter args + limit (offset=0 omitted) = 3
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}

	assertContains(t, query, "s.ticker = $1")
	assertNotContains(t, query, "market_type =")
	assertContains(t, query, "s.status = $2")
	assertNotContains(t, query, "is_paper =")
	assertContains(t, query, "LIMIT $3")
	assertNotContains(t, query, "OFFSET")
}

func TestMarshalConfig_ValidJSON(t *testing.T) {
	input := json.RawMessage(`{"lookback":20,"threshold":0.5}`)

	got, err := marshalConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(got) != `{"lookback":20,"threshold":0.5}` {
		t.Errorf("expected config pass-through, got %s", got)
	}
}

func TestMarshalConfig_NilDefaults(t *testing.T) {
	got, err := marshalConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(got) != "{}" {
		t.Errorf("expected default {}, got %s", got)
	}
}

func TestMarshalConfig_EmptyDefaults(t *testing.T) {
	got, err := marshalConfig(json.RawMessage{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(got) != "{}" {
		t.Errorf("expected default {}, got %s", got)
	}
}

func TestMarshalConfig_InvalidJSON(t *testing.T) {
	_, err := marshalConfig(json.RawMessage(`{not valid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLegacyExecutionRequirements(t *testing.T) {
	tests := []struct {
		market domain.MarketType
		asset  instrument.AssetClass
		kinds  []dataset.Kind
	}{
		{domain.MarketTypeStock, instrument.AssetClassEquity, []dataset.Kind{dataset.KindBars}},
		{domain.MarketTypeCrypto, instrument.AssetClassCryptoSpot, []dataset.Kind{dataset.KindBars}},
		{domain.MarketTypeOptions, instrument.AssetClassOption, []dataset.Kind{dataset.KindBars, dataset.KindOptionChains}},
		{domain.MarketTypeKalshi, instrument.AssetClassPredictionContract, []dataset.Kind{dataset.KindPredictionBooks, dataset.KindPredictionRules, dataset.KindResolutions}},
		{domain.MarketTypePolymarket, instrument.AssetClassPredictionContract, []dataset.Kind{dataset.KindPredictionBooks, dataset.KindPredictionRules, dataset.KindResolutions}},
	}
	for _, test := range tests {
		asset, kinds, err := legacyExecutionRequirements(test.market)
		if err != nil || asset != test.asset || strings.Join(datasetKindsStrings(kinds), ",") != strings.Join(datasetKindsStrings(test.kinds), ",") {
			t.Fatalf("requirements(%s) = %s/%v/%v", test.market, asset, kinds, err)
		}
	}
}

func datasetKindsStrings(kinds []dataset.Kind) []string {
	values := make([]string, len(kinds))
	for i, kind := range kinds {
		values[i] = string(kind)
	}
	return values
}

func TestStrategyRepoIntegration_CreateListAndUpdateStatus(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newStrategyIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewStrategyRepo(pool)

	paused := &domain.Strategy{
		ID:           uuid.New(),
		Name:         "Paused Strategy",
		Ticker:       "AAPL",
		MarketType:   domain.MarketTypeStock,
		ScheduleCron: "0 9 * * 1-5",
		Status:       domain.StrategyStatusPaused,
		SkipNextRun:  true,
		IsPaper:      true,
	}
	if _, err := repo.CreateWithExecutionVersion(ctx, paused); err != nil {
		t.Fatalf("Create(paused) error = %v", err)
	}
	if paused.ID == uuid.Nil {
		t.Fatal("expected paused strategy ID to be set")
	}
	if paused.CreatedAt.IsZero() {
		t.Fatal("expected paused strategy CreatedAt to be set")
	}

	storedPaused, err := repo.Get(ctx, paused.ID)
	if err != nil {
		t.Fatalf("Get(paused) error = %v", err)
	}
	if storedPaused.Status != domain.StrategyStatusPaused {
		t.Fatalf("paused strategy status = %q, want %q", storedPaused.Status, domain.StrategyStatusPaused)
	}
	if storedPaused.ScheduleCron != paused.ScheduleCron {
		t.Fatalf("paused strategy schedule_cron = %q, want %q", storedPaused.ScheduleCron, paused.ScheduleCron)
	}
	if !storedPaused.SkipNextRun {
		t.Fatal("paused strategy skip_next_run = false, want true")
	}

	active := &domain.Strategy{
		ID:         uuid.New(),
		Name:       "Active Strategy",
		Ticker:     "MSFT",
		MarketType: domain.MarketTypeStock,
		Status:     domain.StrategyStatusActive,
		IsPaper:    false,
	}
	if _, err := repo.CreateWithExecutionVersion(ctx, active); err != nil {
		t.Fatalf("Create(active) error = %v", err)
	}

	pausedOnly, err := repo.List(ctx, repository.StrategyFilter{Status: domain.StrategyStatusPaused}, 10, 0)
	if err != nil {
		t.Fatalf("List(status=paused) error = %v", err)
	}
	if len(pausedOnly) != 1 {
		t.Fatalf("paused strategy count = %d, want 1", len(pausedOnly))
	}
	if pausedOnly[0].ID != paused.ID {
		t.Fatalf("paused strategy id = %s, want %s", pausedOnly[0].ID, paused.ID)
	}

	count, err := repo.Count(ctx, repository.StrategyFilter{Status: domain.StrategyStatusActive})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	var sqlCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM strategies WHERE status = $1`, domain.StrategyStatusActive).Scan(&sqlCount); err != nil {
		t.Fatalf("direct SQL count error = %v", err)
	}
	if count != sqlCount {
		t.Fatalf("Count() = %d, direct SQL = %d", count, sqlCount)
	}

	paused.Status = domain.StrategyStatusInactive
	paused.ScheduleCron = "0 15 * * 1-5"
	paused.SkipNextRun = false
	if err := repo.Update(ctx, paused); err != nil {
		t.Fatalf("Update(paused) error = %v", err)
	}
	if paused.UpdatedAt.IsZero() {
		t.Fatal("expected paused strategy UpdatedAt to be set")
	}

	updatedPaused, err := repo.Get(ctx, paused.ID)
	if err != nil {
		t.Fatalf("Get(updated paused) error = %v", err)
	}
	if updatedPaused.Status != domain.StrategyStatusInactive {
		t.Fatalf("updated status = %q, want %q", updatedPaused.Status, domain.StrategyStatusInactive)
	}
	if updatedPaused.ScheduleCron != paused.ScheduleCron {
		t.Fatalf("updated schedule_cron = %q, want %q", updatedPaused.ScheduleCron, paused.ScheduleCron)
	}
	if updatedPaused.SkipNextRun {
		t.Fatal("updated skip_next_run = true, want false")
	}
}

func TestStrategyRepoIntegration_DiscoveryDuplicateRejectedByUniqueIndex(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newStrategyIntegrationPool(t, ctx)
	defer cleanup()

	repo := NewStrategyRepo(pool)

	first := &domain.Strategy{
		ID:         uuid.New(),
		Name:       "discovery: PBM RSI Momentum Breakout",
		Ticker:     "PBM",
		MarketType: domain.MarketTypeStock,
		Status:     domain.StrategyStatusActive,
		IsPaper:    true,
	}
	if _, err := repo.CreateWithExecutionVersion(ctx, first); err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}

	duplicate := &domain.Strategy{
		ID:         uuid.New(),
		Name:       "discovery: PBM RSI Momentum Breakout",
		Ticker:     "PBM",
		MarketType: domain.MarketTypeStock,
		Status:     domain.StrategyStatusActive,
		IsPaper:    true,
	}
	_, err := repo.CreateWithExecutionVersion(ctx, duplicate)
	if err == nil {
		t.Fatal("Create(duplicate) error = nil, want unique violation")
	}
	errText := strings.ToLower(err.Error())
	if !strings.Contains(errText, "unique") && !strings.Contains(errText, "duplicate") {
		t.Fatalf("Create(duplicate) error = %v, want unique/duplicate violation", err)
	}
}

func TestStrategyRepoIntegration_ConcurrentEventIdeasUseDatabaseUniqueness(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newStrategyIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewStrategyRepo(pool)
	ticker := "KX-" + uuid.NewString()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := repo.CreateWithExecutionVersion(ctx, &domain.Strategy{
				ID: uuid.New(), Name: "idea", Ticker: ticker, MarketType: domain.MarketTypeKalshi,
				Status: domain.StrategyStatusInactive, IsPaper: true,
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	succeeded, conflicted := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate"):
			conflicted++
		default:
			t.Fatalf("concurrent create error = %v", err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE ticker=$1 AND market_type='kalshi'`, ticker).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || conflicted != 1 || count != 1 {
		t.Fatalf("concurrent creates succeeded=%d conflicted=%d rows=%d", succeeded, conflicted, count)
	}
}

func TestStrategyRepoIntegration_RebindsEverySnapshotMutation(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newStrategyIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewStrategyRepo(pool)
	strategy := &domain.Strategy{ID: uuid.New(), Name: "versioned", Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true}
	first, err := repo.CreateWithExecutionVersion(ctx, strategy)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := repo.TransitionPaperStatus(ctx, strategy.ID, domain.StrategyStatusActive, domain.StrategyStatusPaused)
	if err != nil {
		t.Fatal(err)
	}
	second := *paused.ExecutionStrategyVersionID
	if second == first {
		t.Fatal("status transition retained snapshot version")
	}
	active, err := repo.TransitionPaperStatus(ctx, strategy.ID, domain.StrategyStatusPaused, domain.StrategyStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	third := *active.ExecutionStrategyVersionID
	skipped, err := repo.MarkPaperSkipNext(ctx, strategy.ID)
	if err != nil {
		t.Fatal(err)
	}
	fourth := *skipped.ExecutionStrategyVersionID
	if fourth == third {
		t.Fatal("skip-next mutation retained snapshot version")
	}
	if err := repo.UpdateThesis(ctx, strategy.ID, json.RawMessage(`{"claim":"momentum"}`)); err != nil {
		t.Fatal(err)
	}
	fifth, err := repo.ResolveExecutionVersionID(ctx, strategy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fifth == fourth {
		t.Fatal("thesis mutation retained snapshot version")
	}
	strategy, err = repo.Get(ctx, strategy.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	retry, err := bindExecutionVersion(ctx, tx, strategy)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := bindExecutionVersion(ctx, tx, strategy)
	if err != nil || replayed != retry || retry != fifth {
		t.Fatalf("unchanged retry versions = %s/%s, %v; want %s", retry, replayed, err, fifth)
	}
}

func TestStrategyRepoIntegration_RejectsLegacyFamilyMismatchAndRollsBack(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newStrategyIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewStrategyRepo(pool)
	strategyID := uuid.New()
	family, err := strategycatalog.NewLegacyFamily(strategyID, instrument.AssetClassCryptoSpot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStrategyCatalogRepo(pool).RegisterStrategyFamily(ctx, family); err != nil {
		t.Fatal(err)
	}

	strategy := &domain.Strategy{ID: strategyID, Name: "conflict", Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true}
	if _, err := repo.CreateWithExecutionVersion(ctx, strategy); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("CreateWithExecutionVersion() error = %v, want ErrIdempotencyConflict", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE id=$1`, strategyID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back strategy count = %d, %v", count, err)
	}
}

func TestStrategyRepoIntegration_ResolveRejectsForeignFamilyMapping(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newStrategyIntegrationPool(t, ctx)
	defer cleanup()
	repo := NewStrategyRepo(pool)
	first := &domain.Strategy{ID: uuid.New(), Name: "first", Ticker: "AAPL", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true}
	second := &domain.Strategy{ID: uuid.New(), Name: "second", Ticker: "MSFT", MarketType: domain.MarketTypeStock, Status: domain.StrategyStatusActive, IsPaper: true}
	if _, err := repo.CreateWithExecutionVersion(ctx, first); err != nil {
		t.Fatal(err)
	}
	foreign, err := repo.CreateWithExecutionVersion(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE strategies SET execution_strategy_version_id=$1 WHERE id=$2`, foreign, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveExecutionVersionID(ctx, first.ID); err == nil || !strings.Contains(err.Error(), "family mismatch") {
		t.Fatalf("ResolveExecutionVersionID() error = %v, want family mismatch", err)
	}
}

func TestStrategyIntegrationRelocatableExtensionsRemainUsable(t *testing.T) {
	ctx := context.Background()
	pool, cleanup := newStrategyIntegrationPool(t, ctx)
	defer cleanup()
	var digestLength, dimensions int
	if err := pool.QueryRow(ctx, `SELECT octet_length(digest('fixture','sha256')),vector_dims('[1,2,3]'::vector)`).Scan(&digestLength, &dimensions); err != nil {
		t.Fatal(err)
	}
	if digestLength != 32 || dimensions != 3 {
		t.Fatalf("extension results digest=%d dimensions=%d", digestLength, dimensions)
	}
}

// assertContains fails if substr is not found in s.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected query to contain %q, got:\n%s", substr, s)
	}
}

// assertNotContains fails if substr is found in s.
func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected query NOT to contain %q, got:\n%s", substr, s)
	}
}

func newStrategyIntegrationPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	connString, config := safeStrategyTestDatabase(t)
	adminPool, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatalf("failed to create admin pool: %v", err)
	}
	if err := testsupport.PreparePostgresExtensions(ctx, adminPool); err != nil {
		adminPool.Close()
		t.Fatalf("failed to prepare shared extensions: %v", err)
	}

	schemaName := "integration_strategy_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	identifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, `CREATE SCHEMA `+identifier); err != nil {
		adminPool.Close()
		t.Fatalf("failed to create test schema: %v", err)
	}

	searchPath, err := testsupport.PostgresTestSearchPath(ctx, adminPool, schemaName)
	if err != nil {
		adminPool.Close()
		t.Fatalf("failed to discover extension schemas: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = searchPath

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA "`+schemaName+`" CASCADE`)
		adminPool.Close()
		t.Fatalf("failed to create test pool: %v", err)
	}

	for _, migration := range strategyTestMigrations(t) {
		contents, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			pool.Close()
			_, _ = adminPool.Exec(ctx, `DROP SCHEMA `+identifier+` CASCADE`)
			adminPool.Close()
			t.Fatalf("failed to apply %s: %v", filepath.Base(migration), err)
		}
	}

	cleanup := func() {
		pool.Close()
		_, _ = adminPool.Exec(ctx, `DROP SCHEMA "`+schemaName+`" CASCADE`)
		adminPool.Close()
	}

	return pool, cleanup
}

func safeStrategyTestDatabase(t *testing.T) (string, *pgxpool.Config) {
	t.Helper()
	for _, key := range []string{"TEST_DATABASE_URL", "DB_URL", "DATABASE_URL"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		config, err := pgxpool.ParseConfig(value)
		if err != nil {
			continue
		}
		if strings.EqualFold(config.ConnConfig.Database, "tradingagent") {
			continue
		}
		return value, config
	}
	t.Skip("skipping strategy integration test: no safe disposable DSN; TEST_DATABASE_URL/DB_URL/DATABASE_URL are unset, invalid, or target protected database tradingagent")
	return "", nil
}

func strategyTestMigrations(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve strategy test path")
	}
	dir := filepath.Join(filepath.Dir(filename), "..", "..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") && entry.Name() <= "000108_canonical_account_expansion.up.sql" {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}

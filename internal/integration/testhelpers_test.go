package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	"github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/testsupport"
)

// testDB holds a connection pool scoped to an isolated schema for one test.
type testDB struct {
	Pool      *pgxpool.Pool
	AdminPool *pgxpool.Pool
	Schema    string
}

// repos groups all repository implementations for a single test schema.
type repos struct {
	Strategy      *postgres.StrategyRepo
	PipelineRun   *postgres.PipelineRunRepo
	AgentDecision *postgres.AgentDecisionRepo
	Order         *postgres.OrderRepo
	Position      *postgres.PositionRepo
	Trade         *postgres.TradeRepo
	Memory        *postgres.MemoryRepo
}

// newTestDB creates an isolated PostgreSQL schema for the test.
// It skips when no safe disposable DSN is configured or -short mode is used.
func newTestDB(t *testing.T) *testDB {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	databaseURL, config := safeIntegrationDatabase(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to create admin pool: %v", err)
	}
	if err := testsupport.PreparePostgresExtensions(ctx, adminPool); err != nil {
		adminPool.Close()
		t.Fatalf("failed to prepare shared extensions: %v", err)
	}

	schemaName := "integ_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
		dropSchema(adminPool, schemaName)
		adminPool.Close()
		t.Fatalf("failed to create test pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropSchema(adminPool, schemaName)
		adminPool.Close()
	})

	applyMigrations(t, pool)

	db := &testDB{
		Pool:      pool,
		AdminPool: adminPool,
		Schema:    schemaName,
	}

	return db
}

func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, migration := range integrationTestMigrations(t) {
		contents, err := os.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("failed to apply %s: %v", filepath.Base(migration), err)
		}
	}
}

func safeIntegrationDatabase(t *testing.T) (string, *pgxpool.Config) {
	t.Helper()
	for _, key := range []string{"TEST_DATABASE_URL", "DB_URL", "DATABASE_URL"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		config, err := pgxpool.ParseConfig(value)
		if err != nil {
			t.Fatalf("invalid disposable integration DSN in %s: %v", key, err)
		}
		if strings.EqualFold(config.ConnConfig.Database, "tradingagent") {
			t.Fatalf("refusing integration test against protected database tradingagent from %s", key)
		}
		return value, config
	}
	t.Skip("skipping integration test: TEST_DATABASE_URL, DB_URL, and DATABASE_URL are unset")
	return "", nil
}

func integrationTestMigrations(t *testing.T) []string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	dir := filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}

// newRepos creates all repository implementations for the given test DB.
func newRepos(db *testDB) repos {
	accountID := uuid.MustParse("00000000-0000-4000-8000-000000000064")
	return repos{
		Strategy:      postgres.NewStrategyRepo(db.Pool),
		PipelineRun:   postgres.NewPipelineRunRepo(db.Pool, accountID),
		AgentDecision: postgres.NewAgentDecisionRepo(db.Pool, accountID),
		Order:         postgres.NewOrderRepo(db.Pool, accountID),
		Position:      postgres.NewPositionRepo(db.Pool, accountID),
		Trade:         postgres.NewTradeRepo(db.Pool, accountID),
		Memory:        postgres.NewMemoryRepo(db.Pool, accountID),
	}
}

func dropSchema(pool *pgxpool.Pool, schema string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(ctx, `DROP SCHEMA "`+schema+`" CASCADE`)
}

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

func createStrategy(t *testing.T, ctx context.Context, r *postgres.StrategyRepo, name, ticker string) *domain.Strategy {
	t.Helper()
	s := &domain.Strategy{
		ID:         uuid.New(),
		Name:       name,
		Ticker:     ticker,
		MarketType: domain.MarketTypeStock,
		Status:     domain.StrategyStatusActive,
		IsPaper:    true,
	}
	if _, err := r.CreateWithExecutionVersion(ctx, s); err != nil {
		t.Fatalf("failed to create strategy: %v", err)
	}
	return s
}

func createPipelineRun(t *testing.T, ctx context.Context, r *postgres.PipelineRunRepo, strategy *domain.Strategy, ticker string, tradeDate time.Time) *domain.PipelineRun {
	t.Helper()
	run := &domain.PipelineRun{
		StrategyID:  strategy.ID,
		Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: strategy.ExecutionStrategyVersionID.String(),
		Ticker:    ticker,
		TradeDate: tradeDate,
		Status:    domain.PipelineStatusRunning,
		StartedAt: tradeDate.Add(9*time.Hour + 30*time.Minute),
	}
	if err := r.Create(ctx, run); err != nil {
		t.Fatalf("failed to create pipeline run: %v", err)
	}
	return run
}

func createPosition(t *testing.T, ctx context.Context, r *postgres.PositionRepo, strategy *domain.Strategy, ticker string, side domain.PositionSide, quantity, avgEntry float64) *domain.Position {
	t.Helper()
	pos := &domain.Position{
		StrategyID:  &strategy.ID,
		Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: strategy.ExecutionStrategyVersionID.String(),
		AssetClass: domain.AssetClassEquity, ContractMultiplier: 1,
		Ticker:   ticker,
		Side:     side,
		Quantity: quantity,
		AvgEntry: avgEntry,
	}
	if err := r.Create(ctx, pos); err != nil {
		t.Fatalf("failed to create position: %v", err)
	}
	return pos
}

func createOrder(t *testing.T, ctx context.Context, r *postgres.OrderRepo, strategy *domain.Strategy, run *domain.PipelineRun, ticker string, side domain.OrderSide, orderType domain.OrderType, qty float64) *domain.Order {
	t.Helper()
	order := &domain.Order{
		StrategyID:  &strategy.ID,
		Environment: domain.AccountEnvironmentPaperScored, OriginType: "strategy_version", OriginID: strategy.ExecutionStrategyVersionID.String(),
		AssetClass: domain.AssetClassEquity, MarketType: domain.MarketTypeStock,
		Ticker:    ticker,
		Side:      side,
		OrderType: orderType,
		Quantity:  qty,
		Status:    domain.OrderStatusPending,
		Broker:    "alpaca",
	}
	if run != nil {
		order.PipelineRunID, order.PipelineRunTradeDate = &run.ID, &run.TradeDate
	}
	if err := r.Create(ctx, order); err != nil {
		t.Fatalf("failed to create order: %v", err)
	}
	return order
}

// ---------------------------------------------------------------------------
// Mock LLM provider
// ---------------------------------------------------------------------------

type mockLLMProvider struct {
	response *llm.CompletionResponse
	err      error
	calls    int
}

func (m *mockLLMProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	m.calls++
	return m.response, m.err
}

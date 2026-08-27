package migrations_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/testsupport"
)

func TestEmbeddingsUpMigrationDefinesExpectedSchema(t *testing.T) {
	upSQL := normalizeSQL(t, readMigrationFile(t, "000030_embeddings.up.sql"))

	for _, fragment := range []string{
		"create extension if not exists vector",
		"alter table news_feed add column embedding vector(768)",
		"alter table social_sentiment add column embedding vector(768)",
		"alter table social_sentiment add column post_summaries jsonb",
		"create index idx_news_feed_embedding",
		"on news_feed using hnsw (embedding vector_cosine_ops)",
		"create index idx_social_sentiment_embedding",
		"on social_sentiment using hnsw (embedding vector_cosine_ops)",
	} {
		if !strings.Contains(upSQL, fragment) {
			t.Fatalf("expected up migration to contain %q, got:\n%s", fragment, upSQL)
		}
	}
}

func TestEmbeddingsDownMigrationDropsColumnsAndExtension(t *testing.T) {
	downSQL := normalizeSQL(t, readMigrationFile(t, "000030_embeddings.down.sql"))

	for _, fragment := range []string{
		"drop index if exists idx_social_sentiment_embedding",
		"drop index if exists idx_news_feed_embedding",
		"alter table social_sentiment drop column if exists post_summaries",
		"alter table social_sentiment drop column if exists embedding",
		"alter table news_feed drop column if exists embedding",
		"drop extension if exists vector",
	} {
		if !strings.Contains(downSQL, fragment) {
			t.Fatalf("expected down migration to contain %q, got:\n%s", fragment, downSQL)
		}
	}
}

func TestEmbeddingsMigrationAppliesAgainstExistingSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration integration test in short mode")
	}

	databaseURL := os.Getenv("DB_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("skipping migration integration test: DB_URL or DATABASE_URL is not set")
	}

	ctx := context.Background()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("failed to parse database config: %v", err)
	}
	if config.ConnConfig.Database == "tradingagent" {
		t.Fatal("refusing migration test against protected database tradingagent")
	}
	adminPool, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatalf("failed to create admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	if err := testsupport.PreparePostgresExtensions(ctx, adminPool); err != nil {
		t.Fatalf("failed to prepare shared extensions: %v", err)
	}

	schemaName := "migr_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	sanitizedSchemaName := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, `CREATE SCHEMA `+sanitizedSchemaName); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminPool.Exec(ctx, `DROP SCHEMA IF EXISTS `+sanitizedSchemaName+` CASCADE`); err != nil {
			t.Errorf("failed to drop schema %q: %v", schemaName, err)
		}
	})

	config.ConnConfig.RuntimeParams["search_path"] = migrationTestSearchPath(t, ctx, databaseURL, schemaName)
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("failed to create schema-scoped pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Apply all migrations through 000030.
	for _, filename := range sortedUpMigrationsThrough(t, "000030_embeddings.up.sql") {
		if _, err := pool.Exec(ctx, readMigrationFile(t, filename)); err != nil {
			t.Fatalf("failed to apply %s: %v", filename, err)
		}
	}

	// Verify news_feed.embedding column exists with type vector.
	var newsEmbeddingType string
	err = pool.QueryRow(ctx,
		`SELECT udt_name FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = 'news_feed' AND column_name = 'embedding'`,
		schemaName).Scan(&newsEmbeddingType)
	if err != nil {
		t.Fatalf("news_feed.embedding column not found: %v", err)
	}
	if newsEmbeddingType != "vector" {
		t.Fatalf("expected news_feed.embedding type 'vector', got %q", newsEmbeddingType)
	}

	// Verify social_sentiment.embedding column exists.
	var socialEmbeddingType string
	err = pool.QueryRow(ctx,
		`SELECT udt_name FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = 'social_sentiment' AND column_name = 'embedding'`,
		schemaName).Scan(&socialEmbeddingType)
	if err != nil {
		t.Fatalf("social_sentiment.embedding column not found: %v", err)
	}

	// Verify social_sentiment.post_summaries column exists.
	var postSummariesType string
	err = pool.QueryRow(ctx,
		`SELECT udt_name FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = 'social_sentiment' AND column_name = 'post_summaries'`,
		schemaName).Scan(&postSummariesType)
	if err != nil {
		t.Fatalf("social_sentiment.post_summaries column not found: %v", err)
	}
	if postSummariesType != "jsonb" {
		t.Fatalf("expected post_summaries type 'jsonb', got %q", postSummariesType)
	}

	// Verify down migration works: apply it and confirm columns are gone.
	downSQL := readMigrationFile(t, "000030_embeddings.down.sql")
	downSQL = strings.ReplaceAll(downSQL, "DROP EXTENSION IF EXISTS vector;", "")
	if _, err := pool.Exec(ctx, downSQL); err != nil {
		t.Fatalf("failed to apply down migration: %v", err)
	}

	var colCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = 'news_feed' AND column_name = 'embedding'`,
		schemaName).Scan(&colCount)
	if err != nil {
		t.Fatalf("query after down migration: %v", err)
	}
	if colCount != 0 {
		t.Fatalf("expected news_feed.embedding to be dropped, but still found %d", colCount)
	}
	var dimensions int
	if err := pool.QueryRow(ctx, `SELECT vector_dims('[1,2,3]'::vector)`).Scan(&dimensions); err != nil {
		t.Fatal(err)
	}
	if dimensions != 3 {
		t.Fatalf("vector dimensions = %d, want 3", dimensions)
	}
}

func TestConcurrentEmbeddingFixturesKeepSharedExtensionAfterDown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration integration test in short mode")
	}
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DB_URL")
	}
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("skipping migration integration test: database URL is not set")
	}
	ctx := context.Background()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if adminConfig.ConnConfig.Database == "tradingagent" {
		t.Fatal("refusing migration test against protected database tradingagent")
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	installedSchemas := make(map[string]string)
	rows, err := admin.Query(ctx, `SELECT e.extname,n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname IN ('pgcrypto','vector')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, schema string
		if err := rows.Scan(&name, &schema); err != nil {
			t.Fatal(err)
		}
		installedSchemas[name] = schema
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	type fixture struct {
		schema string
		pool   *pgxpool.Pool
	}
	fixtures := make([]fixture, 2)
	for i := range fixtures {
		fixtures[i].schema = "migr_ext_concurrent_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		identifier := pgx.Identifier{fixtures[i].schema}.Sanitize()
		if _, err := admin.Exec(ctx, `CREATE SCHEMA `+identifier); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+identifier+` CASCADE`) })
		config := adminConfig.Copy()
		searchPath, pathErr := testsupport.PostgresTestSearchPath(ctx, admin, fixtures[i].schema)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		config.ConnConfig.RuntimeParams["search_path"] = searchPath
		config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		fixtures[i].pool, err = pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(fixtures[i].pool.Close)
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(fixtures))
	for i := range fixtures {
		wg.Add(1)
		go func(f fixture) {
			defer wg.Done()
			if err := testsupport.PreparePostgresExtensions(ctx, f.pool); err != nil {
				errs <- err
				return
			}
			_, err := f.pool.Exec(ctx, `CREATE TABLE news_feed(id bigint); CREATE TABLE social_sentiment(id bigint); `+readMigrationFile(t, "000030_embeddings.up.sql"))
			errs <- err
		}(fixtures[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent fixture setup: %v", err)
		}
	}
	for name, schema := range installedSchemas {
		var current string
		if err := admin.QueryRow(ctx, `SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname=$1`, name).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if current != schema {
			t.Fatalf("concurrent fixture setup moved %s extension from %q to %q", name, schema, current)
		}
	}

	downSQL := strings.ReplaceAll(readMigrationFile(t, "000030_embeddings.down.sql"), "DROP EXTENSION IF EXISTS vector;", "")
	if _, err := fixtures[0].pool.Exec(ctx, downSQL); err != nil {
		t.Fatalf("first fixture down: %v", err)
	}
	var dimensions int
	if err := fixtures[1].pool.QueryRow(ctx, `SELECT vector_dims('[1,2,3]'::vector)`).Scan(&dimensions); err != nil {
		t.Fatalf("second fixture lost vector after first down: %v", err)
	}
	if dimensions != 3 {
		t.Fatalf("second fixture vector dimensions = %d, want 3", dimensions)
	}
}

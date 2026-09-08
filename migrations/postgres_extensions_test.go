package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/testsupport"
)

func prepareMigrationTestExtensions(ctx context.Context, pool *pgxpool.Pool) error {
	return testsupport.PreparePostgresExtensions(ctx, pool)
}

func migrationTestSearchPath(t *testing.T, ctx context.Context, databaseURL, schema string) string {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.Database == "tradingagent" {
		t.Fatal("refusing migration test against protected database tradingagent")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := testsupport.PreparePostgresExtensions(ctx, pool); err != nil {
		t.Fatal(err)
	}
	searchPath, err := testsupport.PostgresTestSearchPath(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	return searchPath
}

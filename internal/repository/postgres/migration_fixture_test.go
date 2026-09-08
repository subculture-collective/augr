package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func execRepositoryMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) (pgconn.CommandTag, error) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS test_fixture_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return pgconn.CommandTag{}, err
	}
	if strings.HasSuffix(name, ".up.sql") {
		var applied bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM test_fixture_migrations WHERE name=$1)`, name).Scan(&applied); err != nil {
			return pgconn.CommandTag{}, err
		}
		if applied {
			return pgconn.CommandTag{}, nil
		}
	}
	tag, err := pool.Exec(ctx, repositoryMigrationSQL(t, name))
	if err != nil {
		return tag, err
	}
	if strings.HasSuffix(name, ".up.sql") {
		_, err = pool.Exec(ctx, `INSERT INTO test_fixture_migrations(name) VALUES($1)`, name)
	} else {
		_, err = pool.Exec(ctx, `DELETE FROM test_fixture_migrations WHERE name=$1`, strings.TrimSuffix(name, ".down.sql")+".up.sql")
	}
	return tag, err
}

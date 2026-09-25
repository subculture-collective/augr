package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	MinimumSupportedSchemaVersion = 118
	MaximumSupportedSchemaVersion = 118
	RequiredSchemaVersion         = MaximumSupportedSchemaVersion
)

type SchemaVersionState string

const (
	schemaVersionBehind SchemaVersionState = "behind"
	schemaVersionMatch  SchemaVersionState = "match"
	schemaVersionAhead  SchemaVersionState = "ahead"
)

type schemaVersionRow interface {
	Scan(dest ...any) error
}

type schemaVersionQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) schemaVersionRow
}

type poolSchemaVersionQuerier struct {
	pool *pgxpool.Pool
}

func (q poolSchemaVersionQuerier) QueryRow(ctx context.Context, sql string, args ...any) schemaVersionRow {
	return q.pool.QueryRow(ctx, sql, args...)
}

// ErrSchemaMigrationDirty reports that the last migration did not finish.
// Startup must fail so an operator repairs the schema before the runtime
// touches partially migrated tables.
var ErrSchemaMigrationDirty = errors.New("postgres: schema_migrations is dirty: the last migration did not complete; repair the schema (golang-migrate `force`) before starting")

// CurrentSchemaVersion returns the latest applied schema version recorded in
// schema_migrations. It fails with ErrSchemaMigrationDirty when the migration
// tool left the dirty flag set.
func CurrentSchemaVersion(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	return currentSchemaVersion(ctx, poolSchemaVersionQuerier{pool: pool})
}

func currentSchemaVersion(ctx context.Context, querier schemaVersionQuerier) (int, error) {
	var version int
	var dirty bool
	if err := querier.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version, &dirty); err != nil {
		if err == pgx.ErrNoRows {
			return 0, fmt.Errorf("postgres: read schema version: schema_migrations is empty: %w", err)
		}
		return 0, fmt.Errorf("postgres: read schema version: %w", err)
	}
	if dirty {
		return version, fmt.Errorf("%w (version %d)", ErrSchemaMigrationDirty, version)
	}

	return version, nil
}

// CompareSchemaVersion compares current against required.
func CompareSchemaVersion(current, required int) SchemaVersionState {
	switch {
	case current < required:
		return schemaVersionBehind
	case current > required:
		return schemaVersionAhead
	default:
		return schemaVersionMatch
	}
}

func IsSchemaVersionCompatible(current int) bool {
	return current >= MinimumSupportedSchemaVersion && current <= MaximumSupportedSchemaVersion
}

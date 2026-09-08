package testsupport

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ExtensionSchema = "augr_test_extensions"

func PreparePostgresExtensions(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire extension setup connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('augr_test_extensions_setup'))`); err != nil {
		return fmt.Errorf("lock extension setup: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('augr_test_extensions_setup'))`)
	}()

	schema := pgx.Identifier{ExtensionSchema}.Sanitize()
	if _, err := conn.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		return fmt.Errorf("create shared extension schema: %w", err)
	}
	for _, extension := range []struct {
		name        string
		relocatable bool
	}{
		{name: "pgcrypto", relocatable: true},
		{name: "vector", relocatable: true},
		{name: "timescaledb", relocatable: false},
	} {
		var installed bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname=$1)`, extension.name).Scan(&installed); err != nil {
			return fmt.Errorf("inspect %s extension: %w", extension.name, err)
		}
		if !installed {
			statement := `CREATE EXTENSION ` + pgx.Identifier{extension.name}.Sanitize()
			if extension.relocatable {
				statement += ` WITH SCHEMA ` + schema
			}
			if _, err := conn.Exec(ctx, statement); err != nil {
				return fmt.Errorf("install %s extension: %w", extension.name, err)
			}
		}
	}
	return nil
}

func PostgresTestSearchPath(ctx context.Context, pool *pgxpool.Pool, schema string) (string, error) {
	rows, err := pool.Query(ctx, `SELECT n.nspname
		FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace
		WHERE e.extname IN ('pgcrypto','vector') ORDER BY e.extname`)
	if err != nil {
		return "", fmt.Errorf("discover extension schemas: %w", err)
	}
	defer rows.Close()
	extensionSchemas := make([]string, 0, 2)
	for rows.Next() {
		var extensionSchema string
		if err := rows.Scan(&extensionSchema); err != nil {
			return "", fmt.Errorf("scan extension schema: %w", err)
		}
		extensionSchemas = append(extensionSchemas, extensionSchema)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("discover extension schemas rows: %w", err)
	}
	return formatPostgresTestSearchPath(schema, extensionSchemas), nil
}

func formatPostgresTestSearchPath(schema string, extensionSchemas []string) string {
	schemas := append([]string{schema, ExtensionSchema}, extensionSchemas...)
	schemas = append(schemas, "public")
	seen := make(map[string]struct{}, len(schemas))
	quoted := make([]string, 0, len(schemas))
	for _, name := range schemas {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		quoted = append(quoted, pgx.Identifier{name}.Sanitize())
	}
	return strings.Join(quoted, ",")
}

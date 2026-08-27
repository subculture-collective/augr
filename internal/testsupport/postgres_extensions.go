package testsupport

import (
	"context"
	"errors"
	"fmt"

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
		var installedSchema string
		var installedRelocatable bool
		err := conn.QueryRow(ctx, `SELECT n.nspname,e.extrelocatable FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname=$1`, extension.name).Scan(&installedSchema, &installedRelocatable)
		if errors.Is(err, pgx.ErrNoRows) {
			statement := `CREATE EXTENSION ` + pgx.Identifier{extension.name}.Sanitize()
			if extension.relocatable {
				statement += ` WITH SCHEMA ` + schema
			}
			if _, err := conn.Exec(ctx, statement); err != nil {
				return fmt.Errorf("install %s extension: %w", extension.name, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s extension: %w", extension.name, err)
		}
		if extension.relocatable && installedRelocatable && installedSchema != ExtensionSchema {
			if _, err := conn.Exec(ctx, `ALTER EXTENSION `+pgx.Identifier{extension.name}.Sanitize()+` SET SCHEMA `+schema); err != nil {
				return fmt.Errorf("move %s extension to shared schema: %w", extension.name, err)
			}
		}
	}
	return nil
}

func PostgresTestSearchPath(schema string) string {
	return pgx.Identifier{schema}.Sanitize() + "," + pgx.Identifier{ExtensionSchema}.Sanitize() + ",public"
}

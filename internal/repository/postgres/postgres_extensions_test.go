package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/testsupport"
)

func preparePostgresTestExtensions(ctx context.Context, pool *pgxpool.Pool) error {
	return testsupport.PreparePostgresExtensions(ctx, pool)
}

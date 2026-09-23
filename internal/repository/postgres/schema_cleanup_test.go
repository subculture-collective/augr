package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Timescale retention workers can deadlock with DROP SCHEMA CASCADE. PostgreSQL
// rolls back the failed statement, so only a deadlock is safe to retry here.
// Keep this policy in disposable test teardown, never in production writes.
func retrySchemaCleanup(ctx context.Context, drop func() error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := drop()
		var databaseError *pgconn.PgError
		if err == nil || attempt == 4 || !errors.As(err, &databaseError) || databaseError.Code != "40P01" {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func TestSchemaCleanupRetriesOnlyDeadlockAndRetainsFailures(t *testing.T) {
	deadlock := &pgconn.PgError{Code: "40P01"}
	for _, tc := range []struct {
		name   string
		errors []error
		calls  int
		want   error
	}{
		{"success", []error{nil}, 1, nil},
		{"deadlock then success", []error{deadlock, nil}, 2, nil},
		{"bounded deadlock", []error{deadlock}, 5, deadlock},
		{"other error", []error{context.DeadlineExceeded}, 1, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retrySchemaCleanup(context.Background(), func() error {
				index := calls
				calls++
				if index >= len(tc.errors) {
					index = len(tc.errors) - 1
				}
				return tc.errors[index]
			})
			if calls != tc.calls || !errors.Is(err, tc.want) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := retrySchemaCleanup(ctx, func() error { t.Fatal("called after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

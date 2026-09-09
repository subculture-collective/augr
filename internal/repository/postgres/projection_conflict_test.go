package postgres

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestProjectionCheckpointIdentityConflictClassification(t *testing.T) {
	for _, tc := range []struct {
		name       string
		code       string
		constraint string
		want       bool
	}{
		{"checkpoint primary key", "23505", "projection_checkpoints_pkey", true},
		{"other unique constraint", "23505", "other_pkey", false},
		{"other error", "23514", "projection_checkpoints_pkey", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: tc.code, ConstraintName: tc.constraint})
			if got := isProjectionCheckpointIdentityConflict(err); got != tc.want {
				t.Fatalf("identity conflict = %v, want %v", got, tc.want)
			}
		})
	}
	if isProjectionCheckpointIdentityConflict(nil) {
		t.Fatal("nil is not an identity conflict")
	}
}

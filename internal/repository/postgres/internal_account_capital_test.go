package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

func TestInternalAccountCapitalUsesAttestedProjectionWithoutExperiment(t *testing.T) {
	ctx := context.Background()
	pools := newProjectionIntegrationPool(t, ctx)
	accountID := uuid.MustParse("00000000-0000-4000-8000-000000000064")
	source, err := NewCanonicalExperimentCapitalStateSource(pools.owner, pools.attestor, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.LoadInternalAccountCapitalState(ctx, accountID); err == nil {
		t.Fatal("missing projection was accepted")
	}
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	frontier, err := NewProjectionOutboxRepository(pools.owner).LatestProjectionFrontier(ctx, accountID, asOf)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjectionRepo(pools.writer, pools.attestor).RebuildPortfolioProjection(ctx, ledger.ProjectionRequest{
		AccountID: accountID, ThroughTransactionID: frontier, AsOf: asOf,
		MarkSource: "kalshi", MarkNamespace: "internal-capital-test", MaxMarkAge: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := source.LoadInternalAccountCapitalState(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ProjectionCheckpointID() != projection.CheckpointID || !state.Equity().Equal(projection.Totals.Equity) {
		t.Fatal("capital did not derive from the exact checkpoint")
	}
	wrong := pools.attestor
	wrong.Secret = make([]byte, 32)
	wrongSource, err := NewCanonicalExperimentCapitalStateSource(pools.owner, wrong, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongSource.LoadInternalAccountCapitalState(ctx, accountID); err == nil {
		t.Fatal("wrong attestation key accepted")
	}
	staleSource, err := NewCanonicalExperimentCapitalStateSource(pools.owner, pools.attestor, time.Microsecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleSource.LoadInternalAccountCapitalState(ctx, accountID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("stale checkpoint must be unavailable: %v", err)
	}
	if _, err := source.LoadInternalAccountCapitalState(ctx, uuid.New()); err == nil {
		t.Fatal("missing account accepted")
	}
}

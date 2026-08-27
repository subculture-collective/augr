package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestProjectionReaderIgnoresWrongProviderReconciliation(t *testing.T) {
	fixture := newVenueReconFixture(t)
	if _, err := fixture.repo.RegisterVenueReconciliationPolicy(fixture.ctx, fixture.policy); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.RecordVenueProviderSnapshot(fixture.ctx, fixture.provider, fixture.created); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.RecordVenueLocalSnapshot(fixture.ctx, fixture.local, fixture.created); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.RecordVenueReconciliationRun(fixture.ctx, fixture.run, fixture.created); err != nil {
		t.Fatal(err)
	}

	binding, err := domain.NewExecutionAccountBinding(fixture.local.AccountID(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewProjectionReader(binding, fixture.pool).GetLatestPortfolioProjection(
		fixture.ctx,
		fixture.local.AccountID(),
		fixture.created,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReconciliationAvailable {
		t.Fatal("reconciliation for a provider other than accounts.venue was available")
	}
}

func TestProjectionReaderRejectsForeignAccountBeforeDatabaseAccess(t *testing.T) {
	binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	reader := NewProjectionReader(binding, nil)
	if reader.executionAccount != binding {
		t.Fatal("projection reader did not retain execution account")
	}
	foreignAccountID := uuid.New()

	if _, err := reader.GetLatestPortfolioProjection(context.Background(), foreignAccountID, time.Now()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetLatestPortfolioProjection() error = %v, want ErrNotFound", err)
	}
	if _, err := reader.GetCutoverEvidenceInventory(context.Background(), foreignAccountID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetCutoverEvidenceInventory() error = %v, want ErrNotFound", err)
	}
}

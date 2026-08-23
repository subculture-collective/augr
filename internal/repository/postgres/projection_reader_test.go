package postgres

import "testing"

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

	snapshot, err := NewProjectionReader(fixture.pool).GetLatestPortfolioProjection(
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

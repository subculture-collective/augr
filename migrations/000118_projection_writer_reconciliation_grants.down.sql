-- Returns the projection writer to read-only ledger access. Existing
-- reconciliation rows are append-only evidence and are retained.
DO $projection_reconciliation_privileges$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='augr_projection_writer') THEN
        REVOKE SELECT, INSERT ON
            venue_reconciliation_policy_artifacts,
            venue_provider_snapshots, venue_provider_snapshot_pages,
            venue_provider_snapshot_positions, venue_provider_snapshot_fills,
            venue_local_snapshots, venue_local_snapshot_transactions,
            venue_local_snapshot_positions, venue_local_snapshot_fills,
            venue_local_snapshot_issues,
            venue_reconciliation_runs, venue_reconciliation_results,
            venue_reconciliation_incidents
            FROM augr_projection_writer;
    END IF;
END;
$projection_reconciliation_privileges$;

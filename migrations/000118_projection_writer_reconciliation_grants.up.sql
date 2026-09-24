-- The portfolio projection refresh job records internal-ledger reconciliation
-- evidence through the projection-writer pool (see
-- internal/automation/jobs_projection_refresh.go). That role only held SELECT
-- on the ledger tables, so every refresh logged
-- "permission denied for table venue_reconciliation_policy_artifacts" and
-- promotion readiness never saw a fresh reconciliation. The tables are
-- append-only (BEFORE UPDATE OR DELETE triggers from 000075), so INSERT plus
-- SELECT is the full write surface the recorder needs.
DO $projection_reconciliation_privileges$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='augr_projection_writer') THEN
        GRANT SELECT, INSERT ON
            venue_reconciliation_policy_artifacts,
            venue_provider_snapshots, venue_provider_snapshot_pages,
            venue_provider_snapshot_positions, venue_provider_snapshot_fills,
            venue_local_snapshots, venue_local_snapshot_transactions,
            venue_local_snapshot_positions, venue_local_snapshot_fills,
            venue_local_snapshot_issues,
            venue_reconciliation_runs, venue_reconciliation_results,
            venue_reconciliation_incidents
            TO augr_projection_writer;
    END IF;
END;
$projection_reconciliation_privileges$;

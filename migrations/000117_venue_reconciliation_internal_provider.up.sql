-- Allow the canonical internal paper ledger account to record venue
-- reconciliation evidence. The internal venue has no broker; its runs compare
-- a projection checkpoint against a self-capture of the same checkpoint (see
-- internal/venuerecon/internal_ledger.go). The reviewed policy artifact, the
-- deterministic identities, and the append-only triggers are unchanged; only
-- the provider vocabulary widens. Alpaca and Kalshi keep requiring a real
-- provider capture.
DO $$ DECLARE table_name TEXT; BEGIN
    FOREACH table_name IN ARRAY ARRAY[
      'venue_provider_snapshots','venue_provider_snapshot_pages','venue_provider_snapshot_positions','venue_provider_snapshot_fills',
      'venue_local_snapshots','venue_local_snapshot_transactions','venue_local_snapshot_positions','venue_local_snapshot_fills','venue_local_snapshot_issues'
    ] LOOP
      EXECUTE format('ALTER TABLE %I DROP CONSTRAINT IF EXISTS %I', table_name, table_name || '_provider_check');
      EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (provider IN (''alpaca'',''kalshi'',''internal''))', table_name, table_name || '_provider_check');
    END LOOP;
END $$;

-- Restores the alpaca/kalshi-only provider vocabulary. This fails while
-- internal-ledger reconciliation rows exist; they are append-only evidence and
-- must be retained, so downgrade only on a database without internal rows.
DO $$ DECLARE table_name TEXT; BEGIN
    FOREACH table_name IN ARRAY ARRAY[
      'venue_provider_snapshots','venue_provider_snapshot_pages','venue_provider_snapshot_positions','venue_provider_snapshot_fills',
      'venue_local_snapshots','venue_local_snapshot_transactions','venue_local_snapshot_positions','venue_local_snapshot_fills','venue_local_snapshot_issues'
    ] LOOP
      EXECUTE format('ALTER TABLE %I DROP CONSTRAINT IF EXISTS %I', table_name, table_name || '_provider_check');
      EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (provider IN (''alpaca'',''kalshi''))', table_name, table_name || '_provider_check');
    END LOOP;
END $$;

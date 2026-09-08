package migrations_test

import (
	"strings"
	"testing"
)

func TestImmutableMarketPayloadMigrationContract(t *testing.T) {
	up := normalizeSQL(t, readMigrationFile(t, "000110_immutable_market_payloads.up.sql"))
	down := normalizeSQL(t, readMigrationFile(t, "000110_immutable_market_payloads.down.sql"))
	for _, fragment := range []string{
		"lock table instruments,dataset_manifests,dataset_manifest_partitions,dataset_manifest_observations in share row exclusive mode",
		"create table dataset_market_payloads",
		"create table dataset_manifest_payload_bindings",
		"content_sha256=encode(digest(canonical_bytes,'sha256'),'hex')",
		"economic_deterministic_uuid('dataset-market-payload'",
		"create function validate_dataset_payload_binding",
		"dataset payload binding does not reconstruct",
		"create function reject_dataset_payload_mutation",
		"deferrable initially deferred",
		"grant select on dataset_market_payloads,dataset_manifest_payload_bindings to augr_app_runtime",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"historical_ohlcv", "current_manifest", "grant insert", "grant update", "grant delete", "submit_order"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration contains forbidden activation %q", forbidden)
		}
	}
	for _, fragment := range []string{
		"in access exclusive mode",
		"cannot roll back migration 110 while immutable market payload evidence exists",
		"drop table dataset_manifest_payload_bindings",
		"drop table dataset_market_payloads",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration missing %q", fragment)
		}
	}
}

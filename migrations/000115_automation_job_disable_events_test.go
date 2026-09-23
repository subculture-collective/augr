package migrations_test

import (
	"strings"
	"testing"
)

func TestAutomationJobDisableEventsMigrationAddsAutoDisableColumns(t *testing.T) {
	upSQL := normalizeSQL(t, readMigrationFile(t, "000115_automation_job_disable_events.up.sql"))
	for _, fragment := range []string{
		"alter table automation_job_controls",
		"add column if not exists reason text",
		"add column if not exists auto_disabled_until timestamptz",
	} {
		if !strings.Contains(upSQL, fragment) {
			t.Fatalf("expected up migration to contain %q, got:\n%s", fragment, upSQL)
		}
	}

	downSQL := normalizeSQL(t, readMigrationFile(t, "000115_automation_job_disable_events.down.sql"))
	for _, fragment := range []string{
		"drop column if exists auto_disabled_until",
		"drop column if exists reason",
	} {
		if !strings.Contains(downSQL, fragment) {
			t.Fatalf("expected down migration to contain %q, got:\n%s", fragment, downSQL)
		}
	}
}

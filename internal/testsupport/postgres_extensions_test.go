package testsupport

import "testing"

func TestFormatPostgresTestSearchPathQuotesAndDeduplicatesSchemas(t *testing.T) {
	got := formatPostgresTestSearchPath(`test"schema`, []string{`ext"schema`, "public", `ext"schema`})
	want := `"test""schema","augr_test_extensions","ext""schema","public"`
	if got != want {
		t.Fatalf("formatPostgresTestSearchPath() = %q, want %q", got, want)
	}
}

func TestFormatPostgresTestSearchPathIncludesSharedSchemaBeforeExtensionsExist(t *testing.T) {
	got := formatPostgresTestSearchPath("fixture", nil)
	want := `"fixture","augr_test_extensions","public"`
	if got != want {
		t.Fatalf("formatPostgresTestSearchPath() = %q, want %q", got, want)
	}
}

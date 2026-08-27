package testsupport

import "testing"

func TestFormatPostgresTestSearchPathQuotesAndDeduplicatesSchemas(t *testing.T) {
	got := formatPostgresTestSearchPath(`test"schema`, []string{`ext"schema`, "public", `ext"schema`})
	want := `"test""schema","ext""schema","public"`
	if got != want {
		t.Fatalf("formatPostgresTestSearchPath() = %q, want %q", got, want)
	}
}

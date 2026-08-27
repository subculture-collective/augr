package testsupport

import "testing"

func TestPostgresTestSearchPathKeepsSharedExtensionsOutsideTestSchema(t *testing.T) {
	got := PostgresTestSearchPath(`test"schema`)
	want := `"test""schema","augr_test_extensions",public`
	if got != want {
		t.Fatalf("PostgresTestSearchPath() = %q, want %q", got, want)
	}
}

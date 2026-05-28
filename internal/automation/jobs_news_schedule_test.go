package automation

import "testing"

func TestNewsScanSpec_StaggersAndReducesCadenceToAvoidOverlap(t *testing.T) {
	t.Parallel()

	const want = "7-59/10 * * * 1-5"
	if got := newsScanSpec.Cron; got != want {
		t.Fatalf("newsScanSpec.Cron = %q, want %q", got, want)
	}
}

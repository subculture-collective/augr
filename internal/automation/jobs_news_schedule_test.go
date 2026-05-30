package automation

import "testing"

func TestNewsScanSpec_ReducesCadenceToThirtyMinutesWhenTwentyMinuteScheduleStillOverlaps(t *testing.T) {
	t.Parallel()

	const want = "7-59/30 * * * 1-5"
	if got := newsScanSpec.Cron; got != want {
		t.Fatalf("newsScanSpec.Cron = %q, want %q", got, want)
	}
}

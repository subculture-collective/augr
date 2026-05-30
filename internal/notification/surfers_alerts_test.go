package notification

import (
	"context"
	"testing"
)

type fakeNotifier struct{ alerts []Alert }

func (f *fakeNotifier) Notify(_ context.Context, a Alert) error {
	f.alerts = append(f.alerts, a)
	return nil
}

func TestSurfersAlerterMethods(t *testing.T) {
	f := &fakeNotifier{}
	a := &SurfersAlerter{notifier: f}
	_ = a.OnDrawdown(context.Background(), "global", -120)
	_ = a.OnFillRateDrop(context.Background(), "s1", 0.7, 1.0)
	_ = a.OnGhostFill(context.Background(), "s1", 6)
	_ = a.OnConsecutiveLossTrip(context.Background(), "s1", 3)
	_ = a.OnRecorderLag(context.Background(), 31)
	if len(f.alerts) != 5 {
		t.Fatalf("alerts=%d", len(f.alerts))
	}
	if f.alerts[0].Metadata["ntfy_priority"] != NtfyPriorityMax {
		t.Fatal("drawdown priority")
	}
	if f.alerts[1].Metadata["ntfy_priority"] != NtfyPriorityHigh {
		t.Fatal("fill rate priority")
	}
	if f.alerts[2].Metadata["ntfy_priority"] != NtfyPriorityHigh {
		t.Fatal("ghost fill priority")
	}
	if f.alerts[3].Metadata["ntfy_priority"] != NtfyPriorityMax {
		t.Fatal("loss trip priority")
	}
	if f.alerts[4].Metadata["ntfy_priority"] != NtfyPriorityHigh {
		t.Fatal("lag priority")
	}
}

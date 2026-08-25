package runcontrol

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestGroupStopRejectsAndCancelsAdmissions(t *testing.T) {
	g := NewGroup()
	ctx, lease, err := g.Admit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g.Stop(Shutdown)
	if cause := context.Cause(ctx); !errors.Is(cause, Shutdown) {
		t.Fatalf("cause = %v", cause)
	}
	if _, _, err := g.Admit(context.Background()); !errors.Is(err, ErrDraining) {
		t.Fatalf("Admit error = %v", err)
	}
	lease.Done()
	lease.Done()
	g.Wait()
	if got := g.InFlight(); got != 0 {
		t.Fatalf("InFlight = %d", got)
	}
}

func TestGroupAdmitStopWaitRace(t *testing.T) {
	for range 100 {
		g := NewGroup()
		var submitters sync.WaitGroup
		for range 20 {
			submitters.Add(1)
			go func() {
				defer submitters.Done()
				ctx, lease, err := g.Admit(context.Background())
				if err != nil {
					return
				}
				<-ctx.Done()
				lease.Done()
			}()
		}
		g.StopAndWait(Shutdown)
		submitters.Wait()
		if got := g.InFlight(); got != 0 {
			t.Fatalf("InFlight = %d", got)
		}
	}
}

func TestGroupLeaseMarker(t *testing.T) {
	g := NewGroup()
	ctx, lease, err := g.Admit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !g.HasLease(ctx) || g.HasLease(context.Background()) {
		t.Fatal("unexpected lease marker")
	}
	lease.Done()
}

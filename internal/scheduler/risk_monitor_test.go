package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
)

func TestRiskMonitor_KillSwitchInactive(t *testing.T) {
	re := &mockRiskEngine{}
	mon := &riskMonitor{
		riskEngine:   re,
		pollInterval: 10 * time.Millisecond,
		logger:       testLogger(),
	}

	ctx, cancel := mon.monitorContext(context.Background())
	defer cancel()

	// Let a few poll cycles run.
	time.Sleep(50 * time.Millisecond)

	select {
	case <-ctx.Done():
		t.Fatal("context should not be cancelled when kill switch is inactive")
	default:
	}
}

func TestRiskMonitor_KillSwitchActiveCancelsContext(t *testing.T) {
	re := &mockRiskEngine{killSwitchActive: true}
	mon := &riskMonitor{
		riskEngine:   re,
		pollInterval: 10 * time.Millisecond,
		logger:       testLogger(),
	}

	ctx, cancel := mon.monitorContext(context.Background())
	defer cancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context cancellation after kill switch activation")
	}
	if !errors.Is(context.Cause(ctx), runcontrol.KillSwitch) {
		t.Fatalf("context cause = %v, want %v", context.Cause(ctx), runcontrol.KillSwitch)
	}
}

func TestRiskMonitor_ErrorCancelsContextFailClosed(t *testing.T) {
	re := &mockRiskEngine{killSwitchErr: errors.New("network error")}
	mon := &riskMonitor{
		riskEngine:   re,
		pollInterval: 10 * time.Millisecond,
		logger:       testLogger(),
	}

	ctx, cancel := mon.monitorContext(context.Background())
	defer cancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context cancellation after poll error")
	}
	if cause := context.Cause(ctx); cause == nil || cause.Error() != "scheduler: poll kill switch: network error" || errors.Is(cause, runcontrol.KillSwitch) {
		t.Fatalf("context cause = %v, want distinct poll failure", cause)
	}
}

func TestRiskMonitor_PanicCancelsContextFailClosed(t *testing.T) {
	re := &mockRiskEngine{panicKillSwitch: true}
	mon := &riskMonitor{
		riskEngine:   re,
		pollInterval: 10 * time.Millisecond,
		logger:       testLogger(),
	}

	ctx, cancel := mon.monitorContext(context.Background())
	defer cancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context cancellation after monitor panic")
	}
	if cause := context.Cause(ctx); cause == nil || cause.Error() != "scheduler: kill-switch monitor panic: sensitive provider detail" || errors.Is(cause, runcontrol.KillSwitch) {
		t.Fatalf("context cause = %v, want distinct panic failure", cause)
	}
}

func TestRiskMonitor_ParentCancelStopsMonitor(t *testing.T) {
	re := &mockRiskEngine{}
	mon := &riskMonitor{
		riskEngine:   re,
		pollInterval: 10 * time.Millisecond,
		logger:       testLogger(),
	}

	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := mon.monitorContext(parent)
	defer cancel()

	parentCancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context cancellation after parent cancel")
	}
	if !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Fatalf("context cause = %v, want parent cancellation", context.Cause(ctx))
	}
}

func TestRiskMonitor_CleanupJoinsInProgressPoll(t *testing.T) {
	entered := make(chan struct{})
	finish := make(chan struct{})
	mon := &riskMonitor{
		riskEngine:   &mockRiskEngine{enteredCh: entered, finishKillSwitch: finish},
		pollInterval: time.Millisecond,
		logger:       testLogger(),
	}

	_, cleanup := mon.monitorContext(context.Background())
	<-entered
	cleaned := make(chan struct{})
	go func() {
		cleanup()
		close(cleaned)
	}()
	select {
	case <-cleaned:
		t.Fatal("cleanup returned while risk poll was running")
	case <-time.After(10 * time.Millisecond):
	}
	close(finish)
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not join completed risk poll")
	}
}

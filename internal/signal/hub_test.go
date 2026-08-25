package signal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSignalHub_PartialStartFailureCleansUpAndStopIsIdempotent(t *testing.T) {
	startErr := errors.New("source start failed")
	started := &hubTestSource{
		name:          "started",
		events:        make(chan RawSignalEvent),
		cancelled:     make(chan struct{}),
		closeOnCancel: true,
	}
	failed := &hubTestSource{name: "failed", startErr: startErr}
	hub := NewSignalHub([]SignalSource{started, failed}, nil, nil)

	if err := hub.Start(context.Background()); !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want %v", err, startErr)
	}
	select {
	case <-started.cancelled:
	case <-time.After(time.Second):
		t.Fatal("started source was not cancelled after later source failed")
	}

	assertReturns(t, hub.Stop)
	assertReturns(t, hub.Stop)
	assertReturns(t, hub.Wait)
}

func TestSignalHub_StopWaitsForWorkers(t *testing.T) {
	events := make(chan RawSignalEvent, 300)
	for range cap(events) {
		events <- RawSignalEvent{Title: "watched"}
	}
	source := &hubTestSource{name: "source", events: events, closeOnCancel: true}
	evaluator := &blockingHubEvaluator{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	index := NewWatchIndex()
	index.AddManual("watched", uuid.New())
	lifecycle := NewLifecycle(index, nil, evaluator, nil, nil, nil)
	hub := NewSignalHub([]SignalSource{source}, lifecycle, nil)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-evaluator.entered:
	case <-time.After(time.Second):
		t.Fatal("processor did not enter evaluator")
	}

	stopped := make(chan struct{})
	go func() {
		hub.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while processor worker was still running")
	case <-time.After(25 * time.Millisecond):
	}

	close(evaluator.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after workers exited")
	}
}

func TestSignalHub_WaitSignalsAfterForwardingWorkerExits(t *testing.T) {
	events := make(chan RawSignalEvent)
	hub := NewSignalHub([]SignalSource{&hubTestSource{name: "source", events: events}}, nil, nil)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	waited := make(chan struct{})
	go func() {
		hub.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned before source forwarding worker exited")
	case <-time.After(25 * time.Millisecond):
	}

	close(events)
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after source forwarding worker exited")
	}
	hub.Stop()
}

func TestSignalHub_StopJoinsSourceAfterForwarderCancellation(t *testing.T) {
	release := make(chan struct{})
	sourceDone := make(chan struct{})
	hub := NewSignalHub([]SignalSource{delayedStopHubSource{release: release, done: sourceDone}}, nil, nil)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	stopped := make(chan struct{})
	go func() {
		hub.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned before source goroutine exited")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-sourceDone:
	case <-time.After(time.Second):
		t.Fatal("source did not exit")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not join source")
	}
}

func TestSignalHub_CancellationWhileForwardingDrainsUntilSourceCloses(t *testing.T) {
	release := make(chan struct{})
	sourceDone := make(chan struct{})
	events := make(chan RawSignalEvent)
	source := &hubTestSource{name: "blocked-forward", events: events}
	go func() {
		defer close(sourceDone)
		defer close(events)
		for range 300 {
			events <- RawSignalEvent{Title: "watched"}
		}
		<-release
	}()
	evaluator := &blockingHubEvaluator{entered: make(chan struct{}), release: make(chan struct{})}
	index := NewWatchIndex()
	index.AddManual("watched", uuid.New())
	hub := NewSignalHub([]SignalSource{source}, NewLifecycle(index, nil, evaluator, nil, nil, nil), nil)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-evaluator.entered

	stopped := make(chan struct{})
	go func() { hub.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned before blocked source closed")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	close(evaluator.release)
	select {
	case <-sourceDone:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not drain source")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not await source closure")
	}
}

func TestSignalHub_CleanupAfterStopCannotRaceProcessing(t *testing.T) {
	events := make(chan RawSignalEvent, 1)
	events <- RawSignalEvent{Title: "watched"}
	evaluator := &cleanupCheckingHubEvaluator{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	index := NewWatchIndex()
	index.AddManual("watched", uuid.New())
	lifecycle := NewLifecycle(index, nil, evaluator, nil, nil, nil)
	hub := NewSignalHub([]SignalSource{&hubTestSource{name: "source", events: events, closeOnCancel: true}}, lifecycle, nil)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-evaluator.entered

	done := make(chan struct{})
	go func() {
		hub.Stop()
		evaluator.mu.Lock()
		evaluator.cleaned = true
		evaluator.mu.Unlock()
		close(done)
	}()
	close(evaluator.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not proceed after Stop")
	}
	if evaluator.sawCleanup {
		t.Fatal("processing observed cleanup before Stop completed")
	}
}

type hubTestSource struct {
	name          string
	events        chan RawSignalEvent
	startErr      error
	cancelled     chan struct{}
	closeOnCancel bool
}

func (s *hubTestSource) Name() string { return s.name }

func (s *hubTestSource) Start(ctx context.Context) (<-chan RawSignalEvent, error) {
	if s.startErr != nil {
		return nil, s.startErr
	}
	if s.cancelled != nil {
		go func() {
			<-ctx.Done()
			close(s.cancelled)
			if s.closeOnCancel {
				close(s.events)
			}
		}()
	} else if s.closeOnCancel {
		go func() {
			<-ctx.Done()
			close(s.events)
		}()
	}
	return s.events, nil
}

type delayedStopHubSource struct {
	release <-chan struct{}
	done    chan<- struct{}
}

func (delayedStopHubSource) Name() string { return "delayed-stop" }

func (s delayedStopHubSource) Start(ctx context.Context) (<-chan RawSignalEvent, error) {
	events := make(chan RawSignalEvent)
	go func() {
		<-ctx.Done()
		<-s.release
		close(events)
		close(s.done)
	}()
	return events, nil
}

type blockingHubEvaluator struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (e *blockingHubEvaluator) Evaluate(context.Context, RawSignalEvent, []StrategyContext) (*EvaluatedSignal, error) {
	e.once.Do(func() { close(e.entered) })
	<-e.release
	return nil, nil
}

type cleanupCheckingHubEvaluator struct {
	mu         sync.Mutex
	entered    chan struct{}
	release    chan struct{}
	cleaned    bool
	sawCleanup bool
}

func (e *cleanupCheckingHubEvaluator) Evaluate(context.Context, RawSignalEvent, []StrategyContext) (*EvaluatedSignal, error) {
	close(e.entered)
	<-e.release
	e.mu.Lock()
	e.sawCleanup = e.cleaned
	e.mu.Unlock()
	return nil, nil
}

func assertReturns(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation deadlocked")
	}
}

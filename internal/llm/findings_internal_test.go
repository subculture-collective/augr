package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type statusErr struct{ code int }

func (e statusErr) Error() string   { return "status" }
func (e statusErr) StatusCode() int { return e.code }

func TestIsRetryableStatusClassification(t *testing.T) {
	t.Parallel()
	for code, want := range map[int]bool{401: false, 403: false, 404: false, 408: true, 429: true, 500: true, 503: true} {
		if got := isRetryable(statusErr{code}); got != want {
			t.Fatalf("isRetryable(%d) = %t, want %t", code, got, want)
		}
	}
}

func TestSharedThrottleIsProcessWide(t *testing.T) {
	resetSharedThrottles()
	t.Cleanup(resetSharedThrottles)

	blocker := make(chan struct{})
	inner := ProviderFunc(func(ctx context.Context, _ CompletionRequest) (*CompletionResponse, error) {
		select {
		case <-blocker:
			return &CompletionResponse{Content: "ok"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	chainA := NewProviderChain(inner, nil, WithThrottle(1), WithThrottleKey("opencode"))
	chainB := NewProviderChain(inner, nil, WithThrottle(1), WithThrottleKey("opencode"))
	other := NewProviderChain(inner, nil, WithThrottle(1), WithThrottleKey("openai"))

	req := CompletionRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}
	go func() { _, _ = chainA.Complete(context.Background(), req) }()
	time.Sleep(20 * time.Millisecond) // chainA now holds the shared opencode slot

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := chainB.Complete(ctx, req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("chainB should block on the shared semaphore, got err=%v", err)
	}
	if sharedThrottle("opencode", 1) != sharedThrottle("opencode", 7) {
		t.Fatal("registry returned different semaphores for the same key")
	}

	otherCtx, otherCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer otherCancel()
	go func() { time.Sleep(10 * time.Millisecond); close(blocker) }()
	if _, err := other.Complete(otherCtx, req); err != nil {
		t.Fatalf("other provider key should not share the slot: %v", err)
	}
}

func TestFallbackDoesNotDetachFromCancelledParent(t *testing.T) {
	t.Parallel()
	primary := ProviderFunc(func(context.Context, CompletionRequest) (*CompletionResponse, error) {
		return nil, context.DeadlineExceeded
	})
	var secondaryCalls int
	secondary := ProviderFunc(func(context.Context, CompletionRequest) (*CompletionResponse, error) {
		secondaryCalls++
		return &CompletionResponse{Content: "fallback"}, nil
	})
	fp, err := NewFallbackProvider(primary, secondary, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fp.Complete(ctx, CompletionRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Complete() error = %v, want primary deadline error", err)
	}
	if secondaryCalls != 0 {
		t.Fatalf("secondary called %d times on a cancelled parent", secondaryCalls)
	}

	fbCtx, fbCancel := newFallbackContext(ctx)
	defer fbCancel()
	if fbCtx.Err() == nil {
		t.Fatal("fallback context from a cancelled parent must stay cancelled")
	}
}

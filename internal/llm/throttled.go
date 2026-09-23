package llm

import (
	"context"
	"sync"
)

// ThrottledProvider wraps a Provider with a concurrency limiter.
// All callers share the same semaphore, preventing Ollama (or any
// serial LLM backend) from being overwhelmed by concurrent requests.
type ThrottledProvider struct {
	inner Provider
	sem   chan struct{}
}

// NewThrottledProvider creates a provider that allows at most maxConcurrent
// simultaneous LLM calls. Additional calls block until a slot is available.
func NewThrottledProvider(inner Provider, maxConcurrent int) *ThrottledProvider {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return NewThrottledProviderWithSemaphore(inner, make(chan struct{}, maxConcurrent))
}

// NewThrottledProviderWithSemaphore creates a provider that acquires a slot
// from the supplied semaphore for each call. Several providers may share one
// semaphore; tests can pass their own channel to observe capacity.
func NewThrottledProviderWithSemaphore(inner Provider, sem chan struct{}) *ThrottledProvider {
	if cap(sem) < 1 {
		sem = make(chan struct{}, 1)
	}
	return &ThrottledProvider{inner: inner, sem: sem}
}

// Capacity reports the semaphore size.
func (t *ThrottledProvider) Capacity() int { return cap(t.sem) }

func (t *ThrottledProvider) Complete(ctx context.Context, request CompletionRequest) (*CompletionResponse, error) {
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
		return t.inner.Complete(ctx, request)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// throttleRegistry holds process-wide semaphores keyed by provider name so
// LLM_THROTTLE_CONCURRENCY applies across every chain built for a provider.
var throttleRegistry = struct {
	mu   sync.Mutex
	sems map[string]chan struct{}
}{sems: make(map[string]chan struct{})}

// sharedThrottle returns the process-wide semaphore for key, creating it with
// the given capacity on first use. A later request with a different capacity
// reuses the existing semaphore; the first registration wins.
func sharedThrottle(key string, capacity int) chan struct{} {
	if capacity < 1 {
		capacity = 1
	}
	throttleRegistry.mu.Lock()
	defer throttleRegistry.mu.Unlock()
	if sem, ok := throttleRegistry.sems[key]; ok {
		return sem
	}
	sem := make(chan struct{}, capacity)
	throttleRegistry.sems[key] = sem
	return sem
}

// resetSharedThrottles clears the registry; for tests only.
func resetSharedThrottles() {
	throttleRegistry.mu.Lock()
	defer throttleRegistry.mu.Unlock()
	throttleRegistry.sems = make(map[string]chan struct{})
}

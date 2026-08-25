package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrRunAlreadyRegistered = errors.New("pipeline run context already registered")

type runContextKey struct {
	id        uuid.UUID
	tradeDate time.Time
}

// RunContextRegistry tracks active run cancellation functions for best-effort cleanup.
type RunContextRegistry struct {
	mu      sync.Mutex
	cancels map[runContextKey]context.CancelCauseFunc
}

func NewRunContextRegistry() *RunContextRegistry {
	return &RunContextRegistry{cancels: make(map[runContextKey]context.CancelCauseFunc)}
}

func normalizeTradeDate(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func (r *RunContextRegistry) Register(runID uuid.UUID, tradeDate time.Time, cancel context.CancelCauseFunc) error {
	if r == nil || cancel == nil {
		return nil
	}
	key := runContextKey{runID, normalizeTradeDate(tradeDate)}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.cancels[key]; exists {
		return ErrRunAlreadyRegistered
	}
	r.cancels[key] = cancel
	return nil
}

func (r *RunContextRegistry) Deregister(runID uuid.UUID, tradeDate time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.cancels, runContextKey{runID, normalizeTradeDate(tradeDate)})
	r.mu.Unlock()
}

func (r *RunContextRegistry) Cancel(runID uuid.UUID, tradeDate time.Time, cause error) bool {
	if r == nil {
		return false
	}
	key := runContextKey{runID, normalizeTradeDate(tradeDate)}
	r.mu.Lock()
	cancel, ok := r.cancels[key]
	if ok {
		delete(r.cancels, key)
	}
	r.mu.Unlock()
	if ok {
		cancel(cause)
	}
	return ok
}

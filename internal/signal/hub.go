package signal

import (
	"context"
	"log/slog"
	"sync"
)

// SignalHub fans in events from multiple SignalSource adapters and hands each
// raw event to the lifecycle module for matching, evaluation, and trigger
// emission.
type SignalHub struct {
	sources   []SignalSource
	lifecycle *Lifecycle
	logger    *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped chan struct{}
}

// NewSignalHub constructs a SignalHub.
func NewSignalHub(sources []SignalSource, lifecycle *Lifecycle, logger *slog.Logger) *SignalHub {
	if logger == nil {
		logger = slog.Default()
	}
	return &SignalHub{
		sources:   sources,
		lifecycle: lifecycle,
		logger:    logger,
	}
}

// Start launches all sources and the fan-in loop. Returns immediately; call
// Stop to shut down gracefully.
func (h *SignalHub) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.cancel != nil {
		return nil // already running
	}

	runCtx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	h.stopped = make(chan struct{})

	if h.lifecycle != nil {
		if err := h.lifecycle.RebuildWatchIndex(runCtx); err != nil {
			h.logger.Warn("signal hub: initial watch index build failed", slog.Any("error", err))
		}
	}

	merged := make(chan RawSignalEvent, 256)
	var wg sync.WaitGroup
	for _, src := range h.sources {
		ch, err := src.Start(runCtx)
		if err != nil {
			cancel()
			return err
		}
		wg.Add(1)
		go func(c <-chan RawSignalEvent) {
			defer wg.Done()
			for evt := range c {
				select {
				case merged <- evt:
				case <-runCtx.Done():
					return
				}
			}
		}(ch)
	}

	go func() {
		wg.Wait()
		close(merged)
	}()

	go func() {
		defer close(h.stopped)
		for {
			select {
			case evt, ok := <-merged:
				if !ok {
					return
				}
				if h.lifecycle != nil {
					h.lifecycle.Process(runCtx, evt)
				}
			case <-runCtx.Done():
				return
			}
		}
	}()

	return nil
}

// Stop shuts down the hub and waits for the fan-in loop to finish.
func (h *SignalHub) Stop() {
	h.mu.Lock()
	cancel := h.cancel
	stopped := h.stopped
	h.cancel = nil
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stopped != nil {
		<-stopped
	}
}

// RebuildWatchIndex refreshes the watch index from the current strategy list.
func (h *SignalHub) RebuildWatchIndex(ctx context.Context) error {
	if h.lifecycle == nil {
		return nil
	}
	return h.lifecycle.RebuildWatchIndex(ctx)
}

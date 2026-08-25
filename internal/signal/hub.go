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
	var sourceWG sync.WaitGroup
	var workerWG sync.WaitGroup
	for _, src := range h.sources {
		ch, err := src.Start(runCtx)
		if err != nil {
			cancel()
			workerWG.Wait()
			close(h.stopped)
			h.cancel = nil
			h.stopped = nil
			return err
		}
		sourceWG.Add(1)
		workerWG.Add(1)
		go func(c <-chan RawSignalEvent) {
			defer sourceWG.Done()
			defer workerWG.Done()
			for {
				select {
				case <-runCtx.Done():
					// Source completion is represented by channel closure. Keep
					// draining after cancellation so Stop joins the source itself,
					// not only this forwarder.
					drainRawSignalEvents(c)
					return
				case evt, ok := <-c:
					if !ok {
						return
					}
					select {
					case merged <- evt:
					case <-runCtx.Done():
						drainRawSignalEvents(c)
						return
					}
				}
			}
		}(ch)
	}

	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		sourceWG.Wait()
		close(merged)
	}()

	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		for {
			select {
			case evt, ok := <-merged:
				if !ok {
					return
				}
				if runCtx.Err() != nil {
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

	stopped := h.stopped
	go func() {
		workerWG.Wait()
		close(stopped)
	}()

	return nil
}

func drainRawSignalEvents(events <-chan RawSignalEvent) {
	for range events {
		continue
	}
}

// Stop shuts down the hub and waits for the fan-in loop to finish.
func (h *SignalHub) Stop() {
	h.mu.Lock()
	cancel := h.cancel
	stopped := h.stopped
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stopped != nil {
		<-stopped
	}

	h.mu.Lock()
	if h.stopped == stopped {
		h.cancel = nil
		h.stopped = nil
	}
	h.mu.Unlock()
}

// Wait blocks until all source-forwarding and processing workers have exited.
func (h *SignalHub) Wait() {
	h.mu.Lock()
	stopped := h.stopped
	h.mu.Unlock()
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

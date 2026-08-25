package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/risk"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
)

const defaultPollInterval = 30 * time.Second

// riskMonitor polls the risk engine's kill switch and cancels the given context
// when the kill switch becomes active.
type riskMonitor struct {
	riskEngine   risk.RiskEngine
	pollInterval time.Duration
	logger       *slog.Logger
}

// monitorContext wraps the parent context with kill-switch polling. It returns
// a derived context that will be cancelled if the kill switch activates, and a
// cleanup function that must be called to stop and join the monitor goroutine.
func (m *riskMonitor) monitorContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancelCause := context.WithCancelCause(parent)
	var wg sync.WaitGroup

	interval := m.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				panicErr := fmt.Errorf("scheduler: kill-switch monitor panic: %v", recovered)
				if m.logger != nil {
					m.logger.Error("scheduler: kill-switch monitor panic contained",
						slog.String("panic_type", fmt.Sprintf("%T", recovered)),
					)
				}
				// Losing the kill-switch monitor must stop the pipeline rather
				// than leave it running without its fail-closed safety poll.
				cancelCause(panicErr)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				active, err := m.riskEngine.IsKillSwitchActive(ctx)
				if err != nil {
					if m.logger != nil {
						m.logger.Error("scheduler: failed to poll kill switch; cancelling pipeline",
							slog.String("error_type", fmt.Sprintf("%T", err)),
						)
					}
					cancelCause(fmt.Errorf("scheduler: poll kill switch: %w", err))
					return
				}
				if active {
					if m.logger != nil {
						m.logger.Warn("scheduler: kill switch activated during pipeline execution; cancelling")
					}
					cancelCause(runcontrol.KillSwitch)
					return
				}
			}
		}
	}()

	cleanup := func() {
		cancelCause(nil)
		wg.Wait()
	}
	return ctx, cleanup
}

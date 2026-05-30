package polymarket

import (
	"errors"
	"log/slog"
	"time"
)

// Metrics is a minimal placeholder for package instrumentation.
type Metrics interface {
	IncCounter(name string, labels map[string]string)
}

type Config struct {
	WSURL                string
	ConnectionsPerFeed   int
	WarmupDuration       time.Duration
	WarmupMinClean       int
	WarmupMaxJumpUSD     float64
	PruneInterval        time.Duration
	PruneFraction        float64
	MaxTickDeltaUSD      float64
	StaggerStartup       time.Duration
	DropFirstTickPerConn bool
	JitterEMAAlpha       float64
	PerMarketSlugs       []string
	Logger               *slog.Logger
	Metrics              Metrics
}

func DefaultConfig() Config {
	return Config{
		WSURL:                "wss://ws-subscriptions-clob.polymarket.com/ws/",
		ConnectionsPerFeed:   100,
		WarmupDuration:       15 * time.Second,
		WarmupMinClean:       3,
		WarmupMaxJumpUSD:     0.05,
		PruneInterval:        4 * time.Second,
		PruneFraction:        0.10,
		MaxTickDeltaUSD:      0.15,
		StaggerStartup:       1 * time.Second,
		DropFirstTickPerConn: true,
		JitterEMAAlpha:       0.2,
	}
}

func (c Config) Validate() error {
	if c.WSURL == "" {
		return errors.New("WSURL must not be empty")
	}
	if c.ConnectionsPerFeed < 1 {
		return errors.New("ConnectionsPerFeed must be at least 1")
	}
	if !(c.PruneFraction > 0 && c.PruneFraction < 1) {
		return errors.New("PruneFraction must be in (0,1)")
	}
	if !(c.JitterEMAAlpha > 0 && c.JitterEMAAlpha <= 1) {
		return errors.New("JitterEMAAlpha must be in (0,1]")
	}
	return nil
}

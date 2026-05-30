package polymarket

import (
	"context"
	"testing"
	"time"
)

func TestCleanerWarmupBlocksEmission(t *testing.T) {
	in := make(chan Tick, 8)
	c := NewCleaner(testCleanerConfig(), in)
	c.Start(context.Background())
	defer c.Close()

	in <- tickAt("slug-a", "yes", 0.40, 1, 0)
	in <- tickAt("slug-a", "yes", 0.41, 1, 1)

	if c.Ready("slug-a") {
		t.Fatal("expected not ready during warmup")
	}
	select {
	case tk := <-c.Out():
		t.Fatalf("unexpected emission: %+v", tk)
	case <-time.After(75 * time.Millisecond):
	}
}

func TestCleanerEmitsAfterWarmup(t *testing.T) {
	in := make(chan Tick, 8)
	c := NewCleaner(testCleanerConfig(), in)
	c.Start(context.Background())
	defer c.Close()

	in <- tickAtNow("slug-a", "yes", 0.40, 1, 0)
	time.Sleep(15 * time.Millisecond)
	in <- tickAtNow("slug-a", "yes", 0.41, 1, 1)

	waitForBool(t, 100*time.Millisecond, func() bool { return c.Ready("slug-a") })

	tk := tickAtNow("slug-a", "yes", 0.42, 1, 2)
	in <- tk
	got := waitForTick(t, c.Out(), 100*time.Millisecond)
	if got.Price != tk.Price || got.Slug != tk.Slug {
		t.Fatalf("unexpected tick: %+v", got)
	}
}

func TestCleanerRejectsLargeDelta(t *testing.T) {
	in := make(chan Tick, 8)
	c := NewCleaner(testCleanerConfig(), in)
	c.Start(context.Background())
	defer c.Close()

	in <- tickAtNow("slug-a", "yes", 0.40, 1, 0)
	time.Sleep(15 * time.Millisecond)
	in <- tickAtNow("slug-a", "yes", 0.41, 1, 1)
	waitForBool(t, 100*time.Millisecond, func() bool { return c.Ready("slug-a") })

	in <- tickAtNow("slug-a", "yes", 0.80, 1, 2)
	select {
	case tk := <-c.Out():
		t.Fatalf("unexpected emitted delta tick: %+v", tk)
	case <-time.After(75 * time.Millisecond):
	}
	if !c.Ready("slug-a") {
		t.Fatal("ready state changed unexpectedly")
	}
}

func TestCleanerDedupesWithinTTL(t *testing.T) {
	in := make(chan Tick, 8)
	c := NewCleaner(testCleanerConfig(), in)
	c.Start(context.Background())
	defer c.Close()

	in <- tickAtNow("slug-a", "yes", 0.40, 1, 0)
	time.Sleep(15 * time.Millisecond)
	in <- tickAtNow("slug-a", "yes", 0.41, 1, 1)
	waitForBool(t, 100*time.Millisecond, func() bool { return c.Ready("slug-a") })

	tk := tickAtNow("slug-a", "yes", 0.42, 1, 2)
	in <- tk
	first := waitForTick(t, c.Out(), 100*time.Millisecond)
	if first.Price != tk.Price {
		t.Fatalf("unexpected first emission: %+v", first)
	}
	in <- tk
	select {
	case dup := <-c.Out():
		t.Fatalf("unexpected duplicate emission: %+v", dup)
	case <-time.After(75 * time.Millisecond):
	}
}

func testCleanerConfig() Config {
	cfg := DefaultConfig()
	cfg.WSURL = "ws://example.invalid"
	cfg.WarmupDuration = 10 * time.Millisecond
	cfg.WarmupMinClean = 2
	cfg.WarmupMaxJumpUSD = 0.05
	cfg.MaxTickDeltaUSD = 0.10
	return cfg
}

func tickAt(slug, side string, price, size float64, seq uint64) Tick {
	return Tick{Slug: slug, Side: side, Price: price, Size: size, ReceivedAt: time.Unix(0, int64(seq)*int64(time.Millisecond)), SeqHint: seq}
}

func tickAtNow(slug, side string, price, size float64, seq uint64) Tick {
	return Tick{Slug: slug, Side: side, Price: price, Size: size, ReceivedAt: time.Now().UTC(), SeqHint: seq}
}

func waitForTick(t *testing.T, ch <-chan Tick, d time.Duration) Tick {
	t.Helper()
	select {
	case tk := <-ch:
		return tk
	case <-time.After(d):
		t.Fatal("timeout waiting for tick")
	}
	return Tick{}
}

func waitForBool(t *testing.T, d time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("timeout waiting for condition")
}

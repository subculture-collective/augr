package signal

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPolymarketSource_ShutdownJoinsWatchedMarketRefresh(t *testing.T) {
	refreshEntered := make(chan struct{})
	releaseRefresh := make(chan struct{})
	loader := &blockingWatchedMarketsLoader{refreshEntered: refreshEntered, releaseRefresh: releaseRefresh}
	source := NewPolymarketSource(PolymarketSourceConfig{Interval: time.Hour, Loader: loader}, nil)
	source.refreshInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	events, err := source.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-refreshEntered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	cancel()
	select {
	case <-events:
		t.Fatal("source channel closed before refresh goroutine exited")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseRefresh)
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("unexpected source event")
		}
	case <-time.After(time.Second):
		t.Fatal("source channel did not close after refresh exited")
	}
}

type blockingWatchedMarketsLoader struct {
	calls          atomic.Int32
	refreshEntered chan struct{}
	releaseRefresh chan struct{}
	enteredOnce    sync.Once
}

func (l *blockingWatchedMarketsLoader) ListEnabledSlugs(context.Context) ([]string, error) {
	if l.calls.Add(1) == 1 {
		return nil, nil
	}
	l.enteredOnce.Do(func() { close(l.refreshEntered) })
	<-l.releaseRefresh
	return nil, nil
}

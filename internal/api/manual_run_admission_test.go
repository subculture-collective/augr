package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/copytrading"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/runcontrol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type blockingBacktestRunner struct {
	called  atomic.Int32
	started chan error
	release chan struct{}
}

func (r *blockingBacktestRunner) RunBacktest(ctx context.Context, _ uuid.UUID, _ string) (*domain.BacktestRun, error) {
	r.called.Add(1)
	<-ctx.Done()
	r.started <- context.Cause(ctx)
	<-r.release
	return nil, ctx.Err()
}

type blockingCopyRebalancer struct {
	called  atomic.Int32
	started chan error
	release chan struct{}
}

func (r *blockingCopyRebalancer) Rebalance(ctx context.Context, _ uuid.UUID) (*copytrading.RebalanceResult, error) {
	r.called.Add(1)
	<-ctx.Done()
	r.started <- context.Cause(ctx)
	<-r.release
	return nil, ctx.Err()
}

func requestWithID(method, target string, id uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", id.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func TestManualBacktestAdmissionDrainsAndRejectsNewWork(t *testing.T) {
	group := runcontrol.NewGroup()
	runner := &blockingBacktestRunner{started: make(chan error, 1), release: make(chan struct{})}
	srv := &Server{backtestSvc: runner, runGroup: group}
	done := make(chan struct{})
	go func() {
		srv.handleRunBacktestConfig(httptest.NewRecorder(), requestWithID(http.MethodPost, "/backtest", uuid.New()))
		close(done)
	}()

	for runner.called.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	drained := make(chan struct{})
	go func() { group.StopAndWait(runcontrol.Shutdown); close(drained) }()
	if cause := <-runner.started; !errors.Is(cause, runcontrol.Shutdown) {
		t.Fatalf("service context cause = %v, want Shutdown", cause)
	}
	select {
	case <-drained:
		t.Fatal("StopAndWait returned before service persistence completed")
	case <-time.After(10 * time.Millisecond):
	}

	rejected := httptest.NewRecorder()
	srv.handleRunBacktestConfig(rejected, requestWithID(http.MethodPost, "/backtest", uuid.New()))
	if rejected.Code != http.StatusServiceUnavailable || runner.called.Load() != 1 {
		t.Fatalf("draining request = status %d, calls %d; want 503 and one call", rejected.Code, runner.called.Load())
	}
	close(runner.release)
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("StopAndWait did not join backtest service")
	}
	<-done
}

func TestManualCopyRebalanceAdmissionDrainsAndRejectsNewWork(t *testing.T) {
	group := runcontrol.NewGroup()
	rebalancer := &blockingCopyRebalancer{started: make(chan error, 1), release: make(chan struct{})}
	srv := &Server{copyTrading: copytrading.NewService(copytrading.ServiceDeps{}), copyRebalancer: rebalancer, runGroup: group}
	done := make(chan struct{})
	go func() {
		srv.handleRebalanceCopySubscription(httptest.NewRecorder(), requestWithID(http.MethodPost, "/copy", uuid.New()))
		close(done)
	}()

	for rebalancer.called.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	drained := make(chan struct{})
	go func() { group.StopAndWait(runcontrol.Shutdown); close(drained) }()
	if cause := <-rebalancer.started; !errors.Is(cause, runcontrol.Shutdown) {
		t.Fatalf("service context cause = %v, want Shutdown", cause)
	}
	select {
	case <-drained:
		t.Fatal("StopAndWait returned before post-authority persistence completed")
	case <-time.After(10 * time.Millisecond):
	}

	rejected := httptest.NewRecorder()
	srv.handleRebalanceCopySubscription(rejected, requestWithID(http.MethodPost, "/copy", uuid.New()))
	if rejected.Code != http.StatusServiceUnavailable || rebalancer.called.Load() != 1 {
		t.Fatalf("draining request = status %d, calls %d; want 503 and one call", rejected.Code, rebalancer.called.Load())
	}
	close(rebalancer.release)
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("StopAndWait did not join copy rebalance")
	}
	<-done
}

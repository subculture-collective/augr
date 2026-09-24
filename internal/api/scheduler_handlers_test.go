package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type stubSchedulerReloader struct {
	err        error
	reloads    int
	registered int
}

func (s *stubSchedulerReloader) Reload(context.Context) error { s.reloads++; return s.err }
func (s *stubSchedulerReloader) RegisteredStrategyCount() int { return s.registered }

func TestSchedulerReloadHandler(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		srv        *Server
		wantStatus int
	}{
		{name: "not configured", srv: &Server{adminAPIKey: "k"}, wantStatus: http.StatusServiceUnavailable},
		{name: "not started", srv: &Server{adminAPIKey: "k", scheduler: &stubSchedulerReloader{err: scheduler.ErrNotStarted}}, wantStatus: http.StatusConflict},
		{name: "reload error", srv: &Server{adminAPIKey: "k", scheduler: &stubSchedulerReloader{err: errors.New("db down")}}, wantStatus: http.StatusInternalServerError},
		{name: "ok", srv: &Server{adminAPIKey: "k", scheduler: &stubSchedulerReloader{registered: 3}}, wantStatus: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.srv.logger = discardLogger()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scheduler/reload", nil)
			req.Header.Set("X-Admin-Key", "k")
			rr := httptest.NewRecorder()
			tc.srv.requireAdmin(http.HandlerFunc(tc.srv.handleSchedulerReload)).ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				body := decodeJSON[SchedulerReloadResponse](t, rr)
				if body.Registered != 3 {
					t.Fatalf("registered = %d, want 3", body.Registered)
				}
			}
		})
	}

	t.Run("requires admin key", func(t *testing.T) {
		srv := &Server{adminAPIKey: "k", scheduler: &stubSchedulerReloader{}, logger: discardLogger()}
		rr := httptest.NewRecorder()
		srv.requireAdmin(http.HandlerFunc(srv.handleSchedulerReload)).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/scheduler/reload", nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rr.Code)
		}
	})
}

func TestStrategyWritesTriggerSchedulerReload(t *testing.T) {
	t.Parallel()

	reloader := &stubSchedulerReloader{}
	deps := testDeps()
	deps.Scheduler = reloader
	srv := newTestServerWithDeps(t, deps)

	rr := doRequest(t, srv, http.MethodDelete, "/api/v1/strategies/"+stratA.ID.String(), nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rr.Code, rr.Body.String())
	}
	if reloader.reloads != 1 {
		t.Fatalf("reloads after delete = %d, want 1", reloader.reloads)
	}
}

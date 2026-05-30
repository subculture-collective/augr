package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type fakeRiskBreaker struct {
	resetErr   error
	resetCalls []string
}

func (f *fakeRiskBreaker) Allow(_ context.Context, _ string) error            { return nil }
func (f *fakeRiskBreaker) Trip(_ context.Context, scope, reason string) error { return nil }
func (f *fakeRiskBreaker) Reset(_ context.Context, scope string) error {
	f.resetCalls = append(f.resetCalls, scope)
	return f.resetErr
}

func TestHandleRiskBreakerReset(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*testing.T) (*Server, *http.Request, *httptest.ResponseRecorder)
		wantStatus int
		wantBody   string
	}{
		{name: "missing scope", setup: func(t *testing.T) (*Server, *http.Request, *httptest.ResponseRecorder) {
			t.Setenv("ADMIN_API_KEY", "test-key")
			srv := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/risk/breaker/reset", bytes.NewBufferString(`{}`))
			req.Header.Set("X-Admin-Key", "test-key")
			return srv, req, httptest.NewRecorder()
		}, wantStatus: http.StatusBadRequest, wantBody: `{"error":"missing_scope"}`},
		{name: "nil breaker", setup: func(t *testing.T) (*Server, *http.Request, *httptest.ResponseRecorder) {
			t.Setenv("ADMIN_API_KEY", "test-key")
			srv := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/risk/breaker/reset", bytes.NewBufferString(`{"scope":"global"}`))
			req.Header.Set("X-Admin-Key", "test-key")
			return srv, req, httptest.NewRecorder()
		}, wantStatus: http.StatusServiceUnavailable, wantBody: `{"error":"risk breaker not configured"}`},
		{name: "success", setup: func(t *testing.T) (*Server, *http.Request, *httptest.ResponseRecorder) {
			t.Setenv("ADMIN_API_KEY", "test-key")
			br := &fakeRiskBreaker{}
			srv := &Server{riskBreaker: br}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/risk/breaker/reset", bytes.NewBufferString(`{"scope":"strategy:abc"}`))
			req.Header.Set("X-Admin-Key", "test-key")
			return srv, req, httptest.NewRecorder()
		}, wantStatus: http.StatusOK, wantBody: `{"scope":"strategy:abc","reset":true}`},
		{name: "admin disabled", setup: func(t *testing.T) (*Server, *http.Request, *httptest.ResponseRecorder) {
			t.Setenv("ADMIN_API_KEY", "")
			srv := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/risk/breaker/reset", bytes.NewBufferString(`{"scope":"global"}`))
			req.Header.Set("X-Admin-Key", "test-key")
			return srv, req, httptest.NewRecorder()
		}, wantStatus: http.StatusServiceUnavailable, wantBody: `{"error":"ADMIN_API_KEY not configured"}`},
		{name: "wrong key", setup: func(t *testing.T) (*Server, *http.Request, *httptest.ResponseRecorder) {
			t.Setenv("ADMIN_API_KEY", "test-key")
			srv := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/risk/breaker/reset", bytes.NewBufferString(`{"scope":"global"}`))
			req.Header.Set("X-Admin-Key", "wrong")
			return srv, req, httptest.NewRecorder()
		}, wantStatus: http.StatusUnauthorized, wantBody: `{"error":"admin key required"}`},
		{name: "repo not found treated as success", setup: func(t *testing.T) (*Server, *http.Request, *httptest.ResponseRecorder) {
			t.Setenv("ADMIN_API_KEY", "test-key")
			br := &fakeRiskBreaker{resetErr: repository.ErrNotFound}
			srv := &Server{riskBreaker: br}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/risk/breaker/reset", bytes.NewBufferString(`{"scope":"global"}`))
			req.Header.Set("X-Admin-Key", "test-key")
			return srv, req, httptest.NewRecorder()
		}, wantStatus: http.StatusOK, wantBody: `{"scope":"global","reset":true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, req, rr := tt.setup(t)
			srv.requireAdmin(http.HandlerFunc(srv.handleRiskBreakerReset)).ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status=%d want %d body=%s", rr.Code, tt.wantStatus, rr.Body.String())
			}
			var got, want map[string]any
			_ = json.Unmarshal([]byte(rr.Body.String()), &got)
			_ = json.Unmarshal([]byte(tt.wantBody), &want)
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("body[%s]=%v want %v", k, got[k], v)
				}
			}
		})
	}
}

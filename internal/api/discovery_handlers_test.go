package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PatrickFanella/get-rich-quick/internal/automation"
	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
)

func TestRunDiscoveryReadinessLockPrecedesRequestAndProviders(t *testing.T) {
	calls := 0
	s := &Server{
		discoveryReadiness: &automation.DiscoveryReadiness{Err: fmt.Errorf("readiness: %w", pgrepo.ErrDiscoveryDeploymentImmutableBinding)},
		discoveryDeps:      &discovery.DiscoveryDeps{},
		discoveryRunner: func(context.Context, discovery.DiscoveryConfig, discovery.DiscoveryDeps) (*discovery.DiscoveryResult, error) {
			calls++
			return nil, errors.New("unexpected discovery call")
		},
	}
	rr := httptest.NewRecorder()
	s.handleRunDiscovery(rr, httptest.NewRequest(http.MethodPost, "/api/v1/discovery/run", strings.NewReader("not-json")))
	var response ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusLocked || response.Code != errCodeDiscoveryImmutableBindingLocked || response.Error != pgrepo.DiscoveryDeploymentUnavailableReason || calls != 0 {
		t.Fatalf("locked response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRunDiscoveryReadinessEvaluationErrorIsUnavailable(t *testing.T) {
	calls := 0
	s := &Server{
		discoveryReadiness: &automation.DiscoveryReadiness{Err: errors.New("database unavailable")},
		discoveryDeps:      &discovery.DiscoveryDeps{},
		discoveryRunner: func(context.Context, discovery.DiscoveryConfig, discovery.DiscoveryDeps) (*discovery.DiscoveryResult, error) {
			calls++
			return nil, nil
		},
	}
	rr := httptest.NewRecorder()
	s.handleRunDiscovery(rr, httptest.NewRequest(http.MethodPost, "/api/v1/discovery/run", strings.NewReader(`{"tickers":["AAPL"]}`)))
	var response ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusServiceUnavailable || response.Code != errCodeDiscoveryReadinessUnavailable || response.Error != automation.DiscoveryReadinessEvaluationErrorReason || strings.Contains(rr.Body.String(), "database unavailable") || calls != 0 {
		t.Fatalf("error response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRunDiscoveryUnknownFalseReadinessIsUnavailable(t *testing.T) {
	for _, readiness := range []*automation.DiscoveryReadiness{
		{Reason: ""},
		{Reason: "untyped false reason"},
		{Err: repository.NewImmutableBindingLock("")},
	} {
		calls := 0
		s := &Server{
			discoveryReadiness: readiness,
			discoveryDeps:      &discovery.DiscoveryDeps{},
			discoveryRunner: func(context.Context, discovery.DiscoveryConfig, discovery.DiscoveryDeps) (*discovery.DiscoveryResult, error) {
				calls++
				return nil, nil
			},
		}
		rr := httptest.NewRecorder()
		s.handleRunDiscovery(rr, httptest.NewRequest(http.MethodPost, "/api/v1/discovery/run", strings.NewReader(`{"tickers":["AAPL"]}`)))
		var response ErrorResponse
		if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if rr.Code != http.StatusServiceUnavailable || response.Code != errCodeDiscoveryReadinessUnavailable || response.Error != automation.DiscoveryReadinessEvaluationErrorReason || calls != 0 {
			t.Fatalf("unknown response = %d %+v", rr.Code, response)
		}
	}
}

func TestRunDiscoveryMissingReadinessEvaluationIsUnavailable(t *testing.T) {
	calls := 0
	s := &Server{
		discoveryDeps: &discovery.DiscoveryDeps{},
		discoveryRunner: func(context.Context, discovery.DiscoveryConfig, discovery.DiscoveryDeps) (*discovery.DiscoveryResult, error) {
			calls++
			return nil, nil
		},
	}
	rr := httptest.NewRecorder()
	s.handleRunDiscovery(rr, httptest.NewRequest(http.MethodPost, "/api/v1/discovery/run", strings.NewReader(`{"tickers":["AAPL"]}`)))
	var response ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusServiceUnavailable || response.Code != errCodeDiscoveryReadinessUnavailable || response.Error != automation.DiscoveryReadinessEvaluationErrorReason || calls != 0 {
		t.Fatalf("missing evaluation response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRunDiscoveryReadyCallsRunner(t *testing.T) {
	calls := 0
	s := &Server{
		discoveryReadiness: &automation.DiscoveryReadiness{Ready: true},
		discoveryDeps:      &discovery.DiscoveryDeps{},
		discoveryRunner: func(_ context.Context, cfg discovery.DiscoveryConfig, _ discovery.DiscoveryDeps) (*discovery.DiscoveryResult, error) {
			calls++
			if len(cfg.Screener.Tickers) != 1 || cfg.Screener.Tickers[0] != "AAPL" {
				t.Fatalf("config tickers = %#v", cfg.Screener.Tickers)
			}
			return &discovery.DiscoveryResult{}, nil
		},
	}
	rr := httptest.NewRecorder()
	s.handleRunDiscovery(rr, httptest.NewRequest(http.MethodPost, "/api/v1/discovery/run", strings.NewReader(`{"tickers":["AAPL"]}`)))
	if rr.Code != http.StatusOK || calls != 1 {
		t.Fatalf("ready response = %d %s; calls = %d", rr.Code, rr.Body.String(), calls)
	}
}

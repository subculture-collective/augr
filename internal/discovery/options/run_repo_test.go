package options

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
	"github.com/PatrickFanella/get-rich-quick/internal/discovery"
)

type captureOptionsRunRepository struct {
	config     json.RawMessage
	result     json.RawMessage
	startedAt  time.Time
	duration   time.Duration
	candidates int
	deployed   int
	err        error
}

func (r *captureOptionsRunRepository) Create(_ context.Context, config, result json.RawMessage, startedAt time.Time, duration time.Duration, candidates, deployed int) error {
	r.config, r.result, r.startedAt, r.duration, r.candidates, r.deployed = config, result, startedAt, duration, candidates, deployed
	return r.err
}

func (*captureOptionsRunRepository) List(context.Context, int, int) ([]discovery.DiscoveryRun, error) {
	return nil, nil
}
func (*captureOptionsRunRepository) Count(context.Context) (int, error) { return 0, nil }

func TestPersistOptionsRunStoresSanitizedConfigAndCompleteResult(t *testing.T) {
	repo := &captureOptionsRunRepository{}
	startedAt := time.Now().Add(-time.Minute).UTC()
	result := &OptionsDiscoveryResult{
		Candidates: 12, Generated: 4, Deployed: 2, Duration: 45 * time.Second,
		Errors:             []string{"one"},
		GenerationEvidence: []OptionsGenerationEvidence{{Ticker: "NVDA", SystemPromptSHA256: "abc", Attempts: []discovery.GenerationAttemptEvidence{{CostUSD: math.Inf(1)}}}},
		Winners: []OptionsDeployedStrategy{{
			Ticker: "NVDA", Score: math.NaN(),
			InSample: backtest.Metrics{SortinoRatio: math.Inf(-1), StartEquity: math.NaN(), OrderFills: 77},
		}},
	}
	cfg := OptionsDiscoveryConfig{
		Screener:   OptionsScreenerConfig{Tickers: []string{"NVDA"}, MinADV: 123},
		Generator:  discovery.GeneratorConfig{Model: "openai/luna", MaxRetries: 2},
		MaxWinners: 2, DryRun: true, ScheduleCron: "30 6 * * 2-6",
	}

	if err := PersistRun(context.Background(), repo, cfg, result, startedAt); err != nil {
		t.Fatalf("PersistRun() error = %v", err)
	}
	if repo.startedAt != startedAt || repo.duration != result.Duration || repo.candidates != 12 || repo.deployed != 2 {
		t.Fatalf("persisted metadata = %#v", repo)
	}
	if strings.Contains(string(repo.config), "Provider") || strings.Contains(string(repo.config), "Metrics") {
		t.Fatalf("runtime-only dependencies leaked into config: %s", repo.config)
	}
	if !strings.Contains(string(repo.config), `"kind":"options"`) || !strings.Contains(string(repo.config), `"model":"openai/luna"`) || !strings.Contains(string(repo.result), `"generation_evidence":[{"ticker":"NVDA"`) {
		t.Fatalf("missing config/result evidence: config=%s result=%s", repo.config, repo.result)
	}
	for _, evidence := range []string{`"schema_version":2`, `"score":null`, `"score_non_finite":"nan"`, `"cost_usd_non_finite":"+inf"`} {
		if !strings.Contains(string(repo.result), evidence) {
			t.Fatalf("result missing %s: %s", evidence, repo.result)
		}
	}
	var roundTrip OptionsDiscoveryResult
	if err := json.Unmarshal(repo.result, &roundTrip); err != nil {
		t.Fatalf("unmarshal persisted result: %v", err)
	}
	if !math.IsNaN(roundTrip.Winners[0].Score) || !math.IsInf(roundTrip.GenerationEvidence[0].Attempts[0].CostUSD, 1) {
		t.Fatalf("non-finite evidence did not round trip: %#v", roundTrip)
	}
	if !math.IsInf(roundTrip.Winners[0].InSample.SortinoRatio, -1) || !math.IsNaN(roundTrip.Winners[0].InSample.StartEquity) || roundTrip.Winners[0].InSample.OrderFills != 77 {
		t.Fatalf("nested backtest metrics did not round trip: %#v", roundTrip.Winners[0].InSample)
	}
}

func TestOptionsWinnerFiniteScoreRoundTrips(t *testing.T) {
	input := OptionsDeployedStrategy{Ticker: "SPY", Score: 3.5}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "score_non_finite") || !strings.Contains(string(encoded), `"score":3.5`) {
		t.Fatalf("finite score encoding = %s", encoded)
	}
	var output OptionsDeployedStrategy
	if err := json.Unmarshal(encoded, &output); err != nil || output.Score != input.Score {
		t.Fatalf("finite score round trip = %#v, %v", output, err)
	}
}

func TestOptionsWinnerRejectsMalformedNonFiniteEncoding(t *testing.T) {
	for _, input := range []string{
		`{"score":null}`,
		`{"score":2,"score_non_finite":"-inf"}`,
		`{"score":null,"score_non_finite":"infinity"}`,
		`{"score_non_finite":"nan"}`,
	} {
		var winner OptionsDeployedStrategy
		if err := json.Unmarshal([]byte(input), &winner); err == nil {
			t.Fatalf("json.Unmarshal(%s) error = nil", input)
		}
	}
}

func TestPersistOptionsRunFailsVisible(t *testing.T) {
	if err := PersistRun(context.Background(), nil, OptionsDiscoveryConfig{}, &OptionsDiscoveryResult{}, time.Now()); err == nil {
		t.Fatal("PersistRun() nil repository error = nil")
	}
	if err := PersistRun(context.Background(), &captureOptionsRunRepository{}, OptionsDiscoveryConfig{}, nil, time.Now()); err == nil {
		t.Fatal("PersistRun() nil result error = nil")
	}
	repo := &captureOptionsRunRepository{err: errors.New("write failed")}
	if err := PersistRun(context.Background(), repo, OptionsDiscoveryConfig{}, &OptionsDiscoveryResult{}, time.Now()); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("PersistRun() error = %v, want write failure", err)
	}
}

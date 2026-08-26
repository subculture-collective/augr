package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
)

type captureRunRepository struct {
	config     json.RawMessage
	result     json.RawMessage
	startedAt  time.Time
	duration   time.Duration
	candidates int
	deployed   int
	err        error
}

func (r *captureRunRepository) Create(_ context.Context, config, result json.RawMessage, startedAt time.Time, duration time.Duration, candidates, deployed int) error {
	r.config, r.result, r.startedAt, r.duration, r.candidates, r.deployed = config, result, startedAt, duration, candidates, deployed
	return r.err
}

func (*captureRunRepository) List(context.Context, int, int) ([]DiscoveryRun, error) { return nil, nil }

func (*captureRunRepository) Count(context.Context) (int, error) { return 0, nil }

func TestPersistRunStoresConfigAndCompleteResult(t *testing.T) {
	repo := &captureRunRepository{}
	startedAt := time.Now().Add(-time.Minute).UTC()
	result := &DiscoveryResult{
		Candidates: 30, Generated: 29, Swept: 28, Validated: 6, Deployed: 3, Duration: 2 * time.Minute, Errors: []string{"one"},
		CandidateEvidence:  []CandidateEvidence{{Ticker: "AAPL", Close: math.NaN(), ADV: math.Inf(1), ATR: math.Inf(-1)}},
		GenerationEvidence: []GenerationEvidence{{Ticker: "AAPL", Attempts: []GenerationAttemptEvidence{{CostUSD: math.NaN()}}}},
		SweepEvidence:      []SweepEvidence{{Ticker: "AAPL", Score: math.Inf(-1), Metrics: backtest.Metrics{SharpeRatio: math.NaN(), ProfitFactor: math.Inf(1), TotalBars: 123}}},
		ValidationEvidence: []ValidationEvidence{{Ticker: "AAPL", OOSRatio: math.Inf(1)}},
		Winners:            []DeployedStrategy{{Ticker: "AAPL", Score: 1.25}},
	}
	cfg := DiscoveryConfig{
		Screener:   ScreenerConfig{Tickers: []string{"AAPL"}, MinADV: 123},
		Generator:  GeneratorConfig{Model: "test-model", MaxRetries: 2},
		Sweep:      SweepConfig{InitialCash: 50_000, Variations: 7},
		MaxWinners: 3,
		DryRun:     true,
	}

	if err := PersistRun(context.Background(), repo, cfg, result, startedAt); err != nil {
		t.Fatalf("PersistRun() error = %v", err)
	}
	if repo.startedAt != startedAt || repo.duration != result.Duration || repo.candidates != 30 || repo.deployed != 3 {
		t.Fatalf("persisted metadata = %#v", repo)
	}
	if strings.Contains(string(repo.config), "Provider") || strings.Contains(string(repo.config), "Metrics") {
		t.Fatalf("runtime-only dependencies leaked into config: %s", repo.config)
	}
	if !strings.Contains(string(repo.config), `"model":"test-model"`) || !strings.Contains(string(repo.result), `"errors":["one"]`) {
		t.Fatalf("missing config/result evidence: config=%s result=%s", repo.config, repo.result)
	}
	for _, evidence := range []string{
		`"schema_version":2`, `"close":null`, `"close_non_finite":"nan"`,
		`"adv_non_finite":"+inf"`, `"atr_non_finite":"-inf"`,
		`"cost_usd_non_finite":"nan"`, `"score_non_finite":"-inf"`,
		`"oos_ratio_non_finite":"+inf"`, `"score":1.25`,
	} {
		if !strings.Contains(string(repo.result), evidence) {
			t.Fatalf("result missing %s: %s", evidence, repo.result)
		}
	}
	var roundTrip DiscoveryResult
	if err := json.Unmarshal(repo.result, &roundTrip); err != nil {
		t.Fatalf("unmarshal persisted result: %v", err)
	}
	if !math.IsNaN(roundTrip.CandidateEvidence[0].Close) || !math.IsInf(roundTrip.CandidateEvidence[0].ADV, 1) || !math.IsInf(roundTrip.SweepEvidence[0].Score, -1) {
		t.Fatalf("non-finite evidence did not round trip: %#v", roundTrip)
	}
	if !math.IsNaN(roundTrip.SweepEvidence[0].Metrics.SharpeRatio) || !math.IsInf(roundTrip.SweepEvidence[0].Metrics.ProfitFactor, 1) || roundTrip.SweepEvidence[0].Metrics.TotalBars != 123 {
		t.Fatalf("nested backtest metrics did not round trip: %#v", roundTrip.SweepEvidence[0].Metrics)
	}
	if roundTrip.Winners[0].Score != 1.25 {
		t.Fatalf("finite score = %v, want 1.25", roundTrip.Winners[0].Score)
	}
}

func TestDiscoveryEvidenceRejectsMalformedNonFiniteEncoding(t *testing.T) {
	for _, input := range []string{
		`{"score":null}`,
		`{"score":1,"score_non_finite":"nan"}`,
		`{"score":null,"score_non_finite":"infinity"}`,
		`{"score_non_finite":"+inf"}`,
	} {
		var evidence SweepEvidence
		if err := json.Unmarshal([]byte(input), &evidence); err == nil {
			t.Fatalf("json.Unmarshal(%s) error = nil", input)
		}
	}
}

func TestDiscoveryResultFiniteEvidenceRoundTrips(t *testing.T) {
	input := DiscoveryResult{
		CandidateEvidence:  []CandidateEvidence{{Ticker: "AAPL", Close: 10.5, ADV: 20.5, ATR: 1.5}},
		GenerationEvidence: []GenerationEvidence{{Ticker: "AAPL", Attempts: []GenerationAttemptEvidence{{CostUSD: 0.25}}}},
		SweepEvidence:      []SweepEvidence{{Ticker: "AAPL", Score: 2.5}},
		ValidationEvidence: []ValidationEvidence{{Ticker: "AAPL", OOSRatio: 0.75}},
		Winners:            []DeployedStrategy{{Ticker: "AAPL", Score: 3.5}},
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "_non_finite") {
		t.Fatalf("finite result contains sentinel: %s", encoded)
	}
	var output DiscoveryResult
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output, input) {
		t.Fatalf("finite result round trip = %#v, want %#v", output, input)
	}
}

func TestPersistRunFailsVisible(t *testing.T) {
	if err := PersistRun(context.Background(), nil, DiscoveryConfig{}, &DiscoveryResult{}, time.Now()); err == nil {
		t.Fatal("PersistRun() nil repository error = nil")
	}
	repo := &captureRunRepository{err: errors.New("write failed")}
	if err := PersistRun(context.Background(), repo, DiscoveryConfig{}, &DiscoveryResult{}, time.Now()); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("PersistRun() error = %v, want write failure", err)
	}
}

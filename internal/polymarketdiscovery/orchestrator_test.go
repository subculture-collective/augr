package polymarketdiscovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/llm"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// --- fakes ----------------------------------------------------------------

type fakeLLM struct {
	resp string
	err  error
}

func (f *fakeLLM) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.CompletionResponse{Content: f.resp}, nil
}

type fakeStrategyRepo struct {
	created []domain.Strategy
	theses  map[uuid.UUID]json.RawMessage
}

func (f *fakeStrategyRepo) Create(ctx context.Context, s *domain.Strategy) error {
	f.created = append(f.created, *s)
	return nil
}
func (f *fakeStrategyRepo) Get(ctx context.Context, id uuid.UUID) (*domain.Strategy, error) {
	return nil, repository.ErrNotFound
}
func (f *fakeStrategyRepo) List(ctx context.Context, _ repository.StrategyFilter, _, _ int) ([]domain.Strategy, error) {
	return f.created, nil
}
func (f *fakeStrategyRepo) Count(ctx context.Context, _ repository.StrategyFilter) (int, error) {
	return len(f.created), nil
}
func (f *fakeStrategyRepo) Update(ctx context.Context, _ *domain.Strategy) error { return nil }
func (f *fakeStrategyRepo) Delete(ctx context.Context, _ uuid.UUID) error        { return nil }
func (f *fakeStrategyRepo) UpdateThesis(ctx context.Context, id uuid.UUID, t json.RawMessage) error {
	if f.theses == nil {
		f.theses = map[uuid.UUID]json.RawMessage{}
	}
	f.theses[id] = t
	return nil
}
func (f *fakeStrategyRepo) GetThesisRaw(ctx context.Context, id uuid.UUID) (json.RawMessage, error) {
	return f.theses[id], nil
}

// --- tests ----------------------------------------------------------------

func TestValidateProposal(t *testing.T) {
	good := &Proposal{
		Template:      TemplateConvergence,
		Name:          "Convergence on X",
		Summary:       "X is 0.9 with no remaining catalyst.",
		Direction:     "YES",
		Conviction:    0.7,
		TimeHorizon:   "days",
		EntryPriceMax: 0.95,
		WatchTerms:    []string{"X resolution"},
	}
	if err := validateProposal(good); err != nil {
		t.Fatalf("good proposal rejected: %v", err)
	}

	bad := &Proposal{Template: "bogus", Name: "n", Summary: "s", Direction: "YES",
		Conviction: 0.5, TimeHorizon: "days", WatchTerms: []string{"a"}}
	if err := validateProposal(bad); err == nil {
		t.Fatal("expected error for invalid template")
	}

	skip := &Proposal{Skip: true}
	if err := validateProposal(skip); err == nil {
		t.Fatal("expected error for skip without reason")
	}
	skip.SkipReason = "thin liquidity"
	if err := validateProposal(skip); err != nil {
		t.Fatalf("valid skip rejected: %v", err)
	}
}

func TestScreenMarkets_FiltersByLiquidityAndVolume(t *testing.T) {
	now := time.Now()
	in := []GammaMarket{
		{
			Slug: "good", AcceptingOrders: true,
			Outcomes:   gammaString(`["Yes","No"]`),
			Volume24Hr: gammaString("20000"), Liquidity: gammaString("10000"),
			EndDate: now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
		},
		{
			Slug: "thin", AcceptingOrders: true,
			Outcomes:   gammaString(`["Yes","No"]`),
			Volume24Hr: gammaString("100"), Liquidity: gammaString("100"),
			EndDate: now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
		},
		{
			Slug: "closed", Closed: true, AcceptingOrders: false,
			Outcomes:   gammaString(`["Yes","No"]`),
			Volume24Hr: gammaString("999999"), Liquidity: gammaString("999999"),
			EndDate: now.Add(30 * 24 * time.Hour).Format(time.RFC3339),
		},
	}
	out := ScreenMarkets(in, DefaultScreenerConfig())
	if len(out) != 1 || out[0].Slug != "good" {
		t.Fatalf("unexpected screened set: %+v", out)
	}
}

func TestRun_HappyPath(t *testing.T) {
	now := time.Now()
	// Mock Gamma API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		markets := []GammaMarket{{
			Slug:            "test-market",
			Question:        "Will X happen?",
			AcceptingOrders: true,
			Outcomes:        gammaString(`["Yes","No"]`),
			Volume24Hr:      gammaString("50000"),
			Liquidity:       gammaString("20000"),
			EndDate:         now.Add(20 * 24 * time.Hour).Format(time.RFC3339),
		}}
		_ = json.NewEncoder(w).Encode(markets)
	}))
	defer srv.Close()

	prop := Proposal{
		Template:      TemplateConvergence,
		Name:          "Convergence on X",
		Summary:       "Strong convergence setup.",
		Direction:     "YES",
		Conviction:    0.75,
		TimeHorizon:   "days",
		EntryPriceMax: 0.95,
		WatchTerms:    []string{"X resolution", "X ruling"},
		InvalidateIf:  []string{"X is overturned"},
	}
	propJSON, _ := json.Marshal(prop)

	repo := &fakeStrategyRepo{}
	cfg := Config{
		GammaBaseURL:   srv.URL,
		Screener:       DefaultScreenerConfig(),
		MaxDeployments: 1,
		MinConviction:  0.5,
	}
	res, err := Run(context.Background(), cfg, Deps{
		LLMProvider: &fakeLLM{resp: string(propJSON)},
		Strategies:  repo,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(res.Deployed) != 1 {
		t.Fatalf("expected 1 deployed, got %d (errors: %v)", len(res.Deployed), res.Errors)
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 created strategy, got %d", len(repo.created))
	}
	created := repo.created[0]
	if created.MarketType != domain.MarketTypePolymarket {
		t.Fatalf("market_type=%q", created.MarketType)
	}
	if created.Ticker != "test-market" {
		t.Fatalf("ticker=%q", created.Ticker)
	}
	if !created.IsPaper {
		t.Fatal("strategy should be paper")
	}
	if got := repo.theses[created.ID]; len(got) == 0 {
		t.Fatal("thesis was not persisted")
	}
}

func TestRun_SkipBelowConviction(t *testing.T) {
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		markets := []GammaMarket{{
			Slug: "mkt", Question: "Q",
			AcceptingOrders: true,
			Outcomes:        gammaString(`["Yes","No"]`),
			Volume24Hr:      gammaString("50000"), Liquidity: gammaString("20000"),
			EndDate: now.Add(10 * 24 * time.Hour).Format(time.RFC3339),
		}}
		_ = json.NewEncoder(w).Encode(markets)
	}))
	defer srv.Close()

	prop := Proposal{
		Template: TemplateConvergence, Name: "Low conv", Summary: "weak",
		Direction: "YES", Conviction: 0.2, TimeHorizon: "days",
		EntryPriceMax: 0.5, WatchTerms: []string{"x"},
	}
	propJSON, _ := json.Marshal(prop)

	repo := &fakeStrategyRepo{}
	res, err := Run(context.Background(), Config{
		GammaBaseURL: srv.URL, MinConviction: 0.5,
	}, Deps{LLMProvider: &fakeLLM{resp: string(propJSON)}, Strategies: repo})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Deployed) != 0 {
		t.Fatalf("expected 0 deployed, got %d", len(res.Deployed))
	}
	if res.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", res.Skipped)
	}
}

func TestFetchOpenMarkets_DecodesNumericFields(t *testing.T) {
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"slug":"numeric-market","question":"Q","acceptingOrders":true,"outcomes":"[\"Yes\",\"No\"]","volume":12345.67,"volume24hr":50000,"liquidity":20000,"endDate":"` + now.Add(20*24*time.Hour).Format(time.RFC3339) + `"}]`))
	}))
	defer srv.Close()

	markets, err := FetchOpenMarkets(context.Background(), srv.URL, 1)
	if err != nil {
		t.Fatalf("FetchOpenMarkets error: %v", err)
	}
	if len(markets) != 1 {
		t.Fatalf("expected 1 market, got %d", len(markets))
	}
	if got := markets[0].Volume24HrFloat(); got != 50000 {
		t.Fatalf("Volume24HrFloat = %v, want 50000", got)
	}
	if !markets[0].IsBinaryYesNo() {
		t.Fatal("expected binary yes/no outcomes")
	}
}

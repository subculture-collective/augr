package postgres

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

func TestGeneratedProposalEvidenceReconstructsExactDailyScope(t *testing.T) {
	fixture := newStrategyCatalogFixture(t)
	for _, migration := range []string{"000095_typed_generative_strategy_compiler.up.sql", "000106_paper_evaluation_scopes.up.sql", "000110_immutable_market_payloads.up.sql"} {
		if _, err := fixture.pool.Exec(fixture.ctx, repositoryMigrationSQL(t, migration)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	instrumentID := datasetManifestInstrumentID(t, fixture.manifest)
	start := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	end := start.Add(270 * 24 * time.Hour)
	cutoff := end.Add(time.Hour)
	folds, err := generativestrategy.PlanReviewedResearchFolds(start, end)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := instrument.NewVenueContract(instrument.VenueContractInput{
		InstrumentID: instrumentID, Venue: "alpaca", ContractID: "SPY", Currency: "USD",
		TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.NewFromInt(1), Multiplier: decimal.NewFromInt(1),
		SettlementMethod: instrument.SettlementPhysical, ValidFrom: start.Add(-time.Hour), CreatedAt: start.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewInstrumentRepo(fixture.pool).RegisterVenueContract(fixture.ctx, contract); err != nil {
		t.Fatal(err)
	}
	payloads := make([]*dataset.MarketPayload, 0, 2)
	observations := make([]dataset.ObservationInput, 0, 2)
	for index, at := range []time.Time{start, folds[0].TrainEnd, end} {
		publishedAt := at
		payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
			Kind: dataset.MarketPayloadStockBar, InstrumentID: instrumentID, Provider: "alpaca", Feed: "sip", Symbol: "SPY",
			Timeframe: "1d", AdjustmentPolicy: "raw", EffectiveAt: at, PublishedAt: &publishedAt, ObservedAt: cutoff, AvailableAt: cutoff,
			Revision: "original", Bar: &dataset.BarPayload{Open: "500", High: "503", Low: "498", Close: fmt.Sprintf("%d", 501+index), Volume: "1000", TradeCount: "100", VWAP: "500.5"},
		})
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
		observations = append(observations, dataset.ObservationInput{
			SourceKey: fmt.Sprintf("SPY/1d/%d", index), InstrumentID: instrumentID, EffectiveAt: at,
			PublishedAt: &publishedAt, ObservedAt: cutoff, AvailableAt: cutoff, Revision: "original", ContentSHA256: payload.Digest(),
		})
	}
	manifest, err := dataset.NewManifest(dataset.ManifestInput{DecisionCutoff: cutoff, Partitions: []dataset.PartitionInput{{
		Kind: dataset.KindBars, Provider: "alpaca", Source: "historical_api", Namespace: "promotion/stock/SPY",
		RequestSHA256: strings.Repeat("a", 64), MediaType: "application/json", SymbologyVersion: "alpaca-v1",
		AdjustmentPolicy: "raw", Timezone: "UTC", Calendar: "XNYS", Revision: "original",
		License: "test", RetentionPolicy: "indefinite", Observations: observations,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := dataset.NewBoundMarketDataset(manifest, payloads)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := dataset.NewPolicy(dataset.ReviewedPolicyV1Input())
	if err != nil {
		t.Fatal(err)
	}
	datasets := NewDatasetRepo(fixture.pool)
	if _, err := datasets.RecordBoundMarketDataset(fixture.ctx, bound, cutoff); err != nil {
		t.Fatal(err)
	}
	quality := datasetRepositoryQuality(t, policy, manifest, instrumentID, false)
	if _, err := datasets.RecordDatasetQualityResult(fixture.ctx, quality, cutoff); err != nil {
		t.Fatal(err)
	}
	simulationArtifact, err := NewSimulationPolicyRepo(fixture.pool).GetSimulationPolicyByVersion(fixture.ctx, fixture.simulation)
	if err != nil {
		t.Fatal(err)
	}
	capitalArtifact, err := NewCapitalPolicyRepo(fixture.pool).GetCapitalPolicyByVersion(fixture.ctx, fixture.capital)
	if err != nil {
		t.Fatal(err)
	}
	scope := &PaperEvaluationScope{
		AccountID: fixture.account.ID, CapitalBindingID: fixture.binding.ID, ManifestSHA256: manifest.Digest(), QualitySHA256: quality.Digest(),
		SimulationPolicySHA256: simulationArtifact.SHA256, CapitalPolicySHA256: capitalArtifact.SHA256,
		EvaluationStart: start, EvaluationEnd: end,
	}
	if err := NewReportArtifactRepo(fixture.pool).RegisterScope(fixture.ctx, scope); err != nil {
		t.Fatal(err)
	}
	family, err := generativestrategy.ReviewedDailyStockFamily()
	if err != nil {
		t.Fatal(err)
	}
	items, err := NewGenerativeStrategyRepo(fixture.pool).ListEligibleGeneratedProposalEvidence(fixture.ctx, fixture.account.ID, scope.ID, family.ID(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Universe.Benchmark != instrumentID || len(items[0].Universe.Instruments) != 1 || len(items[0].AllowedDataFields) != 7 {
		t.Fatalf("items=%+v", items)
	}
	var summary generatedDailyStockEvidenceSummary
	if err := json.Unmarshal([]byte(items[0].ImmutableSummary), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ScopeID != scope.ID.String() || summary.ManifestSHA256 != manifest.Digest() || summary.QualitySHA256 != quality.Digest() ||
		len(summary.Instruments) != 1 || summary.Instruments[0].LatestClose != "502" || summary.Instruments[0].ContentSetSHA256 == "" {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := NewGenerativeStrategyRepo(fixture.pool).ListEligibleGeneratedProposalEvidence(fixture.ctx, uuid.New(), scope.ID, family.ID(), 1); err == nil {
		t.Fatal("cross-account proposal evidence was accepted")
	}

	key := "daily_stock_" + strings.ReplaceAll(scope.ID.String(), "-", "")
	proposalService, err := generativestrategy.NewProposalService(NewGenerativeStrategyRepo(fixture.pool))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := proposalService.Propose(fixture.ctx, generativestrategy.ProposalRequest{
		Input: generativestrategy.SpecInput{
			Family: family, SpecKey: key,
			Inputs:   []generativestrategy.InputField{{Name: "price", Type: "decimal", DatasetKind: dataset.KindBars, Field: "close", FreshnessSeconds: 86400, MissingPolicy: "abstain"}},
			Universe: generativestrategy.Universe{AssetClass: instrument.AssetClassEquity, Instruments: []uuid.UUID{instrumentID}, Benchmark: instrumentID},
			Entry:    generativestrategy.Expr{Op: "gt", Args: []generativestrategy.Expr{{Op: "ref", Ref: "price"}, {Op: "decimal", Value: "500"}}},
			Exit:     generativestrategy.Expr{Op: "lt", Args: []generativestrategy.Expr{{Op: "ref", Ref: "price"}, {Op: "decimal", Value: "500"}}},
			Sizing:   generativestrategy.Sizing{Mode: "fixed_fraction", Value: "0.01", MaxPosition: "0.02"}, MaximumHoldingSeconds: 86400,
			Costs:               generativestrategy.Costs{SpreadBPS: "1", FeeBPS: "1", SlippageBPS: "1"},
			Capacity:            generativestrategy.Capacity{MaximumDailyTurnover: "10000", MaximumParticipation: "0.01"},
			ProhibitedBehaviors: []string{"evidence_mutation", "live_order_submission", "lookahead", "network_access", "promotion", "risk_limit_mutation", "secret_access"},
			PropertyTests:       []string{"cost_hurdle_required", "missing_input_abstains", "no_lookahead", "size_bounded", "stale_input_abstains"},
			ExampleTests:        []generativestrategy.ExampleTest{{Key: "entry", Values: map[string]string{"price": "501"}, ExpectedEntry: true}},
			Retirement:          generativestrategy.Retirement{MaximumDrawdown: "0.2", MinimumSamples: 10, MaximumFailedChecks: 1},
			Authoring:           generativestrategy.Authoring{Provider: "openai", Model: "test", PromptSHA256: strings.Repeat("b", 64), InputTokens: 1, OutputTokens: 1, Currency: "USD", Cost: "0"},
		},
		SourceCommit: strings.Repeat("c", 40), SourceTreeSHA256: strings.Repeat("d", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewGenerativeStrategyRepo(fixture.pool)
	preparations, err := repo.ListEligibleGeneratedResearchPreparations(fixture.ctx, fixture.account.ID, scope.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(preparations) != 1 || preparations[0].Request.SpecID != proposal.Spec.ID() || preparations[0].Request.ExpectedVersionID != proposal.Version.ID() ||
		preparations[0].Request.ExecutionInput != "price" || preparations[0].Request.VenueContractIDs[instrumentID] != contract.ID || preparations[0].Request.Dataset.Manifest().ID() != manifest.ID() {
		t.Fatalf("preparations=%+v", preparations)
	}
	if _, err := repo.ListEligibleGeneratedResearchPreparations(fixture.ctx, uuid.New(), scope.ID, 1); err == nil {
		t.Fatal("cross-account research preparation was accepted")
	}
}

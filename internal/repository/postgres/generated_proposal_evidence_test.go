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
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/generativestrategy"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
)

func TestGeneratedProposalEvidenceReconstructsExactDailyScope(t *testing.T) {
	fixture := newStrategyCatalogFixture(t)
	for _, migration := range []string{
		"000078_reproducible_experiment_runs.up.sql", "000079_trade_portfolio_evaluations.up.sql",
		"000080_statistical_robustness_assessments.up.sql", "000095_typed_generative_strategy_compiler.up.sql",
		"000106_paper_evaluation_scopes.up.sql", "000107_robustness_assessment_scope.up.sql",
		"000110_immutable_market_payloads.up.sql",
	} {
		if _, err := fixture.pool.Exec(fixture.ctx, repositoryMigrationSQL(t, migration)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	scenarioMigration := repositoryMigrationSQL(t, "000111_portfolio_risk_and_activation.up.sql")
	startScenario := strings.Index(scenarioMigration, "CREATE TABLE generated_strategy_scenarios (")
	endScenario := strings.Index(scenarioMigration, "CREATE FUNCTION validate_portfolio_risk_binding()")
	if startScenario < 0 || endScenario <= startScenario {
		t.Fatal("migration 111 generated scenario boundary is missing")
	}
	if _, err := fixture.pool.Exec(fixture.ctx, scenarioMigration[startScenario:endScenario]); err != nil {
		t.Fatalf("apply migration 111 generated scenario schema: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `ALTER TABLE strategies ADD COLUMN execution_strategy_version_id UUID REFERENCES strategy_versions(id) ON DELETE RESTRICT`); err != nil {
		t.Fatal(err)
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
	payloads := make([]*dataset.MarketPayload, 0, 5)
	observations := make([]dataset.ObservationInput, 0, 5)
	for index, at := range []time.Time{start, folds[0].TrainEnd, folds[0].TestStart, folds[1].TestStart, end} {
		publishedAt := at
		payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
			Kind: dataset.MarketPayloadStockBar, InstrumentID: instrumentID, Provider: "alpaca", Feed: "sip", Symbol: "SPY",
			Timeframe: "1d", AdjustmentPolicy: "raw", EffectiveAt: at, PublishedAt: &publishedAt, ObservedAt: cutoff, AvailableAt: cutoff,
			Revision: "original", Bar: &dataset.BarPayload{Open: "500", High: "510", Low: "498", Close: fmt.Sprintf("%d", 501+index), Volume: "1000", TradeCount: "100", VWAP: "500.5"},
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
	if len(items) != 1 || items[0].Universe.Benchmark != instrumentID || len(items[0].Universe.Instruments) != 1 || len(items[0].AllowedDataFields) != 5 {
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

	key, err := generativestrategy.ReviewedDailyStockSpecKey(scope.ID, instrumentID)
	if err != nil {
		t.Fatal(err)
	}
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
	runtimeBinding, err := generativestrategy.NewRuntimeBinding(proposal.Spec, proposal.Version, "0.025", "0.91")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		tx, err := fixture.pool.Begin(fixture.ctx)
		if err != nil {
			t.Fatal(err)
		}
		draft, err := ensureGeneratedRuntimeStrategyTx(fixture.ctx, tx, fixture.account.ID, manifest.ID(), runtimeBinding)
		if err != nil {
			_ = tx.Rollback(fixture.ctx)
			t.Fatal(err)
		}
		if draft.Status != domain.StrategyStatusInactive || draft.ScheduleCron != "" || !draft.IsPaper || draft.ExecutionStrategyVersionID == nil || *draft.ExecutionStrategyVersionID != proposal.Version.ID() {
			_ = tx.Rollback(fixture.ctx)
			t.Fatalf("draft=%+v", draft)
		}
		if _, err := generativestrategy.ParseRuntimeBinding(draft.Config); err != nil {
			_ = tx.Rollback(fixture.ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(fixture.ctx); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewGenerativeStrategyRepo(fixture.pool)
	preparations, err := repo.ListEligibleGeneratedResearchPreparations(fixture.ctx, fixture.account.ID, scope.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(preparations) != 4 || preparations[0].Request.SpecID != proposal.Spec.ID() || preparations[0].Request.ExpectedVersionID != proposal.Version.ID() ||
		preparations[0].Request.ExecutionInput != "price" || preparations[0].Request.VenueContractIDs[instrumentID] != contract.ID || preparations[0].Request.Dataset.Manifest().ID() != manifest.ID() {
		t.Fatalf("preparations=%+v", preparations)
	}
	wantKeys := []string{
		proposal.Spec.ID().String() + "/fold-0/baseline",
		proposal.Spec.ID().String() + "/fold-0/cost_up",
		proposal.Spec.ID().String() + "/fold-1/baseline",
		proposal.Spec.ID().String() + "/fold-1/cost_up",
	}
	for index, preparation := range preparations {
		fold := folds[index/2]
		if preparation.Key != wantKeys[index] || !preparation.Request.EvaluationStart.Equal(fold.TestStart) || !preparation.Request.EvaluationEnd.Equal(fold.TestEnd) {
			t.Fatalf("preparation[%d]=%+v", index, preparation)
		}
		if index%2 == 0 {
			if preparation.Request.SimulationPolicyArtifact != nil || preparation.Request.SimulationPolicyVersion != fixture.simulation {
				t.Fatalf("baseline preparation[%d]=%+v", index, preparation)
			}
		} else if preparation.Request.SimulationPolicyArtifact == nil || preparation.Request.SimulationPolicyVersion == fixture.simulation ||
			preparation.Request.SimulationPolicyVersion != preparation.Request.SimulationPolicyArtifact.Version {
			t.Fatalf("cost-up preparation[%d]=%+v", index, preparation)
		}
	}
	if preparations[1].Request.SimulationPolicyVersion != preparations[3].Request.SimulationPolicyVersion {
		t.Fatal("folds derived different cost-up policy identities")
	}
	preparer, err := generativestrategy.NewResearchPreparer(repo)
	if err != nil {
		t.Fatal(err)
	}
	for index := range preparations {
		if _, err := preparer.Prepare(fixture.ctx, preparations[index].Request); err != nil {
			t.Fatalf("prepare research[%d]: %v", index, err)
		}
	}
	remaining, err := repo.ListEligibleGeneratedResearchPreparations(fixture.ctx, fixture.account.ID, scope.ID, 20)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining preparations=%+v error=%v", remaining, err)
	}
	research, err := repo.ListEligibleGeneratedResearch(fixture.ctx, fixture.account.ID, scope.ID, 20, cutoff.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(research) != 4 {
		t.Fatalf("eligible research=%+v", research)
	}
	for index, item := range research {
		fold := folds[index/2]
		if item.Prepared == nil || !item.Prepared.Experiment.EvaluationStart().Equal(fold.TestStart) || !item.Prepared.Experiment.EvaluationEnd().Equal(fold.TestEnd) {
			t.Fatalf("eligible research[%d]=%+v", index, item)
		}
	}
	robustnessItems, err := repo.ListEligibleGeneratedRobustness(fixture.ctx, fixture.account.ID, scope.ID, 20)
	if err != nil || len(robustnessItems) != 0 {
		t.Fatalf("incomplete robustness items=%+v error=%v", robustnessItems, err)
	}
	deploymentItems, err := repo.ListEligibleGeneratedDeployments(fixture.ctx, fixture.account.ID, scope.ID, 20)
	if err != nil || len(deploymentItems) != 0 {
		t.Fatalf("premature deployment items=%+v error=%v", deploymentItems, err)
	}
	outsideReviewedFold := preparations[0].Request
	outsideReviewedFold.EvaluationStart = start
	outsideReviewedFold.EvaluationEnd = end
	outsideReviewedFold.Seed++
	if _, err := preparer.Prepare(fixture.ctx, outsideReviewedFold); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListEligibleGeneratedResearch(fixture.ctx, fixture.account.ID, scope.ID, 20, cutoff.Add(time.Hour)); err == nil {
		t.Fatal("non-fold generated research was admitted")
	}
	if _, err := repo.ListEligibleGeneratedResearchPreparations(fixture.ctx, uuid.New(), scope.ID, 1); err == nil {
		t.Fatal("cross-account research preparation was accepted")
	}
}

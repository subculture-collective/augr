package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/google/uuid"
)

func TestConfiguredScopeEnablesStockAndKeepsOptionsFailClosed(t *testing.T) {
	fixture := newStrategyCatalogFixture(t)
	for _, migration := range []string{"000106_paper_evaluation_scopes.up.sql", "000110_immutable_market_payloads.up.sql"} {
		if _, err := fixture.pool.Exec(fixture.ctx, repositoryMigrationSQL(t, migration)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	instrumentID := datasetManifestInstrumentID(t, fixture.manifest)
	start := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	cutoff := end.Add(time.Hour)
	payloads := make([]*dataset.MarketPayload, 0, 2)
	observations := make([]dataset.ObservationInput, 0, 2)
	for index, at := range []time.Time{start, end} {
		payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
			Kind: dataset.MarketPayloadStockBar, InstrumentID: instrumentID, Provider: "alpaca", Feed: "sip", Symbol: "SPY",
			Timeframe: "1d", AdjustmentPolicy: "raw", EffectiveAt: at, ObservedAt: cutoff, AvailableAt: cutoff,
			Revision: "original", Bar: &dataset.BarPayload{Open: "500", High: "503", Low: "498", Close: "501", Volume: "1000", TradeCount: "100", VWAP: "500.5"},
		})
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
		observations = append(observations, dataset.ObservationInput{
			SourceKey: fmt.Sprintf("SPY/1d/%d", index), InstrumentID: instrumentID, EffectiveAt: at,
			ObservedAt: cutoff, AvailableAt: cutoff, Revision: "original", ContentSHA256: payload.Digest(),
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
	reports := NewReportArtifactRepo(fixture.pool)
	if err := reports.RegisterScope(fixture.ctx, scope); err != nil {
		t.Fatal(err)
	}
	report, err := reports.DiscoveryDeploymentReadinessForScope(fixture.ctx, scope.ID, fixture.account.ID)
	if err != nil || !report.Ready || !report.Stock.Ready || report.Options.Ready || report.BindingCount != 2 || report.ScopeIDRedacted == scope.ID.String() {
		t.Fatalf("readiness = %+v, %v", report, err)
	}
	if _, err := reports.DiscoveryDeploymentReadinessForScope(fixture.ctx, scope.ID, uuid.New()); err == nil {
		t.Fatal("readiness accepted a cross-account scope")
	}
	loader := NewManifestBoundHistoricalLoader(fixture.pool, reports, fixture.account.ID)
	bars, receipt, err := loader.Load(fixture.ctx, scope.ID, instrumentID, data.Timeframe1d, start, end)
	if err != nil || len(bars) != 2 || receipt.ManifestID != manifest.ID() || len(receipt.ContentSHA256) != 2 {
		t.Fatalf("manifest-bound load = %#v, %+v, %v", bars, receipt, err)
	}
	if _, _, err := loader.LoadOptions(fixture.ctx, scope.ID, instrumentID, data.Timeframe1d, start, end); err == nil || !strings.Contains(err.Error(), "options") {
		t.Fatalf("options load error = %v", err)
	}
}

func TestDiscoveryDeploymentReadinessRejectsValidScopeWithoutLoaderBinding(t *testing.T) {
	scope, err := NewPaperEvaluationScope(PaperEvaluationScope{
		AccountID: uuid.New(), CapitalBindingID: uuid.New(), ManifestSHA256: strings.Repeat("1", 64),
		QualitySHA256: strings.Repeat("2", 64), SimulationPolicySHA256: strings.Repeat("3", 64), CapitalPolicySHA256: strings.Repeat("4", 64),
		EvaluationStart: time.Now().Add(-time.Hour), EvaluationEnd: time.Now(),
	})
	if err != nil || scope.CanonicalSHA256 == "" {
		t.Fatalf("valid scope = %+v, err = %v", scope, err)
	}
	ready, reason, err := (&ReportArtifactRepo{}).DiscoveryDeploymentReadiness(context.Background())
	var lock repository.ImmutableBindingLock
	if !errors.Is(err, ErrDiscoveryDeploymentImmutableBinding) || !errors.As(err, &lock) || ready || reason != DiscoveryDeploymentUnavailableReason || lock.Reason() != reason {
		t.Fatalf("readiness = %t, %q, %v", ready, reason, err)
	}
}

func TestNewPaperEvaluationScopeCanonicalIdentityCoversEveryField(t *testing.T) {
	input := PaperEvaluationScope{
		AccountID: uuid.New(), CapitalBindingID: uuid.New(), ManifestSHA256: strings.Repeat("1", 64),
		QualitySHA256: strings.Repeat("2", 64), SimulationPolicySHA256: strings.Repeat("3", 64),
		CapitalPolicySHA256: strings.Repeat("4", 64),
		EvaluationStart:     time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.FixedZone("offset", 3600)),
		EvaluationEnd:       time.Date(2026, 2, 1, 0, 0, 0, 123456789, time.FixedZone("offset", 3600)),
	}
	first, err := NewPaperEvaluationScope(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(first.CanonicalBytes)
	if first.CanonicalSHA256 != fmt.Sprintf("%x", digest) {
		t.Fatal("canonical digest does not hash canonical bytes")
	}
	for _, want := range []string{
		input.AccountID.String(), input.CapitalBindingID.String(), input.ManifestSHA256,
		input.QualitySHA256, input.SimulationPolicySHA256, input.CapitalPolicySHA256, "paper-evaluation-scope-v1",
	} {
		if !bytes.Contains(first.CanonicalBytes, []byte(want)) {
			t.Fatalf("canonical bytes omit %q: %s", want, first.CanonicalBytes)
		}
	}
	changed := input
	changed.CapitalBindingID = uuid.New()
	second, err := NewPaperEvaluationScope(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalSHA256 == second.CanonicalSHA256 || bytes.Equal(first.CanonicalBytes, second.CanonicalBytes) {
		t.Fatal("capital binding did not change scope identity")
	}
	if first.EvaluationStart.Location() != time.UTC || first.EvaluationStart.Nanosecond()%1000 != 0 {
		t.Fatalf("time not normalized: %v", first.EvaluationStart)
	}
}

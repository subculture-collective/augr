package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/dataset"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

func TestRecordBoundMarketDatasetIsAtomicAndIdempotent(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatalf("apply migration 110: %v", err)
	}
	payload, manifest := boundStockPayloadFixture(t, datasetManifestInstrumentID(t, fixture.manifest))
	bound, err := dataset.NewBoundMarketDataset(manifest, []*dataset.MarketPayload{payload})
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop after payload")
	fixture.repo.afterStage = func(stage string) error {
		if stage == "bound_payloads" {
			return stop
		}
		return nil
	}
	if _, err := fixture.repo.RecordBoundMarketDataset(fixture.ctx, bound, fixture.createdAt); !errors.Is(err, stop) {
		t.Fatalf("RecordBoundMarketDataset() error = %v, want %v", err, stop)
	}
	var count int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM dataset_market_payloads WHERE id=$1`, payload.ID()).Scan(&count); err != nil || count != 0 {
		t.Fatalf("payload count after rollback = %d, %v", count, err)
	}
	fixture.repo.afterStage = nil
	stored, err := fixture.repo.RecordBoundMarketDataset(fixture.ctx, bound, fixture.createdAt)
	if err != nil || stored.ID() != manifest.ID() {
		t.Fatalf("RecordBoundMarketDataset() = %+v, %v", stored, err)
	}
	if _, err := fixture.repo.RecordBoundMarketDataset(fixture.ctx, bound, fixture.createdAt); err != nil {
		t.Fatalf("idempotent RecordBoundMarketDataset() error = %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM dataset_manifest_payload_bindings WHERE manifest_id=$1`, manifest.ID()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("binding count = %d, %v", count, err)
	}
	reloaded, err := fixture.repo.LoadBoundMarketDataset(fixture.ctx, manifest.ID())
	if err != nil || reloaded.Manifest().Digest() != manifest.Digest() || len(reloaded.Payloads()) != 1 || reloaded.Payloads()[0].Digest() != payload.Digest() {
		t.Fatalf("LoadBoundMarketDataset() = %+v, %v", reloaded, err)
	}
}

func TestBoundMarketDatasetPreservesExactSourceEvidence(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatal(err)
	}
	for _, exact := range []bool{false, true} {
		payload, manifest := boundStockPayloadSourceFixture(t, datasetManifestInstrumentID(t, fixture.manifest), exact)
		bound, err := dataset.NewBoundMarketDataset(manifest, []*dataset.MarketPayload{payload})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repo.RecordBoundMarketDataset(fixture.ctx, bound, fixture.createdAt); err != nil {
			t.Fatal(err)
		}
		reloaded, err := NewDatasetRepo(fixture.pool).LoadBoundMarketDataset(fixture.ctx, manifest.ID())
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded.Payloads()) != 1 || !bytes.Equal(reloaded.Payloads()[0].CanonicalBytes(), payload.CanonicalBytes()) {
			t.Fatal("source or legacy canonical bytes changed through SQL")
		}
		var raw []byte
		if err := fixture.pool.QueryRow(fixture.ctx, `SELECT canonical_bytes FROM dataset_market_payloads WHERE id=$1`, payload.ID()).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("source_evidence")) != exact {
			t.Fatal("unexpected persisted source envelope presence")
		}
		if _, err := fixture.repo.RecordBoundMarketDataset(fixture.ctx, bound, fixture.createdAt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMarketPayloadRepositoryPersistsBindsAndRejectsMutation(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatalf("apply migration 110: %v", err)
	}
	payload, manifest := boundStockPayloadFixture(t, datasetManifestInstrumentID(t, fixture.manifest))
	stored, err := fixture.repo.RecordMarketPayload(fixture.ctx, payload, fixture.createdAt)
	if err != nil || stored.ID() != payload.ID() || stored.Digest() != payload.Digest() {
		t.Fatalf("record market payload = %+v, %v", stored, err)
	}
	if _, err := fixture.repo.RecordMarketPayload(fixture.ctx, payload, fixture.createdAt); err != nil {
		t.Fatalf("idempotent market payload retry: %v", err)
	}
	if _, err := fixture.repo.RecordDatasetManifest(fixture.ctx, manifest, fixture.createdAt); err != nil {
		t.Fatalf("record bound manifest: %v", err)
	}
	binding := MarketPayloadBinding{
		ManifestID: manifest.ID(), PartitionSequence: 0, ObservationSequence: 0,
		PayloadID: payload.ID(), ContentSHA256: payload.Digest(), CreatedAt: fixture.createdAt,
	}
	first, err := fixture.repo.BindMarketPayload(fixture.ctx, binding)
	if err != nil || first.PayloadID != payload.ID() {
		t.Fatalf("bind market payload = %+v, %v", first, err)
	}
	if _, err := fixture.repo.BindMarketPayload(fixture.ctx, binding); err != nil {
		t.Fatalf("idempotent binding retry: %v", err)
	}
	loaded, err := NewDatasetRepo(fixture.pool).GetMarketPayload(fixture.ctx, payload.ID())
	if err != nil || loaded.Digest() != payload.Digest() {
		t.Fatalf("reload payload = %+v, %v", loaded, err)
	}
	listed, err := fixture.repo.ListBoundMarketPayloads(fixture.ctx, manifest.ID(), dataset.MarketPayloadStockBar)
	if err != nil || len(listed) != 1 || listed[0].ID() != payload.ID() {
		t.Fatalf("listed payloads = %+v, %v", listed, err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE dataset_market_payloads SET symbol=symbol WHERE id=$1`, payload.ID()); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("market payload mutation error = %v", err)
	}
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.down.sql"); err == nil || !strings.Contains(err.Error(), "cannot roll back migration 110") {
		t.Fatalf("nonempty rollback error = %v", err)
	}
}

func TestMarketPayloadBindingRejectsDivergentObservation(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, "000110_immutable_market_payloads.up.sql"); err != nil {
		t.Fatalf("apply migration 110: %v", err)
	}
	payload, manifest := boundStockPayloadFixture(t, datasetManifestInstrumentID(t, fixture.manifest))
	if _, err := fixture.repo.RecordMarketPayload(fixture.ctx, payload, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.RecordDatasetManifest(fixture.ctx, manifest, fixture.createdAt); err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(fixture.ctx, `INSERT INTO dataset_manifest_payload_bindings(
		manifest_id,partition_sequence,observation_sequence,payload_id,content_sha256,created_at
	) VALUES($1,0,0,$2,$3,$4)`, manifest.ID(), payload.ID(), strings.Repeat("0", 64), fixture.createdAt)
	if err == nil {
		err = tx.Commit(fixture.ctx)
	}
	if err == nil {
		t.Fatal("divergent payload binding committed")
	}
}

func TestMarketPayloadMigrationEmptyRollbackAndReapply(t *testing.T) {
	fixture := newDatasetRepoFixture(t)
	for _, migration := range []string{
		"000110_immutable_market_payloads.up.sql",
		"000110_immutable_market_payloads.down.sql",
		"000110_immutable_market_payloads.up.sql",
	} {
		if _, err := execRepositoryMigration(t, fixture.ctx, fixture.pool, migration); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
}

func boundStockPayloadFixture(t *testing.T, instrumentID uuid.UUID) (*dataset.MarketPayload, *dataset.Manifest) {
	return boundStockPayloadSourceFixture(t, instrumentID, false)
}

func boundStockPayloadSourceFixture(t *testing.T, instrumentID uuid.UUID, exact bool) (*dataset.MarketPayload, *dataset.Manifest) {
	t.Helper()
	effective := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 8, 2, 20, 0, 0, 123456000, time.UTC)
	published := effective.Add(time.Minute)
	provider, adjustment := "alpaca", "all"
	var evidence *dataset.SourcePageEvidence
	if exact {
		provider, adjustment = "polygon", "raw"
		row := []byte(fmt.Sprintf(`{ "o":500,"h":503,"l":498,"c":501,"v":1000,"n":100,"vw":500.5,"t":%d }`, effective.UnixMilli()))
		evidence = &dataset.SourcePageEvidence{RequestPath: fmt.Sprintf("/v2/aggs/ticker/SPY/range/1/day/%d/%d", effective.UnixMilli(), effective.UnixMilli()), Query: "adjusted=false&sort=asc", Row: row, Page: append(append([]byte(`{"results":[`), row...), []byte(`]}`)...)}
	}
	payload, err := dataset.NewMarketPayload(dataset.MarketPayloadInput{
		SourceEvidence: evidence,
		Kind:           dataset.MarketPayloadStockBar, InstrumentID: instrumentID, Provider: provider, Feed: "sip",
		Symbol: "SPY", Timeframe: "1Day", AdjustmentPolicy: adjustment, EffectiveAt: effective,
		PublishedAt: &published, ObservedAt: observed, AvailableAt: observed, Revision: "original",
		Bar: &dataset.BarPayload{Open: "500", High: "503", Low: "498", Close: "501", Volume: "1000", TradeCount: "100", VWAP: "500.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := dataset.NewManifest(dataset.ManifestInput{
		DecisionCutoff: observed,
		Partitions: []dataset.PartitionInput{{
			Kind: dataset.KindBars, Provider: provider, Source: "historical-bars", Namespace: "promotion/stock/SPY/1Day",
			RequestSHA256: strings.Repeat("a", 64), MediaType: "application/json", SymbologyVersion: "alpaca-v1",
			AdjustmentPolicy: adjustment, Timezone: "America/New_York", Calendar: "XNYS", Revision: "original",
			License: "alpaca-market-data", RetentionPolicy: "promotion-evidence",
			Observations: []dataset.ObservationInput{{
				SourceKey: "SPY/1Day/2026-08-01", InstrumentID: instrumentID, EffectiveAt: effective,
				PublishedAt: &published, ObservedAt: observed, AvailableAt: observed, Revision: "original",
				ContentSHA256: payload.Digest(), Volume: stringPointer("1000"),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload, manifest
}

func stringPointer(value string) *string { return &value }

var _ repository.DatasetRepository = (*DatasetRepo)(nil)

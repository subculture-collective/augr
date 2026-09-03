package dataset

import (
	"context"
	"errors"
	"testing"
	"time"
)

type marketImportSourceStub struct {
	result MarketImportSourceResult
	err    error
	calls  int
}

func (stub *marketImportSourceStub) FetchMarketPayloads(_ context.Context, _ MarketImportRequest) (MarketImportSourceResult, error) {
	stub.calls++
	return stub.result, stub.err
}

type boundMarketRecorderStub struct {
	value *BoundMarketDataset
	calls int
	err   error
}

func (stub *boundMarketRecorderStub) RecordBoundMarketDataset(_ context.Context, value *BoundMarketDataset, _ time.Time) (*Manifest, error) {
	stub.calls++
	stub.value = value
	if stub.err != nil {
		return nil, stub.err
	}
	return value.Manifest(), nil
}

func TestMarketImporterDryRunAndPersistUseSameDeterministicGraph(t *testing.T) {
	payload := mustBoundTestPayload(t, MarketPayloadStockBar, "AAPL", [16]byte{})
	metadata := payload.Metadata()
	expectedRows, expectedPartitions := 1, 1
	request := validMarketImportRequest(metadata.EffectiveAt, metadata.AvailableAt)
	request.ExpectedPayloadCount = &expectedRows
	request.ExpectedPartitionCount = &expectedPartitions
	source := &marketImportSourceStub{result: MarketImportSourceResult{
		Origin: MarketImportOriginProviderAPI, Entitled: true, PaginationComplete: true, Payloads: []*MarketPayload{payload},
	}}
	recorder := &boundMarketRecorderStub{}
	importer, err := NewMarketImporter(source, recorder)
	if err != nil {
		t.Fatal(err)
	}
	request.DryRun = true
	dry, err := importer.Import(context.Background(), request, metadata.AvailableAt)
	if err != nil {
		t.Fatalf("dry-run Import() error = %v", err)
	}
	if !dry.DryRun || recorder.calls != 0 || dry.PayloadCount != 1 || dry.PartitionCount != 1 {
		t.Fatalf("dry-run summary = %+v, recorder calls = %d", dry, recorder.calls)
	}
	request.DryRun = false
	written, err := importer.Import(context.Background(), request, metadata.AvailableAt)
	if err != nil {
		t.Fatalf("persist Import() error = %v", err)
	}
	if recorder.calls != 1 || written.ManifestID != dry.ManifestID || written.ManifestSHA256 != dry.ManifestSHA256 {
		t.Fatalf("written = %+v, dry = %+v, recorder calls = %d", written, dry, recorder.calls)
	}
}

func TestMarketImporterRejectsUnsafeOrIncompleteSources(t *testing.T) {
	payload := mustBoundTestPayload(t, MarketPayloadStockBar, "AAPL", [16]byte{})
	metadata := payload.Metadata()
	request := validMarketImportRequest(metadata.EffectiveAt, metadata.AvailableAt)
	for name, result := range map[string]MarketImportSourceResult{
		"cache":       {Origin: MarketImportOriginMutableCache, Entitled: true, PaginationComplete: true, Payloads: []*MarketPayload{payload}},
		"entitlement": {Origin: MarketImportOriginProviderAPI, PaginationComplete: true, Payloads: []*MarketPayload{payload}},
		"pagination":  {Origin: MarketImportOriginProviderAPI, Entitled: true, Payloads: []*MarketPayload{payload}},
	} {
		t.Run(name, func(t *testing.T) {
			importer, _ := NewMarketImporter(&marketImportSourceStub{result: result}, &boundMarketRecorderStub{})
			if _, err := importer.Import(context.Background(), request, metadata.AvailableAt); err == nil {
				t.Fatal("Import() accepted unsafe source")
			}
		})
	}
}

func TestMarketImporterRejectsCountAndPersistenceFailures(t *testing.T) {
	payload := mustBoundTestPayload(t, MarketPayloadStockBar, "AAPL", [16]byte{})
	metadata := payload.Metadata()
	request := validMarketImportRequest(metadata.EffectiveAt, metadata.AvailableAt)
	expected := 2
	request.ExpectedPayloadCount = &expected
	source := &marketImportSourceStub{result: MarketImportSourceResult{
		Origin: MarketImportOriginProviderAPI, Entitled: true, PaginationComplete: true, Payloads: []*MarketPayload{payload},
	}}
	importer, _ := NewMarketImporter(source, &boundMarketRecorderStub{})
	if _, err := importer.Import(context.Background(), request, metadata.AvailableAt); err == nil {
		t.Fatal("Import() accepted wrong expected count")
	}
	request.ExpectedPayloadCount = nil
	stop := errors.New("stop")
	importer, _ = NewMarketImporter(source, &boundMarketRecorderStub{err: stop})
	if _, err := importer.Import(context.Background(), request, metadata.AvailableAt); !errors.Is(err, stop) {
		t.Fatalf("Import() error = %v, want %v", err, stop)
	}
}

func validMarketImportRequest(effectiveAt, cutoff time.Time) MarketImportRequest {
	return MarketImportRequest{
		Provider: "alpaca", Feed: "sip", Timeframe: "1d", AdjustmentPolicy: "raw",
		From: effectiveAt, To: effectiveAt, DecisionCutoff: cutoff, Universe: []string{"AAPL"}, MaxPayloads: 10,
		SourceName: "historical_api", Namespace: "promotion/stock", SymbologyVersion: "alpaca-v1",
		Timezone: "UTC", Calendar: "XNYS", License: "licensed", RetentionPolicy: "indefinite",
	}
}

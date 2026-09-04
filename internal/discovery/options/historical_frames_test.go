package options

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type historicalChainReaderStub struct {
	calls         []time.Time
	chains        map[time.Time][]domain.OptionSnapshot
	boundOverride *domain.OHLCV
	err           error
}

func (stub *historicalChainReaderStub) GetOptionsChainAt(_ context.Context, _ string, at time.Time) ([]domain.OptionSnapshot, error) {
	stub.calls = append(stub.calls, at)
	if stub.err != nil {
		return nil, stub.err
	}
	return stub.chains[at], nil
}

func (stub *historicalChainReaderStub) GetOptionsChainAtWithReceipt(ctx context.Context, underlying string, at time.Time) ([]domain.OptionSnapshot, data.ManifestOptionChainReceipt, error) {
	chain, err := stub.GetOptionsChainAt(ctx, underlying, at)
	if err != nil {
		return nil, data.ManifestOptionChainReceipt{}, err
	}
	receipt := data.ManifestOptionChainReceipt{
		ScopeID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("scope")), AccountID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("account")),
		ManifestID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("manifest")), ManifestSHA256: strings.Repeat("d", 64),
		QualityResultID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("quality")), QualitySHA256: strings.Repeat("e", 64),
		DecisionAt: at, DecisionCutoff: at,
	}
	for index, snapshot := range chain {
		for offset, value := range []struct {
			kind   string
			id     uuid.UUID
			digest string
		}{
			{"option_contract", snapshot.ContractPayloadID, snapshot.ContractSHA256},
			{"option_quote", snapshot.QuotePayloadID, snapshot.QuoteSHA256},
			{"option_snapshot", snapshot.SnapshotPayloadID, snapshot.SnapshotSHA256},
		} {
			receipt.Observations = append(receipt.Observations, data.ManifestPayloadReceipt{
				PayloadID: value.id, PayloadKind: value.kind, PartitionSequence: offset,
				PartitionContentSHA256: strings.Repeat(string(rune('f'-offset)), 64), ObservationSequence: index,
				SourceKey: fmt.Sprintf("%s/%d", snapshot.Contract.OCCSymbol, offset), ContentSHA256: value.digest,
				EffectiveAt: at, AvailableAt: at,
			})
		}
	}
	return chain, receipt, nil
}

func (stub *historicalChainReaderStub) GetUnderlyingBarAtWithReceipt(_ context.Context, _ string, _ data.Timeframe, at time.Time) (domain.OHLCV, data.ManifestPayloadReceipt, error) {
	bar := domain.OHLCV{Timestamp: at, Close: map[bool]float64{true: 101, false: 100}[at.After(time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC))]}
	if stub.boundOverride != nil {
		bar = *stub.boundOverride
	}
	return bar, data.ManifestPayloadReceipt{
		PayloadID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("bar/"+at.String())), PayloadKind: "stock_bar", PartitionSequence: 3,
		PartitionContentSHA256: strings.Repeat("9", 64), ObservationSequence: 0, SourceKey: "bar/" + at.Format(time.RFC3339Nano),
		ContentSHA256: strings.Repeat("8", 64), EffectiveAt: at, AvailableAt: at,
	}, nil
}

func TestLoadManifestBoundOptionFramesRejectsSubstitutedUnderlyingBar(t *testing.T) {
	at := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	reader := &historicalChainReaderStub{
		chains:        map[time.Time][]domain.OptionSnapshot{at: {historicalSnapshot(at, "AAPL250221C00100000")}},
		boundOverride: &domain.OHLCV{Timestamp: at, Close: 99},
	}
	_, err := LoadManifestBoundOptionFrames(context.Background(), reader, "AAPL", data.Timeframe1d, []domain.OHLCV{{Timestamp: at, Close: 100}}, at, at)
	if err == nil || !strings.Contains(err.Error(), "underlying observation does not reconstruct") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadManifestBoundOptionFramesUsesEachCanonicalDecisionTime(t *testing.T) {
	start := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	next := start.Add(24 * time.Hour)
	reader := &historicalChainReaderStub{chains: map[time.Time][]domain.OptionSnapshot{
		start: {historicalSnapshot(start, "AAPL250221C00100000")},
		next:  {historicalSnapshot(next, "AAPL250221C00100000")},
	}}
	bars := []domain.OHLCV{{Timestamp: next, Close: 101}, {Timestamp: start, Close: 100}}

	frames, err := LoadManifestBoundOptionFrames(context.Background(), reader, "aapl", data.Timeframe1d, bars, start, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || len(reader.calls) != 2 || !reader.calls[0].Equal(start) || !reader.calls[1].Equal(next) {
		t.Fatalf("frames/calls = %d/%v", len(frames), reader.calls)
	}
	if frames[0].Underlying.Close != 100 || frames[1].Underlying.Close != 101 {
		t.Fatalf("frames are not deterministically ordered: %#v", frames)
	}
}

func TestLoadManifestBoundOptionFramesFailsClosed(t *testing.T) {
	at := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		chain []domain.OptionSnapshot
		want  string
	}{
		{name: "empty", want: "empty manifest-bound chain"},
		{name: "future quote", chain: func() []domain.OptionSnapshot {
			value := historicalSnapshot(at, "AAPL250221C00100000")
			value.QuoteObservedAt = at.Add(time.Microsecond)
			return []domain.OptionSnapshot{value}
		}(), want: "escapes decision cutoff"},
		{name: "duplicate", chain: []domain.OptionSnapshot{historicalSnapshot(at, "AAPL250221C00100000"), historicalSnapshot(at, "AAPL250221C00100000")}, want: "duplicate option contract"},
		{name: "missing provenance", chain: func() []domain.OptionSnapshot {
			value := historicalSnapshot(at, "AAPL250221C00100000")
			value.QuoteSHA256 = ""
			return []domain.OptionSnapshot{value}
		}(), want: "payload provenance is incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &historicalChainReaderStub{chains: map[time.Time][]domain.OptionSnapshot{at: tt.chain}}
			_, err := LoadManifestBoundOptionFrames(context.Background(), reader, "AAPL", data.Timeframe1d, []domain.OHLCV{{Timestamp: at, Close: 100}}, at, at)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadManifestBoundOptionFramesPropagatesReaderFailure(t *testing.T) {
	at := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	want := errors.New("scope does not reconstruct")
	reader := &historicalChainReaderStub{err: want}
	_, err := LoadManifestBoundOptionFrames(context.Background(), reader, "AAPL", data.Timeframe1d, []domain.OHLCV{{Timestamp: at, Close: 100}}, at, at)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}

func historicalSnapshot(at time.Time, symbol string) domain.OptionSnapshot {
	return domain.OptionSnapshot{
		Contract:          domain.OptionContract{InstrumentID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(symbol)), OCCSymbol: symbol, Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: 100, Expiry: time.Date(2025, 2, 21, 0, 0, 0, 0, time.UTC), Multiplier: 100, Style: "american"},
		ContractPayloadID: uuid.New(), ContractSHA256: strings.Repeat("a", 64), QuotePayloadID: uuid.New(), QuoteSHA256: strings.Repeat("b", 64), SnapshotPayloadID: uuid.New(), SnapshotSHA256: strings.Repeat("c", 64),
		Bid: 2, Ask: 2.2, Mid: 2.1, ObservedAt: at, QuoteObservedAt: at,
	}
}

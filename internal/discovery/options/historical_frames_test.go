package options

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

type historicalChainReaderStub struct {
	calls  []time.Time
	chains map[time.Time][]domain.OptionSnapshot
	err    error
}

func (stub *historicalChainReaderStub) GetOptionsChainAt(_ context.Context, _ string, at time.Time) ([]domain.OptionSnapshot, error) {
	stub.calls = append(stub.calls, at)
	if stub.err != nil {
		return nil, stub.err
	}
	return stub.chains[at], nil
}

func TestLoadManifestBoundOptionFramesUsesEachCanonicalDecisionTime(t *testing.T) {
	start := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	next := start.Add(24 * time.Hour)
	reader := &historicalChainReaderStub{chains: map[time.Time][]domain.OptionSnapshot{
		start: {historicalSnapshot(start, "AAPL250221C00100000")},
		next:  {historicalSnapshot(next, "AAPL250221C00100000")},
	}}
	bars := []domain.OHLCV{{Timestamp: next, Close: 101}, {Timestamp: start, Close: 100}}

	frames, err := LoadManifestBoundOptionFrames(context.Background(), reader, "aapl", bars, start, next)
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
			_, err := LoadManifestBoundOptionFrames(context.Background(), reader, "AAPL", []domain.OHLCV{{Timestamp: at, Close: 100}}, at, at)
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
	_, err := LoadManifestBoundOptionFrames(context.Background(), reader, "AAPL", []domain.OHLCV{{Timestamp: at}}, at, at)
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

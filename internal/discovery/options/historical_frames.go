package options

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// HistoricalOptionFrame binds an immutable underlying observation to the
// complete option chain that was knowable at that observation's decision time.
type HistoricalOptionFrame struct {
	DecisionAt time.Time               `json:"decision_at"`
	Underlying domain.OHLCV            `json:"underlying"`
	Chain      []domain.OptionSnapshot `json:"chain"`
}

// LoadManifestBoundOptionFrames constructs a deterministic, no-lookahead
// historical input. The reader is the only permitted source for options data;
// a missing or malformed chain fails the entire requested interval closed.
func LoadManifestBoundOptionFrames(
	ctx context.Context,
	reader data.ManifestBoundOptionChainReader,
	underlying string,
	bars []domain.OHLCV,
	start, end time.Time,
) ([]HistoricalOptionFrame, error) {
	underlying = strings.TrimSpace(strings.ToUpper(underlying))
	if reader == nil || underlying == "" {
		return nil, fmt.Errorf("options/historical: manifest-bound reader and underlying are required")
	}
	if !canonicalDecisionTime(start) || !canonicalDecisionTime(end) || end.Before(start) {
		return nil, fmt.Errorf("options/historical: evaluation interval must be canonical UTC microseconds")
	}

	selected := make([]domain.OHLCV, 0, len(bars))
	for _, bar := range bars {
		if bar.Timestamp.Before(start) || bar.Timestamp.After(end) {
			continue
		}
		if !canonicalDecisionTime(bar.Timestamp) {
			return nil, fmt.Errorf("options/historical: underlying observation time is not canonical UTC microseconds")
		}
		selected = append(selected, bar)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Timestamp.Before(selected[j].Timestamp) })
	if len(selected) == 0 {
		return nil, fmt.Errorf("options/historical: no underlying observations in evaluation interval")
	}
	for index := 1; index < len(selected); index++ {
		if !selected[index].Timestamp.After(selected[index-1].Timestamp) {
			return nil, fmt.Errorf("options/historical: duplicate underlying decision time %s", selected[index].Timestamp.Format(time.RFC3339Nano))
		}
	}

	frames := make([]HistoricalOptionFrame, 0, len(selected))
	for _, bar := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chain, err := reader.GetOptionsChainAt(ctx, underlying, bar.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("options/historical: load chain at %s: %w", bar.Timestamp.Format(time.RFC3339Nano), err)
		}
		active := make([]domain.OptionSnapshot, 0, len(chain))
		for _, snapshot := range chain {
			if !snapshot.Contract.Expiry.Before(bar.Timestamp) {
				active = append(active, snapshot)
			}
		}
		if err := validateHistoricalChain(underlying, bar.Timestamp, active); err != nil {
			return nil, err
		}
		frames = append(frames, HistoricalOptionFrame{DecisionAt: bar.Timestamp, Underlying: bar, Chain: active})
	}
	return frames, nil
}

func validateHistoricalChain(underlying string, decisionAt time.Time, chain []domain.OptionSnapshot) error {
	underlying = strings.ToUpper(strings.TrimSpace(underlying))
	if len(chain) == 0 {
		return fmt.Errorf("options/historical: empty manifest-bound chain at %s", decisionAt.Format(time.RFC3339Nano))
	}
	seen := make(map[string]struct{}, len(chain))
	for _, snapshot := range chain {
		contract := snapshot.Contract
		if strings.ToUpper(contract.Underlying) != underlying || strings.TrimSpace(contract.OCCSymbol) == "" || contract.InstrumentID == uuid.Nil {
			return fmt.Errorf("options/historical: option contract identity does not reconstruct at %s", decisionAt.Format(time.RFC3339Nano))
		}
		if _, exists := seen[contract.OCCSymbol]; exists {
			return fmt.Errorf("options/historical: duplicate option contract %q at %s", contract.OCCSymbol, decisionAt.Format(time.RFC3339Nano))
		}
		seen[contract.OCCSymbol] = struct{}{}
		if snapshot.ContractPayloadID == uuid.Nil || snapshot.QuotePayloadID == uuid.Nil || snapshot.SnapshotPayloadID == uuid.Nil ||
			!validHistoricalSHA(snapshot.ContractSHA256) || !validHistoricalSHA(snapshot.QuoteSHA256) || !validHistoricalSHA(snapshot.SnapshotSHA256) {
			return fmt.Errorf("options/historical: immutable payload provenance is incomplete for %q", contract.OCCSymbol)
		}
		if !canonicalDecisionTime(snapshot.ObservedAt) || !canonicalDecisionTime(snapshot.QuoteObservedAt) ||
			snapshot.ObservedAt.After(decisionAt) || snapshot.QuoteObservedAt.After(decisionAt) {
			return fmt.Errorf("options/historical: option evidence for %q escapes decision cutoff", contract.OCCSymbol)
		}
		if contract.Expiry.Location() != time.UTC || contract.Expiry.Before(decisionAt) || contract.Multiplier <= 0 || snapshot.Bid < 0 || snapshot.Ask <= 0 || snapshot.Ask < snapshot.Bid {
			return fmt.Errorf("options/historical: option market evidence is invalid for %q", contract.OCCSymbol)
		}
	}
	return nil
}

func canonicalDecisionTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Equal(value.Truncate(time.Microsecond))
}

func validHistoricalSHA(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

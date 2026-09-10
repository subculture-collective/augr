package options

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestEvaluateManifestBoundOptionsUsesExecutableObservedQuotes(t *testing.T) {
	start := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	next := start.Add(24 * time.Hour)
	config := historicalVerticalConfig()
	frames := []HistoricalOptionFrame{
		historicalEvaluationFrame(start, 100, 2.8, 3.0, 1.0, 1.2, 1),
		historicalEvaluationFrame(next, 101, 4.0, 4.2, 0.8, 1.0, 2),
	}

	result, err := EvaluateManifestBoundOptions(t.Context(), config, frames, 100_000, 0.65)
	if err != nil {
		t.Fatal(err)
	}
	if result.OpenedPackages != 1 || result.ClosedPackages != 1 {
		t.Fatalf("packages = opened %d closed %d", result.OpenedPackages, result.ClosedPackages)
	}
	if result.Metrics.ClosedTrades != 1 {
		t.Fatalf("closed-trade evidence = %d, want one package, not four leg fills", result.Metrics.ClosedTrades)
	}
	// Entry pays 3.0 and receives 1.0; close receives 4.0 and pays 1.0.
	// Gross profit is $100, with four $0.65 contract fees.
	if got, want := result.Metrics.EndEquity, 100097.4; got != want {
		t.Fatalf("end equity = %.2f, want %.2f", got, want)
	}
	if result.Metrics.OrderAttempts != 4 || result.Metrics.OrderFills != 4 || result.Metrics.FillRate != 1 {
		t.Fatalf("execution metrics = %+v", result.Metrics)
	}
	if len(result.PayloadSHA256) != 14 || len(result.Evidence) != 14 {
		t.Fatalf("payload evidence = %d/%d, want 14/14", len(result.PayloadSHA256), len(result.Evidence))
	}
	if result.ScopeID != frames[0].Receipt.ScopeID || result.AccountID != frames[0].Receipt.AccountID ||
		result.ManifestID != frames[0].Receipt.ManifestID || result.QualityResultID != frames[0].Receipt.QualityResultID {
		t.Fatalf("evaluation parent identity does not reconstruct: %+v", result)
	}
}

func TestEvaluateManifestBoundOptionsRejectsMissingOpenContract(t *testing.T) {
	start := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	frames := []HistoricalOptionFrame{
		historicalEvaluationFrame(start, 100, 2.8, 3.0, 1.0, 1.2, 1),
		historicalEvaluationFrame(start.Add(24*time.Hour), 101, 4.0, 4.2, 0.8, 1.0, 2),
	}
	frames[1].Chain = []domain.OptionSnapshot{historicalEvaluationSnapshot(frames[1].DecisionAt, "AAPL250221C00110000", 110, 0.2, 0.5, 0.7, 9)}
	frames[1].Receipt.Observations = nil
	snapshot := frames[1].Chain[0]
	for offset, evidence := range []struct {
		kind   string
		id     uuid.UUID
		digest string
	}{
		{"option_contract", snapshot.ContractPayloadID, snapshot.ContractSHA256},
		{"option_quote", snapshot.QuotePayloadID, snapshot.QuoteSHA256},
		{"option_snapshot", snapshot.SnapshotPayloadID, snapshot.SnapshotSHA256},
	} {
		frames[1].Receipt.Observations = append(frames[1].Receipt.Observations, historicalPayloadReceipt(frames[1].DecisionAt, evidence.kind, evidence.id, evidence.digest, 9+offset, offset))
	}
	config := historicalVerticalConfig()
	never := 200.0
	config.Exit = rules.ConditionGroup{Operator: "AND", Conditions: []rules.Condition{{Field: "close", Op: "gt", Value: &never}}}

	_, err := EvaluateManifestBoundOptions(t.Context(), config, frames, 100_000, 0)
	if err == nil || !strings.Contains(err.Error(), "open contract") {
		t.Fatalf("error = %v, want missing open contract", err)
	}
}

func TestEvaluateManifestBoundOptionsRejectsUnboundFrameEvidence(t *testing.T) {
	start := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	frames := []HistoricalOptionFrame{
		historicalEvaluationFrame(start, 100, 2.8, 3.0, 1.0, 1.2, 1),
		historicalEvaluationFrame(start.Add(24*time.Hour), 101, 4.0, 4.2, 0.8, 1.0, 2),
	}
	frames[0].Receipt.Observations[1].ContentSHA256 = strings.Repeat("f", 64)
	_, err := EvaluateManifestBoundOptions(t.Context(), historicalVerticalConfig(), frames, 100_000, 0.65)
	if err == nil || !strings.Contains(err.Error(), "receipt payload does not reconstruct") {
		t.Fatalf("error = %v", err)
	}
}

func TestEvaluateManifestBoundOptionsRejectsCrossScopeFrames(t *testing.T) {
	start := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	frames := []HistoricalOptionFrame{
		historicalEvaluationFrame(start, 100, 2.8, 3.0, 1.0, 1.2, 1),
		historicalEvaluationFrame(start.Add(24*time.Hour), 101, 4.0, 4.2, 0.8, 1.0, 2),
	}
	otherScope := uuid.NewSHA1(uuid.NameSpaceOID, []byte("other-scope"))
	frames[1].Receipt.ScopeID = otherScope
	frames[1].UnderlyingReceipt.ScopeID = otherScope
	for index := range frames[1].Receipt.Observations {
		frames[1].Receipt.Observations[index].ScopeID = otherScope
	}
	_, err := EvaluateManifestBoundOptions(t.Context(), historicalVerticalConfig(), frames, 100_000, 0.65)
	if err == nil || !strings.Contains(err.Error(), "changes evidence scope") {
		t.Fatalf("error = %v", err)
	}
}

func historicalVerticalConfig() rules.OptionsRulesConfig {
	zero, hundred := 0.0, 100.5
	return rules.OptionsRulesConfig{
		Version: 1, StrategyType: domain.StrategyBullCallSpread, Underlying: "AAPL",
		Entry: rules.ConditionGroup{Operator: "AND", Conditions: []rules.Condition{{Field: "close", Op: "gt", Value: &zero}, {Field: "close", Op: "lt", Value: &hundred}}},
		Exit:  rules.ConditionGroup{Operator: "AND", Conditions: []rules.Condition{{Field: "close", Op: "gt", Value: &hundred}}},
		LegSelection: map[string]rules.LegSelector{
			"long":  {OptionType: domain.OptionTypeCall, DeltaTarget: 0.6, DTEMin: 20, DTEMax: 60, Side: domain.OrderSideBuy, Intent: domain.PositionIntentBuyToOpen, Ratio: 1},
			"short": {OptionType: domain.OptionTypeCall, DeltaTarget: 0.3, DTEMin: 20, DTEMax: 60, Side: domain.OrderSideSell, Intent: domain.PositionIntentSellToOpen, Ratio: 1},
		},
		PositionSizing: rules.OptionsSizingConfig{Method: "max_risk", MaxRiskUSD: 500},
	}
}

func historicalEvaluationFrame(at time.Time, closePrice, longBid, longAsk, shortBid, shortAsk float64, salt int) HistoricalOptionFrame {
	frame := HistoricalOptionFrame{
		DecisionAt: at,
		Underlying: domain.OHLCV{Timestamp: at, Open: closePrice, High: closePrice, Low: closePrice, Close: closePrice, Volume: 1_000_000},
		Chain: []domain.OptionSnapshot{
			historicalEvaluationSnapshot(at, "AAPL250221C00100000", 100, 0.6, longBid, longAsk, salt*2),
			historicalEvaluationSnapshot(at, "AAPL250221C00105000", 105, 0.3, shortBid, shortAsk, salt*2+1),
		},
	}
	frame.UnderlyingReceipt = historicalPayloadReceipt(at, "stock_bar", uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("bar/%d", salt))), fmt.Sprintf("%064x", salt*100), salt, 0)
	frame.Receipt = data.ManifestOptionChainReceipt{
		ScopeID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("scope")), AccountID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("account")),
		ManifestID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("manifest")), ManifestSHA256: strings.Repeat("d", 64),
		QualityResultID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("quality")), QualitySHA256: strings.Repeat("e", 64),
		DecisionAt: at, DecisionCutoff: at,
	}
	for chainIndex, snapshot := range frame.Chain {
		for offset, evidence := range []struct {
			kind   string
			id     uuid.UUID
			digest string
		}{
			{"option_contract", snapshot.ContractPayloadID, snapshot.ContractSHA256},
			{"option_quote", snapshot.QuotePayloadID, snapshot.QuoteSHA256},
			{"option_snapshot", snapshot.SnapshotPayloadID, snapshot.SnapshotSHA256},
		} {
			frame.Receipt.Observations = append(frame.Receipt.Observations, historicalPayloadReceipt(at, evidence.kind, evidence.id, evidence.digest, salt+offset, chainIndex*3+offset))
		}
	}
	return frame
}

func historicalPayloadReceipt(at time.Time, kind string, id uuid.UUID, digest string, partition, sequence int) data.ManifestPayloadReceipt {
	return data.ManifestPayloadReceipt{
		ScopeID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("scope")), AccountID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("account")),
		ManifestID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("manifest")), ManifestSHA256: strings.Repeat("d", 64),
		QualityResultID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("quality")), QualitySHA256: strings.Repeat("e", 64),
		PayloadID: id, PayloadKind: kind, PartitionSequence: partition, PartitionContentSHA256: fmt.Sprintf("%064x", partition+1000),
		ObservationSequence: sequence, SourceKey: fmt.Sprintf("%s/%d/%d", kind, partition, sequence), ContentSHA256: digest,
		EffectiveAt: at, AvailableAt: at,
	}
}

func historicalEvaluationSnapshot(at time.Time, symbol string, strike, delta, bid, ask float64, salt int) domain.OptionSnapshot {
	digest := func(offset int) string { return fmt.Sprintf("%064x", salt*10+offset) }
	return domain.OptionSnapshot{
		Contract:          domain.OptionContract{InstrumentID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(symbol)), OCCSymbol: symbol, Underlying: "AAPL", OptionType: domain.OptionTypeCall, Strike: strike, Expiry: time.Date(2025, 2, 21, 0, 0, 0, 0, time.UTC), Multiplier: 100, Style: "american"},
		ContractPayloadID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(symbol+digest(1))), ContractSHA256: digest(1),
		QuotePayloadID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(symbol+digest(2))), QuoteSHA256: digest(2),
		SnapshotPayloadID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(symbol+digest(3))), SnapshotSHA256: digest(3),
		Greeks: domain.OptionGreeks{Delta: delta, Gamma: 0.02, Theta: -0.03, Vega: 0.1, IV: 0.25},
		Bid:    bid, BidSize: 10, Ask: ask, AskSize: 10, Mid: (bid + ask) / 2, ObservedAt: at, QuoteObservedAt: at,
	}
}

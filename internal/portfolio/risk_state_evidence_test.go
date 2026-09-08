package portfolio

import (
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

func TestBindRiskStateEvidenceIsDeterministicAndComplete(t *testing.T) {
	observedAt := time.Date(2026, 9, 3, 15, 4, 5, 123456000, time.UTC)
	state := PortfolioState{
		AccountSnapshotID: uuid.New(), Equity: 100000, BuyingPower: 50000, OptionsBuyingPower: 25000,
		GrossExposure: 12000, MarketExposure: map[domain.MarketType]float64{domain.MarketTypeOptions: 2000, domain.MarketTypeStock: 10000},
		UnderlyingRisk: map[string]float64{"MSFT": 800, "AAPL": 1200}, NewOrdersToday: 2,
		DailyLossPct: .01, DrawdownPct: .03, OpenPositionCount: 4, ReconciliationID: uuid.NewString(), Delta: 10, Gamma: 2, Theta: -3, Vega: 8,
	}
	copyState := state
	copyState.MarketExposure = map[domain.MarketType]float64{domain.MarketTypeStock: 10000, domain.MarketTypeOptions: 2000}
	copyState.UnderlyingRisk = map[string]float64{"AAPL": 1200, "MSFT": 800}
	if err := BindRiskStateEvidence(&state, observedAt); err != nil {
		t.Fatal(err)
	}
	if err := BindRiskStateEvidence(&copyState, observedAt); err != nil {
		t.Fatal(err)
	}
	if state.RiskStateSHA256 == "" || state.RiskStateSHA256 != copyState.RiskStateSHA256 || string(state.RiskStateBytes) != string(copyState.RiskStateBytes) {
		t.Fatalf("risk evidence diverged: %s != %s", state.RiskStateSHA256, copyState.RiskStateSHA256)
	}
}

func TestBindRiskStateEvidenceRejectsMissingReconciliation(t *testing.T) {
	state := PortfolioState{AccountSnapshotID: uuid.New(), Equity: 100}
	if err := BindRiskStateEvidence(&state, time.Now().UTC()); err == nil {
		t.Fatal("missing reconciliation was accepted")
	}
}

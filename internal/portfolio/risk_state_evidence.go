package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

const RiskStateEvidenceSchemaV1 = "portfolio-risk-state-v1"

type riskAmount struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

type riskStateCanonical struct {
	Schema             string       `json:"schema"`
	AccountSnapshotID  string       `json:"account_snapshot_id"`
	ObservedAt         string       `json:"observed_at"`
	Equity             float64      `json:"equity"`
	BuyingPower        float64      `json:"buying_power"`
	OptionsBuyingPower float64      `json:"options_buying_power"`
	GrossExposure      float64      `json:"gross_exposure"`
	MarketExposure     []riskAmount `json:"market_exposure"`
	UnderlyingRisk     []riskAmount `json:"underlying_risk"`
	NewOrdersToday     int          `json:"new_orders_today"`
	DailyLossPct       float64      `json:"daily_loss_pct"`
	DrawdownPct        float64      `json:"drawdown_pct"`
	OpenPositionCount  int          `json:"open_position_count"`
	CircuitBreakerOpen bool         `json:"circuit_breaker_open"`
	ReconciliationID   string       `json:"reconciliation_id"`
	Delta              float64      `json:"delta"`
	Gamma              float64      `json:"gamma"`
	Theta              float64      `json:"theta"`
	Vega               float64      `json:"vega"`
}

func BindRiskStateEvidence(state *PortfolioState, observedAt time.Time) error {
	if state == nil || state.AccountSnapshotID == uuid.Nil || observedAt.IsZero() || state.Equity <= 0 || state.ReconciliationID == "" {
		return fmt.Errorf("portfolio risk state evidence is incomplete")
	}
	canonical := riskStateCanonical{
		Schema: RiskStateEvidenceSchemaV1, AccountSnapshotID: state.AccountSnapshotID.String(), ObservedAt: observedAt.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z"),
		Equity: state.Equity, BuyingPower: state.BuyingPower, OptionsBuyingPower: state.OptionsBuyingPower, GrossExposure: state.GrossExposure,
		MarketExposure: sortedMarketRisk(state.MarketExposure), UnderlyingRisk: sortedStringRisk(state.UnderlyingRisk), NewOrdersToday: state.NewOrdersToday,
		DailyLossPct: state.DailyLossPct, DrawdownPct: state.DrawdownPct, OpenPositionCount: state.OpenPositionCount,
		CircuitBreakerOpen: state.CircuitBreakerOpen, ReconciliationID: state.ReconciliationID,
		Delta: state.Delta, Gamma: state.Gamma, Theta: state.Theta, Vega: state.Vega,
	}
	for _, value := range []float64{canonical.Equity, canonical.BuyingPower, canonical.OptionsBuyingPower, canonical.GrossExposure, canonical.DailyLossPct, canonical.DrawdownPct, canonical.Delta, canonical.Gamma, canonical.Theta, canonical.Vega} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("portfolio risk state evidence contains a non-finite value")
		}
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	state.RiskStateBytes = raw
	state.RiskStateSHA256 = hex.EncodeToString(hash[:])
	return nil
}

func sortedMarketRisk(values map[domain.MarketType]float64) []riskAmount {
	converted := make(map[string]float64, len(values))
	for key, value := range values {
		converted[string(key)] = value
	}
	return sortedStringRisk(converted)
}

func sortedStringRisk(values map[string]float64) []riskAmount {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]riskAmount, 0, len(keys))
	for _, key := range keys {
		out = append(out, riskAmount{Key: key, Value: values[key]})
	}
	return out
}

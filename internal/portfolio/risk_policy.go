package portfolio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
)

const PortfolioRiskPolicySchemaV1 = "portfolio-risk-policy-v1"

type PortfolioRiskPolicy struct {
	Schema                      string  `json:"schema"`
	Version                     string  `json:"version"`
	TargetGrossExposurePct      float64 `json:"target_gross_exposure_pct"`
	HardGrossExposurePct        float64 `json:"hard_gross_exposure_pct"`
	CashReservePct              float64 `json:"cash_reserve_pct"`
	MaxNewSelectionsPerRun      int     `json:"max_new_selections_per_run"`
	MaxNewSelectionsPerDay      int     `json:"max_new_selections_per_day"`
	MaxPositionRiskPct          float64 `json:"max_position_risk_pct"`
	MaxOptionsMarketRiskPct     float64 `json:"max_options_market_risk_pct"`
	MaxDailyLossPct             float64 `json:"max_daily_loss_pct"`
	MaxDrawdownPct              float64 `json:"max_drawdown_pct"`
	MaxOpenPositions            int     `json:"max_open_positions"`
	MaxReconciliationAgeSeconds int     `json:"max_reconciliation_age_seconds"`
	MaxQuoteAgeSeconds          int     `json:"max_quote_age_seconds"`
	MaxOptionSpreadPct          float64 `json:"max_option_spread_pct"`
	MinOptionLiquidityUSD       float64 `json:"min_option_liquidity_usd"`
	MaxAbsoluteDelta            float64 `json:"max_absolute_delta"`
	MaxAbsoluteGamma            float64 `json:"max_absolute_gamma"`
	MaxAbsoluteTheta            float64 `json:"max_absolute_theta"`
	MaxAbsoluteVega             float64 `json:"max_absolute_vega"`
	canonical                   []byte
	digest                      string
	id                          uuid.UUID
}

func ReviewedPortfolioRiskPolicyV1() (*PortfolioRiskPolicy, error) {
	value := &PortfolioRiskPolicy{
		Schema: PortfolioRiskPolicySchemaV1, Version: "reviewed-v1", TargetGrossExposurePct: .35,
		HardGrossExposurePct: .50, CashReservePct: .20, MaxNewSelectionsPerRun: 2, MaxNewSelectionsPerDay: 5,
		MaxPositionRiskPct: .02, MaxOptionsMarketRiskPct: .10, MaxDailyLossPct: .03, MaxDrawdownPct: .15,
		MaxOpenPositions: 20, MaxReconciliationAgeSeconds: 900, MaxQuoteAgeSeconds: 300, MaxOptionSpreadPct: .15, MinOptionLiquidityUSD: 1000,
		MaxAbsoluteDelta: 500, MaxAbsoluteGamma: 100, MaxAbsoluteTheta: 500, MaxAbsoluteVega: 1000,
	}
	return finalizePortfolioRiskPolicy(value)
}

func finalizePortfolioRiskPolicy(value *PortfolioRiskPolicy) (*PortfolioRiskPolicy, error) {
	if value == nil || value.Schema != PortfolioRiskPolicySchemaV1 || value.Version == "" ||
		value.TargetGrossExposurePct <= 0 || value.HardGrossExposurePct < value.TargetGrossExposurePct || value.HardGrossExposurePct > 1 ||
		value.CashReservePct < 0 || value.CashReservePct >= 1 || value.MaxNewSelectionsPerRun <= 0 || value.MaxNewSelectionsPerDay < value.MaxNewSelectionsPerRun ||
		value.MaxPositionRiskPct <= 0 || value.MaxOptionsMarketRiskPct <= 0 || value.MaxOpenPositions <= 0 || value.MaxReconciliationAgeSeconds <= 0 || value.MaxQuoteAgeSeconds <= 0 ||
		value.MaxOptionSpreadPct <= 0 || value.MinOptionLiquidityUSD <= 0 || nonFinitePolicy(value) {
		return nil, fmt.Errorf("portfolio risk policy is invalid")
	}
	type canonicalPolicy PortfolioRiskPolicy
	copyValue := canonicalPolicy(*value)
	copyValue.canonical, copyValue.digest, copyValue.id = nil, "", uuid.Nil
	raw, err := json.Marshal(copyValue)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	value.canonical = raw
	value.digest = hex.EncodeToString(hash[:])
	value.id = economicid.DeterministicUUID("portfolio-risk-policy", PortfolioRiskPolicySchemaV1+"@sha256:"+value.digest)
	return value, nil
}

func nonFinitePolicy(value *PortfolioRiskPolicy) bool {
	for _, number := range []float64{value.TargetGrossExposurePct, value.HardGrossExposurePct, value.CashReservePct,
		value.MaxPositionRiskPct, value.MaxOptionsMarketRiskPct, value.MaxDailyLossPct, value.MaxDrawdownPct,
		value.MaxOptionSpreadPct, value.MinOptionLiquidityUSD, value.MaxAbsoluteDelta, value.MaxAbsoluteGamma,
		value.MaxAbsoluteTheta, value.MaxAbsoluteVega} {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return true
		}
	}
	return false
}

func (value *PortfolioRiskPolicy) ID() uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return value.id
}
func (value *PortfolioRiskPolicy) Digest() string {
	if value == nil {
		return ""
	}
	return value.digest
}
func (value *PortfolioRiskPolicy) CanonicalBytes() []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value.canonical...)
}
func (value *PortfolioRiskPolicy) Reference() string {
	if value == nil {
		return ""
	}
	return PortfolioRiskPolicySchemaV1 + "@sha256:" + value.digest
}
func (value *PortfolioRiskPolicy) MaxQuoteAge() time.Duration {
	if value == nil {
		return 0
	}
	return time.Duration(value.MaxQuoteAgeSeconds) * time.Second
}

func (value *PortfolioRiskPolicy) MaxReconciliationAge() time.Duration {
	if value == nil {
		return 0
	}
	return time.Duration(value.MaxReconciliationAgeSeconds) * time.Second
}

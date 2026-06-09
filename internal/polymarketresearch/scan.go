package polymarketresearch

import (
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/edge"
)

type OpportunityState string

const (
	OpportunityStateAccepted OpportunityState = "accepted"
	OpportunityStateRejected OpportunityState = "rejected"
)

// BinaryProbabilityEstimate describes a calibrated probability used for scanning.
type BinaryProbabilityEstimate struct {
	Probability  float64   `json:"probability"`
	Source       string    `json:"source,omitempty"`
	Calibration  string    `json:"calibration,omitempty"`
	Confidence   float64   `json:"confidence,omitempty"`
	CalibratedAt time.Time `json:"calibrated_at,omitempty"`
	Notes        []string  `json:"notes,omitempty"`
}

// ScanConfig configures paper-first opportunity scanning.
type ScanConfig struct {
	MinNetEdge             float64       `json:"min_net_edge"`
	MaxSpread              float64       `json:"max_spread"`
	MinDepthUSD            float64       `json:"min_depth_usd"`
	KellyFraction          float64       `json:"kelly_fraction"`
	MaxPositionPctBankroll float64       `json:"max_position_pct_bankroll"`
	Bankroll               float64       `json:"bankroll"`
	Fee                    float64       `json:"fee,omitempty"`
	Slippage               float64       `json:"slippage,omitempty"`
	ExitHaircut            float64       `json:"exit_haircut,omitempty"`
	MaxSnapshotAge         time.Duration `json:"max_snapshot_age,omitempty"`
}

// DefaultScanConfig returns conservative paper-first defaults.
func DefaultScanConfig() ScanConfig {
	return ScanConfig{
		MinNetEdge:             0.01,
		MaxSpread:              0.08,
		MinDepthUSD:            250,
		KellyFraction:          0.25,
		MaxPositionPctBankroll: 0.05,
		Bankroll:               1000,
		MaxSnapshotAge:         5 * time.Minute,
	}
}

// Validate checks whether the scan config is usable.
func (c ScanConfig) Validate() []string {
	var reasons []string
	if !isFiniteNonNegative(c.MinNetEdge) {
		reasons = append(reasons, "invalid_min_net_edge")
	}
	if !isFinitePositive(c.MaxSpread) {
		reasons = append(reasons, "invalid_max_spread")
	}
	if !isFiniteNonNegative(c.MinDepthUSD) {
		reasons = append(reasons, "invalid_min_depth_usd")
	}
	if !isFinitePositive(c.KellyFraction) {
		reasons = append(reasons, "invalid_kelly_fraction")
	}
	if !isFinitePositive(c.MaxPositionPctBankroll) || c.MaxPositionPctBankroll > 1 {
		reasons = append(reasons, "invalid_max_position_pct_bankroll")
	}
	if !isFinitePositive(c.Bankroll) {
		reasons = append(reasons, "invalid_bankroll")
	}
	if !isFiniteNonNegative(c.Fee) {
		reasons = append(reasons, "invalid_fee")
	}
	if !isFiniteNonNegative(c.Slippage) {
		reasons = append(reasons, "invalid_slippage")
	}
	if !isFiniteNonNegative(c.ExitHaircut) {
		reasons = append(reasons, "invalid_exit_haircut")
	}
	if c.MaxSnapshotAge < 0 {
		reasons = append(reasons, "invalid_max_snapshot_age")
	}
	return reasons
}

// Opportunity captures the scan outcome and a journal-ready trade decision.
type Opportunity struct {
	State         OpportunityState          `json:"state"`
	Reasons       []string                  `json:"reasons,omitempty"`
	Estimate      BinaryProbabilityEstimate `json:"estimate"`
	GrossEV       float64                   `json:"gross_ev"`
	NetEV         float64                   `json:"net_ev"`
	KellyFraction float64                   `json:"kelly_fraction"`
	ProposedSize  float64                   `json:"proposed_size"`
	ApprovedSize  float64                   `json:"approved_size"`
	Decision      domain.TradeDecision      `json:"decision"`
}

// ScanBinaryOpportunity evaluates a yes/buy candidate against the ask.
func ScanBinaryOpportunity(now time.Time, snapshot domain.PolymarketBookSnapshot, est BinaryProbabilityEstimate, cfg ScanConfig) Opportunity {
	reasons := cfg.Validate()
	probValid := isFiniteProbability(est.Probability)
	bidValid := isFinitePrice(snapshot.BestBid)
	askValid := isFinitePrice(snapshot.BestAsk)
	instrumentKey := firstNonEmpty(snapshot.TokenID, snapshot.Slug)
	externalMarketID := firstNonEmpty(snapshot.Slug, snapshot.TokenID)
	identifierValid := instrumentKey != "" && externalMarketID != ""
	if !probValid {
		reasons = append(reasons, "invalid_probability")
	}
	if !bidValid || !askValid {
		reasons = append(reasons, "invalid_executable_price")
	}
	if !identifierValid {
		reasons = append(reasons, "missing_market_identifier")
	}
	if bidValid && askValid && snapshot.BestAsk <= snapshot.BestBid {
		reasons = append(reasons, "invalid_spread")
	}

	spread := 0.0
	if bidValid && askValid {
		spread = snapshot.BestAsk - snapshot.BestBid
	}
	if cfg.MaxSnapshotAge > 0 && snapshot.ReceivedAt.IsZero() {
		reasons = append(reasons, "stale_snapshot")
	} else if cfg.MaxSnapshotAge > 0 && !snapshot.ReceivedAt.IsZero() && now.Sub(snapshot.ReceivedAt) > cfg.MaxSnapshotAge {
		reasons = append(reasons, "stale_snapshot")
	}
	if isFinitePositive(cfg.MaxSpread) && spread > cfg.MaxSpread {
		reasons = append(reasons, "spread_too_wide")
	}

	depthUSD := buyAskDepthUSD(snapshot)
	if depthUSD < cfg.MinDepthUSD {
		reasons = append(reasons, "insufficient_depth")
	}

	metricsValid := len(cfg.Validate()) == 0 && probValid && bidValid && askValid
	grossNet := edge.BinaryEVResult{}
	kellyFraction := 0.0
	proposedSize := 0.0
	approvedSize := 0.0
	if metricsValid {
		grossNet = edge.BinaryNetEV(edge.BinaryEVInput{
			Probability: est.Probability,
			Price:       snapshot.BestAsk,
			Fee:         cfg.Fee,
			Slippage:    cfg.Slippage,
			ExitHaircut: cfg.ExitHaircut,
		})
		kellyFraction = edge.FractionalKellyCap(edge.BinaryKellyInput{
			Probability: est.Probability,
			Price:       snapshot.BestAsk,
			Fraction:    cfg.KellyFraction,
			Cap:         cfg.MaxPositionPctBankroll,
		})
		proposedSize = cfg.Bankroll * kellyFraction
		approvedSize = proposedSize
		if grossNet.NetEV < cfg.MinNetEdge {
			reasons = append(reasons, "insufficient_edge")
			approvedSize = 0
		}
	}

	sanitizedEstimate := sanitizeProbabilityEstimate(est)
	decision := domain.TradeDecision{
		ID:               uuid.New(),
		MarketType:       domain.MarketTypePolymarket,
		InstrumentKey:    instrumentKey,
		ExternalMarketID: externalMarketID,
		Side:             domain.OrderSideBuy,
		Outcome:          snapshot.Outcome,
		FairValue:        safeProbability(est.Probability),
		ExecutablePrice:  safeExecutablePrice(snapshot.BestAsk),
		Spread:           spread,
		Depth:            depthUSD,
		GrossEV:          finiteOrZero(grossNet.GrossEV),
		NetEV:            finiteOrZero(grossNet.NetEV),
		KellyFraction:    kellyFraction,
		ProposedSize:     proposedSize,
		ApprovedSize:     approvedSize,
		RiskReasons:      append([]string(nil), reasons...),
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if len(reasons) > 0 {
		decision.RiskStatus = domain.RiskDecisionRejected
		decision.Status = domain.TradeDecisionStatusRejected
		decision.ApprovedSize = 0
		return Opportunity{
			State:         OpportunityStateRejected,
			Reasons:       append([]string(nil), reasons...),
			Estimate:      sanitizedEstimate,
			GrossEV:       finiteOrZero(grossNet.GrossEV),
			NetEV:         finiteOrZero(grossNet.NetEV),
			KellyFraction: kellyFraction,
			ProposedSize:  proposedSize,
			ApprovedSize:  0,
			Decision:      decision,
		}
	}

	decision.RiskStatus = domain.RiskDecisionApproved
	decision.Status = domain.TradeDecisionStatusCandidate
	return Opportunity{
		State:         OpportunityStateAccepted,
		Estimate:      sanitizedEstimate,
		GrossEV:       finiteOrZero(grossNet.GrossEV),
		NetEV:         finiteOrZero(grossNet.NetEV),
		KellyFraction: kellyFraction,
		ProposedSize:  proposedSize,
		ApprovedSize:  approvedSize,
		Decision:      decision,
	}
}

func buyAskDepthUSD(snapshot domain.PolymarketBookSnapshot) float64 {
	if isFinitePositive(snapshot.AskDepthUSD) {
		return snapshot.AskDepthUSD
	}
	if len(snapshot.Asks) > 0 && isFinitePrice(snapshot.Asks[0].Price) && isFinitePositive(snapshot.Asks[0].Size) {
		return snapshot.Asks[0].Price * snapshot.Asks[0].Size
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func isFiniteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func isFiniteProbability(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v < 1
}

func isFinitePrice(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v < 1
}

func finiteOrZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func safeProbability(v float64) float64 {
	if !isFiniteProbability(v) {
		return 0
	}
	return v
}

func safeExecutablePrice(v float64) float64 {
	if !isFinitePrice(v) {
		return 0
	}
	return v
}

func sanitizeProbabilityEstimate(est BinaryProbabilityEstimate) BinaryProbabilityEstimate {
	est.Probability = safeProbability(est.Probability)
	est.Confidence = finiteOrZero(est.Confidence)
	return est
}

package options

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/agent/rules"
	"github.com/PatrickFanella/get-rich-quick/internal/backtest"
	"github.com/PatrickFanella/get-rich-quick/internal/data"
	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// HistoricalOptionsEvaluation is promotion-capable options evidence produced
// exclusively from manifest-bound market observations. Synthetic chains are
// deliberately not accepted by this API.
type HistoricalOptionsEvaluation struct {
	Metrics         backtest.Metrics              `json:"metrics"`
	EquityCurve     []backtest.EquityPoint        `json:"equity_curve"`
	OpenedPackages  int                           `json:"opened_packages"`
	ClosedPackages  int                           `json:"closed_packages"`
	ScopeID         uuid.UUID                     `json:"scope_id"`
	AccountID       uuid.UUID                     `json:"account_id"`
	ManifestID      uuid.UUID                     `json:"manifest_id"`
	ManifestSHA256  string                        `json:"manifest_sha256"`
	QualityResultID uuid.UUID                     `json:"quality_result_id"`
	QualitySHA256   string                        `json:"quality_sha256"`
	Evidence        []data.ManifestPayloadReceipt `json:"evidence"`
	PayloadSHA256   []string                      `json:"payload_sha256"`
}

type historicalObservedPosition struct {
	spread         *domain.OptionSpread
	entryCash      float64
	entryFees      float64
	feePerContract float64
	maximumRisk    float64
	maximumReward  float64
}

// EvaluateManifestBoundOptions executes one deterministic vertical strategy
// over pre-bound historical frames using executable bid/ask observations.
func EvaluateManifestBoundOptions(ctx context.Context, config rules.OptionsRulesConfig, frames []HistoricalOptionFrame, initialCash, feePerContract float64) (*HistoricalOptionsEvaluation, error) {
	if err := rules.ValidateDefinedRiskVertical(&config); err != nil {
		return nil, fmt.Errorf("options/historical: ineligible strategy: %w", err)
	}
	if len(frames) < 2 || initialCash <= 0 || feePerContract < 0 {
		return nil, fmt.Errorf("options/historical: at least two frames, positive capital, and non-negative fees are required")
	}
	firstReceipt := frames[0].Receipt
	for index := range frames {
		if index > 0 && !frames[index].DecisionAt.After(frames[index-1].DecisionAt) {
			return nil, fmt.Errorf("options/historical: frames must be strictly chronological")
		}
		if err := validateHistoricalChain(config.Underlying, frames[index].DecisionAt, frames[index].Chain); err != nil {
			return nil, err
		}
		if err := validateUnderlyingReceipt(frames[index].DecisionAt, frames[index].UnderlyingReceipt); err != nil {
			return nil, fmt.Errorf("options/historical: frame %d underlying evidence: %w", index, err)
		}
		if err := validateHistoricalReceipt(frames[index].DecisionAt, frames[index].Chain, frames[index].Receipt); err != nil {
			return nil, fmt.Errorf("options/historical: frame %d chain evidence: %w", index, err)
		}
		if !sameHistoricalReceiptParent(firstReceipt, frames[index].Receipt) {
			return nil, fmt.Errorf("options/historical: frame %d changes evidence scope", index)
		}
		underlying := frames[index].UnderlyingReceipt
		if underlying.ScopeID != frames[index].Receipt.ScopeID || underlying.AccountID != frames[index].Receipt.AccountID ||
			underlying.ManifestID != frames[index].Receipt.ManifestID || underlying.ManifestSHA256 != frames[index].Receipt.ManifestSHA256 ||
			underlying.QualityResultID != frames[index].Receipt.QualityResultID || underlying.QualitySHA256 != frames[index].Receipt.QualitySHA256 {
			return nil, fmt.Errorf("options/historical: frame %d underlying and chain evidence parents differ", index)
		}
	}

	bars := make([]domain.OHLCV, len(frames))
	for index := range frames {
		bars[index] = frames[index].Underlying
	}
	indicators := precomputeIndicatorSnapshots(bars)
	cash, realized := initialCash, 0.0
	var position *historicalObservedPosition
	var previous *rules.Snapshot
	result := &HistoricalOptionsEvaluation{
		EquityCurve: make([]backtest.EquityPoint, 0, len(frames)), ScopeID: firstReceipt.ScopeID, AccountID: firstReceipt.AccountID,
		ManifestID: firstReceipt.ManifestID, ManifestSHA256: firstReceipt.ManifestSHA256,
		QualityResultID: firstReceipt.QualityResultID, QualitySHA256: firstReceipt.QualitySHA256,
	}
	content := make(map[string]struct{})
	evidenceByID := make(map[uuid.UUID]data.ManifestPayloadReceipt)
	orderAttempts, orderFills := 0, 0

	for index, frame := range frames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frameEvidence := append([]data.ManifestPayloadReceipt{frame.UnderlyingReceipt}, frame.Receipt.Observations...)
		for _, observation := range frameEvidence {
			if prior, exists := evidenceByID[observation.PayloadID]; exists && prior != observation {
				return nil, fmt.Errorf("options/historical: payload receipt changes within evaluation interval")
			}
			evidenceByID[observation.PayloadID] = observation
			content[observation.ContentSHA256] = struct{}{}
		}

		closeValue, closeFees, pnl := 0.0, 0.0, 0.0
		if position != nil {
			var err error
			closeValue, closeFees, err = observedLiquidationValue(position.spread, frame, position.feePerContract)
			if err != nil {
				return nil, err
			}
			pnl = position.entryCash - position.entryFees + closeValue - closeFees
		}
		values := historicalOptionSignalValues(indicators[index], frame, position, pnl)
		snapshot := rules.Snapshot{Values: values}
		closed := false
		if position != nil {
			shouldClose := historicalManagementClose(position, config.Management, frame.DecisionAt, pnl)
			if !shouldClose {
				shouldClose = rules.EvaluateGroup(config.Exit, snapshot, previous)
			}
			if shouldClose {
				cash += closeValue - closeFees
				realized += pnl
				orderAttempts += len(position.spread.Legs)
				orderFills += len(position.spread.Legs)
				position = nil
				result.ClosedPackages++
				closed = true
			}
		}
		if position == nil && !closed && rules.EvaluateGroup(config.Entry, snapshot, previous) {
			opened, err := openObservedVertical(config, frame, feePerContract)
			if err != nil {
				return nil, err
			}
			if opened.maximumRisk <= initialCash {
				position = opened
				cash += opened.entryCash - opened.entryFees
				orderAttempts += len(opened.spread.Legs)
				orderFills += len(opened.spread.Legs)
				result.OpenedPackages++
			}
		}

		marketValue, unrealized := 0.0, 0.0
		if position != nil {
			var err error
			marketValue, closeFees, err = observedLiquidationValue(position.spread, frame, position.feePerContract)
			if err != nil {
				return nil, err
			}
			unrealized = position.entryCash - position.entryFees + marketValue - closeFees
		}
		equity := cash + marketValue
		result.EquityCurve = append(result.EquityCurve, backtest.EquityPoint{
			Timestamp: frame.DecisionAt, Cash: cash, MarketValue: marketValue, Equity: equity,
			UnrealizedPnL: unrealized, RealizedPnL: realized, TotalPnL: equity - initialCash,
		})
		copyValue := cloneSnapshot(snapshot)
		previous = &copyValue
	}

	if position != nil {
		last := frames[len(frames)-1]
		closeValue, closeFees, err := observedLiquidationValue(position.spread, last, position.feePerContract)
		if err != nil {
			return nil, err
		}
		pnl := position.entryCash - position.entryFees + closeValue - closeFees
		cash += closeValue - closeFees
		realized += pnl
		orderAttempts += len(position.spread.Legs)
		orderFills += len(position.spread.Legs)
		result.ClosedPackages++
		lastPoint := &result.EquityCurve[len(result.EquityCurve)-1]
		lastPoint.Cash, lastPoint.MarketValue, lastPoint.Equity = cash, 0, cash
		lastPoint.UnrealizedPnL, lastPoint.RealizedPnL, lastPoint.TotalPnL = 0, realized, cash-initialCash
	}

	result.Metrics = backtest.ComputeMetrics(result.EquityCurve, bars)
	result.Metrics.OrderAttempts = orderAttempts
	result.Metrics.OrderFills = orderFills
	if orderAttempts > 0 {
		result.Metrics.FillRate = float64(orderFills) / float64(orderAttempts)
	}
	result.PayloadSHA256 = make([]string, 0, len(content))
	for digest := range content {
		result.PayloadSHA256 = append(result.PayloadSHA256, digest)
	}
	sort.Strings(result.PayloadSHA256)
	result.Evidence = make([]data.ManifestPayloadReceipt, 0, len(evidenceByID))
	for _, observation := range evidenceByID {
		result.Evidence = append(result.Evidence, observation)
	}
	sort.Slice(result.Evidence, func(i, j int) bool {
		left, right := result.Evidence[i], result.Evidence[j]
		if left.PartitionSequence != right.PartitionSequence {
			return left.PartitionSequence < right.PartitionSequence
		}
		if left.ObservationSequence != right.ObservationSequence {
			return left.ObservationSequence < right.ObservationSequence
		}
		return left.PayloadID.String() < right.PayloadID.String()
	})
	return result, nil
}

func sameHistoricalReceiptParent(left, right data.ManifestOptionChainReceipt) bool {
	return left.ScopeID == right.ScopeID && left.AccountID == right.AccountID && left.ManifestID == right.ManifestID &&
		left.ManifestSHA256 == right.ManifestSHA256 && left.QualityResultID == right.QualityResultID && left.QualitySHA256 == right.QualitySHA256
}

func openObservedVertical(config rules.OptionsRulesConfig, frame HistoricalOptionFrame, feePerContract float64) (*historicalObservedPosition, error) {
	selected, err := rules.SelectSpreadLegs(frame.Chain, config.LegSelection, frame.DecisionAt)
	if err != nil {
		return nil, fmt.Errorf("options/historical: select observed vertical: %w", err)
	}
	spread, err := rules.BuildSpread(config.StrategyType, config.Underlying, selected, config.LegSelection)
	if err != nil {
		return nil, fmt.Errorf("options/historical: build observed vertical: %w", err)
	}
	entryCash, fees := 0.0, 0.0
	for index := range spread.Legs {
		leg := &spread.Legs[index]
		source := selectedSnapshotBySymbol(selected, leg.Contract.OCCSymbol)
		if source == nil {
			return nil, fmt.Errorf("options/historical: selected leg payload is missing")
		}
		leg.ContractPayloadID, leg.ContractSHA256 = source.ContractPayloadID, source.ContractSHA256
		leg.QuotePayloadID, leg.QuoteSHA256 = source.QuotePayloadID, source.QuoteSHA256
		leg.SnapshotPayloadID, leg.SnapshotSHA256 = source.SnapshotPayloadID, source.SnapshotSHA256
		leg.Bid, leg.BidSize, leg.Ask, leg.AskSize, leg.QuoteObservedAt, leg.Greeks = source.Bid, source.BidSize, source.Ask, source.AskSize, source.QuoteObservedAt, source.Greeks
		price := source.Ask
		if leg.Side == domain.OrderSideSell {
			price = source.Bid
		}
		leg.ExecutablePrice = price
		amount := price * leg.Contract.Multiplier * leg.Quantity
		if leg.Side == domain.OrderSideSell {
			entryCash += amount
		} else {
			entryCash -= amount
		}
		fees += feePerContract * leg.Quantity
	}
	maximumReward, maximumRisk := spreadRiskReward(spread, entryCash)
	roundTripFees := fees * 2
	maximumRisk += roundTripFees
	maximumReward = math.Max(maximumReward-roundTripFees, 0)
	if maximumRisk <= 0 || math.IsNaN(maximumRisk) || math.IsInf(maximumRisk, 0) {
		return nil, fmt.Errorf("options/historical: observed vertical maximum loss is undefined")
	}
	spread.MaxRisk, spread.MaxReward, spread.QuoteObservedAt = maximumRisk, maximumReward, frame.DecisionAt
	return &historicalObservedPosition{spread: spread, entryCash: entryCash, entryFees: fees, feePerContract: feePerContract, maximumRisk: maximumRisk, maximumReward: maximumReward}, nil
}

func selectedSnapshotBySymbol(selected map[string]*domain.OptionSnapshot, symbol string) *domain.OptionSnapshot {
	for _, snapshot := range selected {
		if snapshot.Contract.OCCSymbol == symbol {
			return snapshot
		}
	}
	return nil
}

func observedLiquidationValue(spread *domain.OptionSpread, frame HistoricalOptionFrame, feePerContract float64) (float64, float64, error) {
	bySymbol := make(map[string]domain.OptionSnapshot, len(frame.Chain))
	for _, snapshot := range frame.Chain {
		bySymbol[snapshot.Contract.OCCSymbol] = snapshot
	}
	value, fees := 0.0, 0.0
	for _, leg := range spread.Legs {
		premium := 0.0
		if current, ok := bySymbol[leg.Contract.OCCSymbol]; ok {
			if leg.Side == domain.OrderSideBuy {
				premium = current.Bid
			} else {
				premium = current.Ask
			}
		} else if !leg.Contract.Expiry.After(frame.DecisionAt) {
			premium = observedIntrinsic(leg.Contract, frame.Underlying.Close)
		} else {
			return 0, 0, fmt.Errorf("options/historical: open contract %q is missing before expiry", leg.Contract.OCCSymbol)
		}
		amount := premium * leg.Contract.Multiplier * leg.Quantity
		if leg.Side == domain.OrderSideBuy {
			value += amount
		} else {
			value -= amount
		}
		fees += feePerContract * leg.Quantity
	}
	return value, fees, nil
}

func observedIntrinsic(contract domain.OptionContract, underlying float64) float64 {
	if contract.OptionType == domain.OptionTypeCall {
		return math.Max(underlying-contract.Strike, 0)
	}
	return math.Max(contract.Strike-underlying, 0)
}

func historicalManagementClose(position *historicalObservedPosition, management rules.OptionsManagement, now time.Time, pnl float64) bool {
	if management.CloseAtProfitPct > 0 && position.maximumReward > 0 {
		ratio := management.CloseAtProfitPct
		if ratio > 1 {
			ratio /= 100
		}
		if pnl >= position.maximumReward*ratio {
			return true
		}
	}
	if management.StopLossPct > 0 {
		ratio := management.StopLossPct
		if ratio > 1 {
			ratio /= 100
		}
		if -pnl >= position.maximumRisk*ratio {
			return true
		}
	}
	return management.CloseAtDTE > 0 && len(position.spread.Legs) > 0 && int(position.spread.Legs[0].Contract.Expiry.Sub(now).Hours()/24) <= management.CloseAtDTE
}

func historicalOptionSignalValues(indicators map[string]float64, frame HistoricalOptionFrame, position *historicalObservedPosition, pnl float64) map[string]float64 {
	bar := frame.Underlying
	values := map[string]float64{"close": bar.Close, "open": bar.Open, "high": bar.High, "low": bar.Low, "volume": bar.Volume}
	for key, value := range indicators {
		values[key] = value
	}
	atmIV, putCallRatio := chainMetrics(frame.Chain, bar.Close)
	values["atm_iv"], values["put_call_ratio"] = atmIV, putCallRatio
	if position != nil {
		values["pnl_pct"] = pnl / position.maximumRisk * 100
		values["dte"] = position.spread.Legs[0].Contract.Expiry.Sub(frame.DecisionAt).Hours() / 24
	}
	return values
}

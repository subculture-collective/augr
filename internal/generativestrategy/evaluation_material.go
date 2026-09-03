package generativestrategy

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/evaluation"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
)

type EvaluationMaterialInput struct {
	Prepared   *PreparedResearch
	Graph      *experimentrun.EvidenceGraph
	Plan       *experimentrun.Plan
	Result     *experimentrun.Result
	Lifecycles map[uuid.UUID]*lifecycle.Aggregate
	Policy     *evaluation.Policy
}

type evaluationPosition struct {
	instrumentID uuid.UUID
	quantity     decimal.Decimal
	entryPrice   decimal.Decimal
	markPrice    decimal.Decimal
	multiplier   decimal.Decimal
	entryFees    decimal.Decimal
	entryAt      time.Time
	entryFillIDs []uuid.UUID
}

// BuildEvaluationMaterial reconstructs portfolio observations and FIFO closed
// trades from the exact persisted experiment plan and common execution
// lifecycles. It never infers a fill from an intended order or calls a data
// provider. V1 generated programs are long-only and hold at most one lot per
// instrument, so any short, crossing, or partial-exit graph fails closed.
func BuildEvaluationMaterial(input EvaluationMaterialInput) (evaluation.ReportInput, error) {
	if input.Prepared == nil || input.Prepared.Scenario == nil || input.Prepared.Experiment == nil || input.Graph == nil ||
		input.Graph.CapitalState == nil || input.Plan == nil || input.Result == nil || input.Policy == nil || input.Lifecycles == nil {
		return evaluation.ReportInput{}, fmt.Errorf("generated evaluation requires complete prepared, capital, plan, result, lifecycle, and policy evidence")
	}
	scenario, experiment := input.Prepared.Scenario, input.Prepared.Experiment
	if input.Result.ExperimentID() != experiment.ID() || input.Result.PlanID() != input.Plan.ID() || input.Result.AccountID() != experiment.AccountID() ||
		input.Result.ManifestID() != scenario.ManifestID() || input.Plan.ExperimentID() != experiment.ID() || input.Plan.AccountID() != experiment.AccountID() ||
		!input.Plan.EvaluationStart().Equal(scenario.EvaluationStart()) || !input.Plan.EvaluationEnd().Equal(scenario.EvaluationEnd()) ||
		input.Plan.CapitalStateID() != input.Graph.CapitalState.ID() || input.Plan.CapitalStateSHA256() != input.Graph.CapitalState.Hash() {
		return evaluation.ReportInput{}, fmt.Errorf("generated evaluation identity graph does not reconstruct")
	}
	frames, steps, outcomes := scenario.ExecutionEvidence(), input.Plan.Steps(), input.Result.Outcomes()
	if len(frames) != len(steps) || len(steps) != len(outcomes) || len(frames) == 0 {
		return evaluation.ReportInput{}, fmt.Errorf("generated evaluation scenario, plan, and result sequences differ")
	}
	initialEquity := input.Graph.CapitalState.Equity()
	if !initialEquity.IsPositive() {
		return evaluation.ReportInput{}, fmt.Errorf("generated evaluation initial equity is invalid")
	}
	state := evaluationReplayState{cash: initialEquity, initialEquity: initialEquity, positions: map[uuid.UUID]*evaluationPosition{}}
	observations := []evaluation.ObservationInput{state.observation(scenario.EvaluationStart(), input.Graph.CapitalState.ID(), input.Graph.CapitalState.Hash())}
	for sequence, frame := range frames {
		if err := state.applyStep(sequence, frame, steps[sequence], outcomes[sequence], input); err != nil {
			return evaluation.ReportInput{}, err
		}
		if !frame.AvailableAt.After(scenario.EvaluationStart()) || frame.AvailableAt.After(scenario.EvaluationEnd()) {
			return evaluation.ReportInput{}, fmt.Errorf("generated evaluation frame %d falls outside observation interval", sequence)
		}
		observation := state.observation(frame.AvailableAt, frame.PayloadID, frame.PayloadSHA256)
		if observation.ObservedAt.Equal(observations[len(observations)-1].ObservedAt) {
			observations[len(observations)-1] = observation
		} else {
			observations = append(observations, observation)
		}
	}
	lastEvidenceID, lastEvidenceSHA := frames[len(frames)-1].PayloadID, frames[len(frames)-1].PayloadSHA256
	end := state.observation(scenario.EvaluationEnd(), lastEvidenceID, lastEvidenceSHA)
	if end.ObservedAt.Equal(observations[len(observations)-1].ObservedAt) {
		observations[len(observations)-1] = end
	} else {
		observations = append(observations, end)
	}
	metrics := input.Result.Metrics()
	return evaluation.ReportInput{
		Policy: input.Policy, EvaluationStart: scenario.EvaluationStart(), EvaluationEnd: scenario.EvaluationEnd(),
		OpenLotCount: len(state.positions), Execution: evaluation.ExecutionInput{
			AttemptedOrders: decimal.NewFromInt(int64(metrics.OrderCount)).String(), FilledOrders: decimal.NewFromInt(int64(state.filledOrders)).String(),
			AttemptedQuantity: state.attemptedQuantity.String(), FilledQuantity: decimal.RequireFromString(metrics.FilledQuantity).String(),
		}, Observations: observations, ClosedTrades: state.closedTrades,
	}, nil
}

type evaluationReplayState struct {
	cash               decimal.Decimal
	initialEquity      decimal.Decimal
	positions          map[uuid.UUID]*evaluationPosition
	cumulativeFees     decimal.Decimal
	cumulativeTurnover decimal.Decimal
	attemptedQuantity  decimal.Decimal
	filledOrders       int
	closedTrades       []evaluation.ClosedTradeInput
}

func (state *evaluationReplayState) applyStep(sequence int, frame ScenarioExecutionEvidence, step experimentrun.StepInput, outcome experimentrun.StepOutcomeInput, input EvaluationMaterialInput) error {
	if step.Action != outcome.Action || outcome.DecisionSHA256 != input.Plan.DecisionSHA256(sequence) {
		return fmt.Errorf("generated evaluation step %d decision or action differs", sequence)
	}
	mark, err := exactDecimal(frame.ExecutionPrice)
	if err != nil || !mark.IsPositive() {
		return fmt.Errorf("generated evaluation frame %d mark is invalid", sequence)
	}
	if position := state.positions[frame.InstrumentID]; position != nil {
		position.markPrice = mark
	}
	if step.Intent == nil {
		if len(outcome.FillIDs) != 0 {
			return fmt.Errorf("generated evaluation non-order step %d has fills", sequence)
		}
		return nil
	}
	attempted, err := exactDecimal(step.Intent.Quantity)
	if err != nil || !attempted.IsPositive() {
		return fmt.Errorf("generated evaluation step %d attempted quantity is invalid", sequence)
	}
	state.attemptedQuantity = state.attemptedQuantity.Add(attempted)
	if len(outcome.FillIDs) == 0 {
		return nil
	}
	intentID, orderID := input.Plan.IntentID(sequence), input.Plan.OrderID(sequence)
	aggregate := input.Lifecycles[intentID]
	if aggregate == nil || aggregate.Order == nil || aggregate.Intent.ID != intentID || aggregate.Order.ID != orderID ||
		aggregate.Intent.AccountID != input.Result.AccountID() || aggregate.Intent.InstrumentID != frame.InstrumentID || aggregate.Order.VenueContractID != frame.VenueContractID ||
		aggregate.State != lifecycle.StateFilled || len(aggregate.Fills) != len(outcome.FillIDs) {
		return fmt.Errorf("generated evaluation step %d lifecycle does not reconstruct", sequence)
	}
	contract := input.Graph.VenueContracts[frame.VenueContractID]
	if contract == nil || contract.InstrumentID != frame.InstrumentID || !contract.Multiplier.IsPositive() {
		return fmt.Errorf("generated evaluation step %d contract is unavailable", sequence)
	}
	feeTotal, err := exactDecimal(outcome.FeeTotal)
	if err != nil || feeTotal.IsNegative() {
		return fmt.Errorf("generated evaluation step %d fee total is invalid", sequence)
	}
	quantity, notional, weightedPrice := decimal.Zero, decimal.Zero, decimal.Zero
	fillIDs := make([]uuid.UUID, len(aggregate.Fills))
	for index, fill := range aggregate.Fills {
		if err := fill.Validate(); err != nil {
			return fmt.Errorf("generated evaluation step %d fill %d is invalid: %w", sequence, index, err)
		}
		if fill.ID != outcome.FillIDs[index] || fill.InstrumentID != frame.InstrumentID || fill.VenueContractID != frame.VenueContractID || fill.Side != aggregate.Order.Side {
			return fmt.Errorf("generated evaluation step %d fill %d differs", sequence, index)
		}
		quantity = quantity.Add(fill.Quantity)
		notional = notional.Add(fill.Quantity.Mul(fill.Price).Mul(contract.Multiplier))
		weightedPrice = weightedPrice.Add(fill.Quantity.Mul(fill.Price))
		fillIDs[index] = fill.ID
	}
	if !quantity.IsPositive() || !quantity.Equal(decimal.RequireFromString(outcome.FilledQuantity)) {
		return fmt.Errorf("generated evaluation step %d filled quantity differs", sequence)
	}
	weightedPrice = weightedPrice.Div(quantity)
	state.cumulativeFees = state.cumulativeFees.Add(feeTotal)
	state.cumulativeTurnover = state.cumulativeTurnover.Add(notional.Abs().Div(state.initialEquity))
	state.filledOrders++
	switch aggregate.Order.Side {
	case lifecycle.SideBuy:
		if state.positions[frame.InstrumentID] != nil {
			return fmt.Errorf("generated evaluation step %d adds to an existing v1 lot", sequence)
		}
		state.cash = state.cash.Sub(notional).Sub(feeTotal)
		state.positions[frame.InstrumentID] = &evaluationPosition{instrumentID: frame.InstrumentID, quantity: quantity, entryPrice: weightedPrice, markPrice: mark, multiplier: contract.Multiplier, entryFees: feeTotal, entryAt: aggregate.Fills[0].EffectiveAt, entryFillIDs: fillIDs}
	case lifecycle.SideSell:
		position := state.positions[frame.InstrumentID]
		if position == nil || !position.quantity.Equal(quantity) || !position.multiplier.Equal(contract.Multiplier) {
			return fmt.Errorf("generated evaluation step %d is not an exact v1 lot exit", sequence)
		}
		state.cash = state.cash.Add(notional).Sub(feeTotal)
		gross := weightedPrice.Sub(position.entryPrice).Mul(quantity).Mul(contract.Multiplier)
		state.closedTrades = append(state.closedTrades, evaluation.ClosedTradeInput{
			InstrumentID: frame.InstrumentID, Side: "long", Quantity: quantity.String(), EntryFillIDs: append([]uuid.UUID(nil), position.entryFillIDs...), ExitFillIDs: fillIDs,
			EntryAt: position.entryAt, ExitAt: aggregate.Fills[len(aggregate.Fills)-1].EffectiveAt, EntryPrice: position.entryPrice.String(), ExitPrice: weightedPrice.String(),
			EntryFees: position.entryFees.String(), ExitFees: feeTotal.String(), OtherOwnershipCost: "0", GrossPnL: gross.String(), AfterCostPnL: gross.Sub(position.entryFees).Sub(feeTotal).String(),
		})
		delete(state.positions, frame.InstrumentID)
	default:
		return fmt.Errorf("generated evaluation step %d has unsupported side", sequence)
	}
	return nil
}

func (state *evaluationReplayState) observation(at time.Time, evidenceID uuid.UUID, evidenceSHA string) evaluation.ObservationInput {
	marketValue, gross, net, largest := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
	keys := make([]uuid.UUID, 0, len(state.positions))
	for id := range state.positions {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, id := range keys {
		position := state.positions[id]
		value := position.quantity.Mul(position.markPrice).Mul(position.multiplier)
		marketValue = marketValue.Add(value)
		gross = gross.Add(value.Abs())
		net = net.Add(value)
		if value.Abs().GreaterThan(largest) {
			largest = value.Abs()
		}
	}
	equity := state.cash.Add(marketValue)
	weight := decimal.Zero
	if equity.IsPositive() {
		weight = largest.Div(equity)
	}
	return evaluation.ObservationInput{
		ObservedAt: at, Equity: equity.String(), BenchmarkValue: state.initialEquity.String(), CashReturn: "0", GrossExposure: gross.String(), NetExposure: net.String(),
		LargestPositionWeight: weight.String(), CumulativeOwnershipCost: state.cumulativeFees.String(), CumulativeTurnover: state.cumulativeTurnover.String(),
		CumulativeModeledSlippage: "0", EvidenceID: evidenceID, EvidenceSHA256: evidenceSHA,
	}
}

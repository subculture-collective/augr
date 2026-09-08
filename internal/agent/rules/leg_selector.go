package rules

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

// SelectLeg picks the contract from the chain that best matches the selector
// criteria. It filters by option type and DTE range, then returns the contract
// whose absolute delta is closest to the target.
func SelectLeg(chain []domain.OptionSnapshot, selector LegSelector, now time.Time) (*domain.OptionSnapshot, error) {
	if len(chain) == 0 {
		return nil, fmt.Errorf("leg_selector: empty chain")
	}

	var candidates []domain.OptionSnapshot

	for _, snap := range chain {
		// Filter by option type.
		if snap.Contract.OptionType != selector.OptionType {
			continue
		}
		// Filter by DTE range.
		dte := daysToExpiry(snap.Contract.Expiry, now)
		if dte < selector.DTEMin || dte > selector.DTEMax {
			continue
		}
		candidates = append(candidates, snap)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("leg_selector: no contracts match option_type=%s dte=[%d,%d]",
			selector.OptionType, selector.DTEMin, selector.DTEMax)
	}

	// Find the candidate whose |delta| is closest to the target.
	best := candidates[0]
	bestDist := math.Abs(math.Abs(best.Greeks.Delta) - math.Abs(selector.DeltaTarget))

	for _, c := range candidates[1:] {
		dist := math.Abs(math.Abs(c.Greeks.Delta) - math.Abs(selector.DeltaTarget))
		if dist < bestDist {
			bestDist = dist
			best = c
		}
	}

	return &best, nil
}

// SelectSpreadLegs selects all legs for a spread strategy from the chain.
func SelectSpreadLegs(chain []domain.OptionSnapshot, selectors map[string]LegSelector, now time.Time) (map[string]*domain.OptionSnapshot, error) {
	if len(selectors) == 0 {
		return nil, fmt.Errorf("leg_selector: no leg selectors provided")
	}
	if len(selectors) == 2 {
		names := make([]string, 0, 2)
		for name := range selectors {
			names = append(names, name)
		}
		sort.Strings(names)
		first, second := selectors[names[0]], selectors[names[1]]
		if first.DTEMin == second.DTEMin && first.DTEMax == second.DTEMax {
			return selectSameExpiryPair(chain, names, first, second, now)
		}
	}

	result := make(map[string]*domain.OptionSnapshot, len(selectors))
	for name, sel := range selectors {
		snap, err := SelectLeg(chain, sel, now)
		if err != nil {
			return nil, fmt.Errorf("leg_selector: leg %q: %w", name, err)
		}
		result[name] = snap
	}
	return result, nil
}

func selectSameExpiryPair(chain []domain.OptionSnapshot, names []string, first, second LegSelector, now time.Time) (map[string]*domain.OptionSnapshot, error) {
	var bestA, bestB *domain.OptionSnapshot
	bestScore := math.Inf(1)
	bestKey := ""
	for firstIndex := range chain {
		a := &chain[firstIndex]
		if !legMatchesSelector(*a, first, now) {
			continue
		}
		for secondIndex := range chain {
			b := &chain[secondIndex]
			if optionContractSelectionKey(a.Contract) == optionContractSelectionKey(b.Contract) || !a.Contract.Expiry.Equal(b.Contract.Expiry) || !legMatchesSelector(*b, second, now) {
				continue
			}
			score := math.Abs(math.Abs(a.Greeks.Delta)-math.Abs(first.DeltaTarget)) + math.Abs(math.Abs(b.Greeks.Delta)-math.Abs(second.DeltaTarget))
			key := a.Contract.Expiry.UTC().Format(time.RFC3339Nano) + "\x00" + optionContractSelectionKey(a.Contract) + "\x00" + optionContractSelectionKey(b.Contract)
			if score < bestScore || score == bestScore && (bestKey == "" || key < bestKey) {
				bestA, bestB, bestScore, bestKey = a, b, score, key
			}
		}
	}
	if bestA == nil || bestB == nil {
		return nil, fmt.Errorf("leg_selector: no distinct same-expiry contract pair matches both selectors")
	}
	return map[string]*domain.OptionSnapshot{names[0]: bestA, names[1]: bestB}, nil
}

func optionContractSelectionKey(contract domain.OptionContract) string {
	if contract.OCCSymbol != "" {
		return contract.OCCSymbol
	}
	return fmt.Sprintf("%s:%s:%.8f:%s", contract.Underlying, contract.OptionType, contract.Strike, contract.Expiry.UTC().Format(time.RFC3339Nano))
}

func legMatchesSelector(snapshot domain.OptionSnapshot, selector LegSelector, now time.Time) bool {
	if snapshot.Contract.OptionType != selector.OptionType {
		return false
	}
	dte := daysToExpiry(snapshot.Contract.Expiry, now)
	return dte >= selector.DTEMin && dte <= selector.DTEMax
}

// BuildSpread constructs an OptionSpread from selected legs.
func BuildSpread(
	strategyType domain.OptionStrategyType,
	underlying string,
	selectedLegs map[string]*domain.OptionSnapshot,
	selectors map[string]LegSelector,
) (*domain.OptionSpread, error) {
	if len(selectedLegs) == 0 {
		return nil, fmt.Errorf("leg_selector: no selected legs")
	}

	spread := &domain.OptionSpread{
		StrategyType: strategyType,
		Underlying:   underlying,
	}

	names := make([]string, 0, len(selectedLegs))
	for name := range selectedLegs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		snap := selectedLegs[name]
		sel, ok := selectors[name]
		if !ok {
			return nil, fmt.Errorf("leg_selector: missing selector for leg %q", name)
		}
		ratio := sel.Ratio
		if ratio < 1 {
			ratio = 1
		}
		leg := domain.SpreadLeg{
			Contract:       snap.Contract,
			Side:           sel.Side,
			PositionIntent: sel.Intent,
			Ratio:          ratio,
			Quantity:       float64(ratio),
		}
		spread.Legs = append(spread.Legs, leg)
	}

	return spread, nil
}

// daysToExpiry returns the number of calendar days from now until expiry.
func daysToExpiry(expiry, now time.Time) int {
	d := expiry.Sub(now).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(d)
}

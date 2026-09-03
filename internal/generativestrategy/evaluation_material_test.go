package generativestrategy

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestEvaluationReplayObservationUsesCashMarksAndCanonicalExposure(t *testing.T) {
	instrumentID := uuid.New()
	state := evaluationReplayState{
		cash:               decimal.RequireFromString("8999"),
		initialEquity:      decimal.RequireFromString("10000"),
		cumulativeFees:     decimal.RequireFromString("1"),
		cumulativeTurnover: decimal.RequireFromString("0.1"),
		positions: map[uuid.UUID]*evaluationPosition{
			instrumentID: {instrumentID: instrumentID, quantity: decimal.NewFromInt(10), markPrice: decimal.RequireFromString("101"), multiplier: decimal.NewFromInt(1)},
		},
	}
	at := time.Date(2026, 9, 3, 20, 0, 0, 0, time.UTC)
	evidenceID := uuid.New()
	observation := state.observation(at, evidenceID, string(make([]byte, 64)))
	if observation.Equity != "10009" || observation.GrossExposure != "1010" || observation.NetExposure != "1010" ||
		observation.LargestPositionWeight != decimal.RequireFromString("1010").Div(decimal.RequireFromString("10009")).String() ||
		observation.CumulativeOwnershipCost != "1" || observation.CumulativeTurnover != "0.1" || observation.EvidenceID != evidenceID {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestBuildEvaluationMaterialRequiresCompleteExactGraph(t *testing.T) {
	if material, err := BuildEvaluationMaterial(EvaluationMaterialInput{}); err == nil || material.Policy != nil {
		t.Fatalf("material=%+v err=%v", material, err)
	}
}

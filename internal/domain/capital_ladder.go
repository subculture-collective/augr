package domain

import "time"

const (
	DefaultCapitalLadderStep      = 0.10
	DefaultCapitalLadderMaxStep   = 1.0
	DefaultCapitalLadderTolerance = 0.03
)

type CapitalLadderEntry struct {
	StrategyID       string     `json:"strategy_id"`
	StepPct          float64    `json:"step_pct"`
	FillRate         float64    `json:"fill_rate"`
	WinRate          float64    `json:"win_rate"`
	DrawdownPct      float64    `json:"drawdown_pct"`
	BaselineFillRate float64    `json:"baseline_fill_rate"`
	BaselineWinRate  float64    `json:"baseline_win_rate"`
	AdvancedAt       *time.Time `json:"advanced_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

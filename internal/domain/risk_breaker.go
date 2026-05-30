package domain

import "time"

type RiskBreakerState struct {
	Scope     string     `json:"scope"`
	TrippedAt time.Time  `json:"tripped_at"`
	Reason    string     `json:"reason"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

const RiskBreakerScopeGlobal = "global"

func RiskBreakerScopeStrategy(id string) string { return "strategy:" + id }

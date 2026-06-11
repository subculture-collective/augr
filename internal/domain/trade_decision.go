package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type TradeDecisionStatus string

const (
	TradeDecisionStatusCandidate TradeDecisionStatus = "candidate"
	TradeDecisionStatusRejected  TradeDecisionStatus = "rejected"
	TradeDecisionStatusPaper     TradeDecisionStatus = "paper_ordered"
	TradeDecisionStatusLive      TradeDecisionStatus = "live_ordered"
	TradeDecisionStatusClosed    TradeDecisionStatus = "closed"
)

type RiskDecisionStatus string

const (
	RiskDecisionApproved RiskDecisionStatus = "approved"
	RiskDecisionRejected RiskDecisionStatus = "rejected"
)

type TradeDecision struct {
	ID               uuid.UUID           `json:"id"`
	StrategyID       *uuid.UUID          `json:"strategy_id,omitempty"`
	PipelineRunID    *uuid.UUID          `json:"pipeline_run_id,omitempty"`
	MarketType       MarketType          `json:"market_type"`
	InstrumentKey    string              `json:"instrument_key"`
	ExternalMarketID string              `json:"external_market_id,omitempty"`
	Side             OrderSide           `json:"side"`
	Outcome          string              `json:"outcome,omitempty"`
	FairValue        float64             `json:"fair_value"`
	ExecutablePrice  float64             `json:"executable_price"`
	Spread           float64             `json:"spread"`
	Depth            float64             `json:"depth"`
	GrossEV          float64             `json:"gross_ev"`
	NetEV            float64             `json:"net_ev"`
	KellyFraction    float64             `json:"kelly_fraction"`
	ProposedSize     float64             `json:"proposed_size"`
	ApprovedSize     float64             `json:"approved_size"`
	RiskStatus       RiskDecisionStatus  `json:"risk_status"`
	RiskReasons      []string            `json:"risk_reasons"`
	Evidence         json.RawMessage     `json:"evidence,omitempty"`
	Features         json.RawMessage     `json:"features,omitempty"`
	RegimeTags       []string            `json:"regime_tags"`
	PromptText       string              `json:"prompt_text,omitempty"`
	LLMProvider      string              `json:"llm_provider,omitempty"`
	LLMModel         string              `json:"llm_model,omitempty"`
	PromptTokens     *int                `json:"prompt_tokens,omitempty"`
	CompletionTokens *int                `json:"completion_tokens,omitempty"`
	LatencyMS        *int                `json:"latency_ms,omitempty"`
	CostUSD          *float64            `json:"cost_usd,omitempty"`
	PaperOrderID     *uuid.UUID          `json:"paper_order_id,omitempty"`
	LiveOrderID      *uuid.UUID          `json:"live_order_id,omitempty"`
	Status           TradeDecisionStatus `json:"status"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

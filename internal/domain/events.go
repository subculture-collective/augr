package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PipelineEvent represents a structured event emitted during a pipeline run.
type PipelineEvent struct {
	AccountID            uuid.UUID          `json:"account_id,omitzero"`
	Environment          AccountEnvironment `json:"environment,omitempty"`
	OriginType           string             `json:"origin_type,omitempty"`
	OriginID             string             `json:"origin_id,omitempty"`
	PipelineRunID        uuid.UUID          `json:"pipeline_run_id"`
	PipelineRunTradeDate time.Time          `json:"pipeline_run_trade_date,omitzero"`
	EventType            string             `json:"event_type"`
	Payload              json.RawMessage    `json:"payload,omitempty"`
	OccurredAt           time.Time          `json:"occurred_at"`
}

// EarningsEvent represents a scheduled or completed earnings report.
type EarningsEvent struct {
	Symbol          string    `json:"symbol"`
	Date            time.Time `json:"date"`
	Hour            string    `json:"hour"` // "bmo", "amc", "dmh"
	EPSEstimate     *float64  `json:"eps_estimate,omitempty"`
	EPSActual       *float64  `json:"eps_actual,omitempty"`
	RevenueEstimate *float64  `json:"revenue_estimate,omitempty"`
	RevenueActual   *float64  `json:"revenue_actual,omitempty"`
	Quarter         int       `json:"quarter"`
	Year            int       `json:"year"`
}

// SECFiling represents a filing submitted to the SEC.
type SECFiling struct {
	Symbol       string    `json:"symbol"`
	Form         string    `json:"form"`
	FiledDate    time.Time `json:"filed_date"`
	AcceptedDate time.Time `json:"accepted_date"`
	ReportDate   time.Time `json:"report_date"`
	URL          string    `json:"url"`
	AccessNumber string    `json:"access_number"`
}

// EconomicEvent represents a macroeconomic calendar event.
type EconomicEvent struct {
	Event    string    `json:"event"`
	Country  string    `json:"country"`
	Time     time.Time `json:"time"`
	Impact   string    `json:"impact"`
	Estimate *float64  `json:"estimate,omitempty"`
	Actual   *float64  `json:"actual,omitempty"`
	Previous *float64  `json:"previous,omitempty"`
	Unit     string    `json:"unit"`
}

// IPOEvent represents an upcoming or recent IPO.
type IPOEvent struct {
	Symbol        string    `json:"symbol"`
	Date          time.Time `json:"date"`
	Exchange      string    `json:"exchange"`
	Name          string    `json:"name"`
	PriceRange    string    `json:"price_range"`
	SharesOffered int64     `json:"shares_offered"`
	Status        string    `json:"status"`
}

// CorporateAction represents a corporate event such as a dividend or split.
type CorporateAction struct {
	Symbol      string    `json:"symbol"`
	ActionType  string    `json:"action_type"`
	ExDate      time.Time `json:"ex_date"`
	RecordDate  time.Time `json:"record_date"`
	PayableDate time.Time `json:"payable_date"`
	CashAmount  float64   `json:"cash_amount"`
}

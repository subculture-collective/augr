package domain

import (
	"encoding/json"
	"testing"
)

func TestLegacyRecordJSONOmitsZeroScopeFields(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{"agent decision", AgentDecision{}},
		{"agent event", AgentEvent{}},
		{"allocation decision", AllocationDecision{}},
		{"copy subscription", CopySubscription{}},
		{"copy trade intent", CopyTradeIntent{}},
		{"pipeline event", PipelineEvent{}},
		{"opportunity", Opportunity{}},
		{"order", Order{}},
		{"pipeline run", PipelineRun{}},
		{"pipeline snapshot", PipelineRunSnapshot{}},
		{"position", Position{}},
		{"replay event", ReplayEvent{}},
		{"trade", Trade{}},
		{"trade decision", TradeDecision{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			for _, field := range []string{"account_id", "environment", "pipeline_run_trade_date", "copy_origin_rebalance_run_id"} {
				if _, ok := fields[field]; ok {
					t.Errorf("legacy JSON includes zero scope field %q: %s", field, encoded)
				}
			}
		})
	}
}

package trader

import (
	"strings"
	"testing"
)

func TestParseTradingPlanPositionSizeUnits(t *testing.T) {
	t.Parallel()
	base := `{"action":"buy","ticker":"aapl","entry_type":"market","entry_price":200,"position_size":%s,"stop_loss":190,"take_profit":230,"time_horizon":"swing","confidence":0.8,"rationale":"r","risk_reward":3%s}`
	for name, tc := range map[string]struct {
		size, extra string
		wantShares  float64
		wantErr     string
	}{
		"default shares":  {size: "10", wantShares: 10},
		"explicit shares": {size: "10", extra: `,"position_size_unit":"shares"`, wantShares: 10},
		"usd converts":    {size: "5000", extra: `,"position_size_unit":"usd"`, wantShares: 25},
		"unknown unit":    {size: "10", extra: `,"position_size_unit":"lots"`, wantErr: "position_size_unit"},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := ParseTradingPlan(strings.Replace(strings.Replace(base, "%s", tc.size, 1), "%s", tc.extra, 1))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.PositionSize != tc.wantShares || plan.PositionSizeUnit != "shares" {
				t.Fatalf("position = %v %s, want %v shares", plan.PositionSize, plan.PositionSizeUnit, tc.wantShares)
			}
		})
	}
}

func TestTraderPromptStatesShareUnits(t *testing.T) {
	t.Parallel()
	if !strings.Contains(TraderSystemPrompt, "number of shares or contracts to trade, not dollars") || !strings.Contains(TraderSystemPrompt, "position_size_unit") {
		t.Fatal("trader prompt must state that position_size is in shares and document position_size_unit")
	}
}

package debate

import "testing"

func TestParseInvestmentPlanAcceptsEmptyEvidenceArrays(t *testing.T) {
	t.Parallel()
	plan, err := ParseInvestmentPlan(`{"direction":"hold","conviction":5,"key_evidence":[],"acknowledged_risks":[],"rationale":"wait"}`)
	if err != nil {
		t.Fatalf("ParseInvestmentPlan() error = %v, want empty arrays accepted", err)
	}
	if plan.Direction != "hold" {
		t.Fatalf("plan = %+v", plan)
	}
}

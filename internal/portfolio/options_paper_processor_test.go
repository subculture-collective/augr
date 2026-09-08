package portfolio

import (
	"errors"
	"testing"
	"time"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/google/uuid"
)

func TestDefinedRiskSpreadFromOpportunitySupportsOnlyFourVerticals(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		optionType  string
		longStrike  float64
		shortStrike float64
		want        domain.OptionStrategyType
	}{
		{"bull call", "call", 500, 505, domain.StrategyBullCallSpread},
		{"bear call", "call", 505, 500, domain.StrategyBearCallSpread},
		{"bear put", "put", 505, 500, domain.StrategyBearPutSpread},
		{"bull put", "put", 500, 505, domain.StrategyBullPutSpread},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opportunity := strongOptionOpportunity(now, now)
			opportunity.OptionLegs[0].OptionType = test.optionType
			opportunity.OptionLegs[1].OptionType = test.optionType
			opportunity.OptionLegs[0].Strike = test.longStrike
			opportunity.OptionLegs[1].Strike = test.shortStrike
			spread, err := DefinedRiskSpreadFromOpportunity(opportunity)
			if err != nil || spread.StrategyType != test.want || len(spread.Legs) != 2 || spread.MaxRisk != opportunity.MaxLossPerUnit {
				t.Fatalf("spread=%+v err=%v want=%s", spread, err, test.want)
			}
		})
	}
}

func TestDefinedRiskSpreadFromOpportunityRejectsNakedOrChangedPackage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	opportunity := strongOptionOpportunity(now, now)
	opportunity.OptionLegs = opportunity.OptionLegs[:1]
	if _, err := DefinedRiskSpreadFromOpportunity(opportunity); err == nil {
		t.Fatal("naked option package accepted")
	}
	opportunity = strongOptionOpportunity(now, now)
	opportunity.OptionLegs[1].Expiry = opportunity.OptionLegs[1].Expiry.AddDate(0, 1, 0)
	if _, err := DefinedRiskSpreadFromOpportunity(opportunity); err == nil {
		t.Fatal("mixed-expiry option package accepted")
	}
}

func TestOptionsPackageResultDistinguishesDefinitiveAndAmbiguousFailures(t *testing.T) {
	t.Parallel()
	submitErr := errors.New("provider result uncertain")
	for _, status := range []domain.OrderStatus{domain.OrderStatusRejected, domain.OrderStatusCancelled} {
		order := &domain.Order{ID: uuid.New(), Status: status}
		result, err := classifyOptionsPackageResult(order, submitErr)
		if err != nil || result.OrderID == nil || *result.OrderID != order.ID || result.Reason != "option_package_"+status.String() {
			t.Fatalf("definitive status %s result=%+v err=%v", status, result, err)
		}
	}
	order := &domain.Order{ID: uuid.New(), Status: domain.OrderStatusPending}
	result, err := classifyOptionsPackageResult(order, submitErr)
	if !errors.Is(err, submitErr) || result.OrderID == nil || *result.OrderID != order.ID {
		t.Fatalf("ambiguous result=%+v err=%v", result, err)
	}
}

type testPaperOptionsPreflightError struct{ code string }

func (e testPaperOptionsPreflightError) Error() string                       { return "provider detail" }
func (e testPaperOptionsPreflightError) PaperOptionsPreflightReason() string { return e.code }

func TestPaperOptionsPreflightReasonPreservesStableConstraint(t *testing.T) {
	t.Parallel()
	wrapped := errors.Join(errors.New("outer"), testPaperOptionsPreflightError{code: "account_identity_mismatch"})
	if got := paperOptionsPreflightReason(wrapped); got != "paper_options_preflight_account_identity_mismatch" {
		t.Fatalf("reason = %q", got)
	}
	if got := paperOptionsPreflightReason(errors.New("untyped")); got != "paper_options_preflight_rejected" {
		t.Fatalf("fallback reason = %q", got)
	}
}

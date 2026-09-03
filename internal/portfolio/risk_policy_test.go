package portfolio

import (
	"bytes"
	"strings"
	"testing"
)

func TestReviewedPortfolioRiskPolicyIsStableAndPreservesEnvelope(t *testing.T) {
	first, err := ReviewedPortfolioRiskPolicyV1()
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReviewedPortfolioRiskPolicyV1()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatal("reviewed portfolio risk policy is not deterministic")
	}
	if first.TargetGrossExposurePct != .35 || first.HardGrossExposurePct != .50 || first.CashReservePct != .20 ||
		first.MaxNewSelectionsPerRun != 2 || first.MaxNewSelectionsPerDay != 5 || !strings.HasPrefix(first.Reference(), PortfolioRiskPolicySchemaV1+"@sha256:") {
		t.Fatalf("reviewed policy = %+v reference=%s", first, first.Reference())
	}
}

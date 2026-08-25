package runcontrol

import (
	"errors"
	"testing"
)

func TestJoinCauseFromErrorMessageRecognizesOnlyStableCauseMessages(t *testing.T) {
	base := errors.New("lost terminal authority")
	joined := JoinCauseFromErrorMessage(base, Operator.Error())
	if !errors.Is(joined, base) || !errors.Is(joined, Operator) {
		t.Fatalf("JoinCauseFromErrorMessage() = %v, want base and Operator", joined)
	}

	for _, message := range []string{"operator", "prefix: " + Operator.Error(), Operator.Error() + ": suffix", "pipeline run cancelled: unknown"} {
		if got := JoinCauseFromErrorMessage(base, message); got != base {
			t.Fatalf("JoinCauseFromErrorMessage(%q) = %v, want unchanged base", message, got)
		}
	}
}

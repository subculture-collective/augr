package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/promotion"
)

func TestProjectedResearchLifecycleIsExplicitAndPreservesConfig(t *testing.T) {
	deploymentID, decisionID, scopeID, accountID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	raw, err := projectedResearchLifecycle(json.RawMessage(`{"rules_engine":{"name":"momentum"}}`), promotion.ActivationAction,
		deploymentID, decisionID, scopeID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got["rules_engine"]) == 0 || !strings.Contains(string(got["research_lifecycle"]), `"stage":"shadow"`) ||
		!strings.Contains(string(got["research_lifecycle"]), `"auto_activation_blocked":false`) ||
		!strings.Contains(string(got["research_lifecycle"]), scopeID.String()) {
		t.Fatalf("projected config = %s", raw)
	}
	suspended, err := projectedResearchLifecycle(raw, promotion.SuspensionAction, deploymentID, decisionID, scopeID, accountID)
	if err != nil || !strings.Contains(string(suspended), `"stage":"held"`) ||
		!strings.Contains(string(suspended), `"auto_activation_blocked":true`) {
		t.Fatalf("suspended config = %s err=%v", suspended, err)
	}
}

func TestPromotionActivationIsInertWhenDisabled(t *testing.T) {
	projection, err := (&PromotionRepo{}).ProjectAuthoritativeActivation(t.Context(), uuid.New(), uuid.New(), uuid.New(), false)
	if err != nil || projection == nil || projection.Changed || !strings.Contains(projection.Reason, "disabled") {
		t.Fatalf("disabled projection = %+v err=%v", projection, err)
	}
}

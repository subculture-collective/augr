package copytrading

import (
	"testing"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
)

func TestNewOrderManagerExecutorRetainsExecutionAccount(t *testing.T) {
	binding, err := domain.NewExecutionAccountBinding(uuid.New(), domain.AccountEnvironmentPaperScored)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewOrderManagerExecutor(OrderManagerExecutorDeps{ExecutionAccount: binding})
	if executor.deps.ExecutionAccount != binding {
		t.Fatal("executor did not retain execution account")
	}
}

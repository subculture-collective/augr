package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// ExecutionAccountBinding identifies the account owned by an execution graph.
type ExecutionAccountBinding struct {
	accountID   uuid.UUID
	environment AccountEnvironment
}

func NewExecutionAccountBinding(accountID uuid.UUID, environment AccountEnvironment) (ExecutionAccountBinding, error) {
	binding := ExecutionAccountBinding{accountID: accountID, environment: environment}
	if err := binding.Validate(); err != nil {
		return ExecutionAccountBinding{}, err
	}
	return binding, nil
}

func (b ExecutionAccountBinding) AccountID() uuid.UUID { return b.accountID }

func (b ExecutionAccountBinding) Environment() AccountEnvironment { return b.environment }

func (b ExecutionAccountBinding) Validate() error {
	if b.accountID == uuid.Nil {
		return fmt.Errorf("execution account ID is required")
	}
	if !b.environment.IsValid() {
		return fmt.Errorf("invalid execution environment %q", b.environment)
	}
	return nil
}

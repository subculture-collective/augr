package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestExecutionAccountBindingAcceptsEveryValidEnvironment(t *testing.T) {
	accountID := uuid.New()
	for _, environment := range []AccountEnvironment{
		AccountEnvironmentPaperScored,
		AccountEnvironmentPaperStress,
		AccountEnvironmentShadow,
		AccountEnvironmentLive,
	} {
		binding, err := NewExecutionAccountBinding(accountID, environment)
		if err != nil {
			t.Fatalf("NewExecutionAccountBinding(%q) error = %v", environment, err)
		}
		if binding.AccountID() != accountID || binding.Environment() != environment {
			t.Fatalf("binding = %s/%q, want %s/%q", binding.AccountID(), binding.Environment(), accountID, environment)
		}
		if err := binding.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	}
}

func TestExecutionAccountBindingRejectsInvalidIdentity(t *testing.T) {
	for _, input := range []struct {
		accountID   uuid.UUID
		environment AccountEnvironment
	}{
		{environment: AccountEnvironmentLive},
		{accountID: uuid.New()},
		{accountID: uuid.New(), environment: "invalid"},
	} {
		if _, err := NewExecutionAccountBinding(input.accountID, input.environment); err == nil {
			t.Fatal("NewExecutionAccountBinding() error = nil")
		}
	}
	if err := (ExecutionAccountBinding{}).Validate(); err == nil {
		t.Fatal("zero binding Validate() error = nil")
	}
}

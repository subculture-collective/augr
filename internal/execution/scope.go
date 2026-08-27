package execution

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

const nonRunOriginIDDomain = "execution-non-run-origin"

// ExecutionScope binds one execution graph to its account and origin.
type ExecutionScope struct {
	executionAccount domain.ExecutionAccountBinding
	originType       ledger.ExecutionOriginType
	originID         string
	pipelineRun      domain.PipelineRunRef
	hasPipelineRun   bool
	copyOriginRunID  uuid.UUID
}

func NewStrategyExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, strategyVersionID uuid.UUID, run domain.PipelineRunRef) (ExecutionScope, error) {
	binding, err := domain.NewExecutionAccountBinding(accountID, environment)
	if err != nil {
		return ExecutionScope{}, err
	}
	if strategyVersionID == uuid.Nil {
		return ExecutionScope{}, fmt.Errorf("strategy version ID is required")
	}
	if run.ID == uuid.Nil || run.TradeDate.IsZero() {
		return ExecutionScope{}, fmt.Errorf("complete pipeline run identity is required")
	}
	if run.TradeDate.Location() != time.UTC || run.TradeDate != run.TradeDate.Truncate(24*time.Hour) {
		return ExecutionScope{}, fmt.Errorf("pipeline run trade date must be UTC midnight")
	}
	return ExecutionScope{
		executionAccount: binding,
		originType:       ledger.ExecutionOriginStrategyVersion,
		originID:         strategyVersionID.String(),
		pipelineRun:      run,
		hasPipelineRun:   true,
	}, nil
}

func NewCopyExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, subscriptionID, copyOriginRunID uuid.UUID) (ExecutionScope, error) {
	binding, err := domain.NewExecutionAccountBinding(accountID, environment)
	if err != nil {
		return ExecutionScope{}, err
	}
	if subscriptionID == uuid.Nil {
		return ExecutionScope{}, fmt.Errorf("copy subscription ID is required")
	}
	if copyOriginRunID == uuid.Nil {
		return ExecutionScope{}, fmt.Errorf("copy origin rebalance run ID is required")
	}
	return ExecutionScope{
		executionAccount: binding,
		originType:       ledger.ExecutionOriginCopySubscription,
		originID:         subscriptionID.String(),
		copyOriginRunID:  copyOriginRunID,
	}, nil
}

func NewNonRunExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, originType ledger.ExecutionOriginType, originID string) (ExecutionScope, error) {
	binding, err := domain.NewExecutionAccountBinding(accountID, environment)
	if err != nil {
		return ExecutionScope{}, err
	}
	originID = strings.TrimSpace(originID)
	if originID == "" {
		return ExecutionScope{}, fmt.Errorf("execution origin ID is required")
	}
	switch originType {
	case ledger.ExecutionOriginPortfolioRebalance, ledger.ExecutionOriginRiskReduction,
		ledger.ExecutionOriginOperator, ledger.ExecutionOriginSettlement, ledger.ExecutionOriginReconciliation:
	default:
		return ExecutionScope{}, fmt.Errorf("execution origin type %q requires a run-specific scope", originType)
	}
	return ExecutionScope{
		executionAccount: binding,
		originType:       originType,
		originID:         originID,
	}, nil
}

// NewScheduledNonRunExecutionScope derives a stable origin ID from a scheduler occurrence identity.
func NewScheduledNonRunExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, originType ledger.ExecutionOriginType, occurrenceID string) (ExecutionScope, error) {
	occurrenceID = strings.TrimSpace(occurrenceID)
	if occurrenceID == "" {
		return ExecutionScope{}, fmt.Errorf("scheduler occurrence ID is required")
	}
	switch originType {
	case ledger.ExecutionOriginOperator, ledger.ExecutionOriginSettlement, ledger.ExecutionOriginReconciliation:
	default:
		return ExecutionScope{}, fmt.Errorf("execution origin type %q is not scheduled", originType)
	}
	originID := economicid.DeterministicUUID(
		nonRunOriginIDDomain+":"+string(originType),
		accountID.String(),
		string(environment),
		occurrenceID,
	).String()
	return NewNonRunExecutionScope(accountID, environment, originType, originID)
}

func (s ExecutionScope) AccountID() uuid.UUID { return s.executionAccount.AccountID() }

func (s ExecutionScope) Environment() domain.AccountEnvironment {
	return s.executionAccount.Environment()
}

func (s ExecutionScope) Origin() (ledger.ExecutionOriginType, string) {
	return s.originType, s.originID
}

func (s ExecutionScope) PipelineRun() (domain.PipelineRunRef, bool) {
	return s.pipelineRun, s.hasPipelineRun
}

func (s ExecutionScope) CopyOriginRunID() uuid.UUID { return s.copyOriginRunID }

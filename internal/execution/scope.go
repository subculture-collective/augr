package execution

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
)

const nonRunOriginIDDomain = "execution-non-run-origin"

// ExecutionScope binds one execution graph to its account and origin.
type ExecutionScope struct {
	accountID       uuid.UUID
	environment     domain.AccountEnvironment
	originType      ledger.ExecutionOriginType
	originID        string
	pipelineRun     domain.PipelineRunRef
	hasPipelineRun  bool
	copyOriginRunID uuid.UUID
}

func NewStrategyExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, strategyVersionID uuid.UUID, run domain.PipelineRunRef) (ExecutionScope, error) {
	if err := validateExecutionAccount(accountID, environment); err != nil {
		return ExecutionScope{}, err
	}
	if strategyVersionID == uuid.Nil {
		return ExecutionScope{}, fmt.Errorf("strategy version ID is required")
	}
	if run.ID == uuid.Nil || run.TradeDate.IsZero() {
		return ExecutionScope{}, fmt.Errorf("complete pipeline run identity is required")
	}
	return ExecutionScope{
		accountID:      accountID,
		environment:    environment,
		originType:     ledger.ExecutionOriginStrategyVersion,
		originID:       strategyVersionID.String(),
		pipelineRun:    run,
		hasPipelineRun: true,
	}, nil
}

func NewCopyExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, subscriptionID, copyOriginRunID uuid.UUID) (ExecutionScope, error) {
	if err := validateExecutionAccount(accountID, environment); err != nil {
		return ExecutionScope{}, err
	}
	if subscriptionID == uuid.Nil {
		return ExecutionScope{}, fmt.Errorf("copy subscription ID is required")
	}
	if copyOriginRunID == uuid.Nil {
		return ExecutionScope{}, fmt.Errorf("copy origin rebalance run ID is required")
	}
	return ExecutionScope{
		accountID:       accountID,
		environment:     environment,
		originType:      ledger.ExecutionOriginCopySubscription,
		originID:        subscriptionID.String(),
		copyOriginRunID: copyOriginRunID,
	}, nil
}

func NewNonRunExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, originType ledger.ExecutionOriginType, originID string) (ExecutionScope, error) {
	if err := validateExecutionAccount(accountID, environment); err != nil {
		return ExecutionScope{}, err
	}
	originID = strings.TrimSpace(originID)
	if originID == "" {
		return ExecutionScope{}, fmt.Errorf("execution origin ID is required")
	}
	switch originType {
	case ledger.ExecutionOriginPortfolioRebalance, ledger.ExecutionOriginRiskReduction:
	case ledger.ExecutionOriginOperator, ledger.ExecutionOriginSettlement, ledger.ExecutionOriginReconciliation:
		originID = economicid.DeterministicUUID(
			nonRunOriginIDDomain+":"+string(originType),
			accountID.String(),
			string(environment),
			originID,
		).String()
	default:
		return ExecutionScope{}, fmt.Errorf("execution origin type %q requires a run-specific scope", originType)
	}
	return ExecutionScope{
		accountID:   accountID,
		environment: environment,
		originType:  originType,
		originID:    originID,
	}, nil
}

func (s ExecutionScope) AccountID() uuid.UUID { return s.accountID }

func (s ExecutionScope) Environment() domain.AccountEnvironment { return s.environment }

func (s ExecutionScope) Origin() (ledger.ExecutionOriginType, string) {
	return s.originType, s.originID
}

func (s ExecutionScope) PipelineRun() (domain.PipelineRunRef, bool) {
	return s.pipelineRun, s.hasPipelineRun
}

func (s ExecutionScope) CopyOriginRunID() uuid.UUID { return s.copyOriginRunID }

func validateExecutionAccount(accountID uuid.UUID, environment domain.AccountEnvironment) error {
	if accountID == uuid.Nil {
		return fmt.Errorf("execution account ID is required")
	}
	if !environment.IsValid() {
		return fmt.Errorf("invalid execution environment %q", environment)
	}
	return nil
}

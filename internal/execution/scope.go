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
	legacyStrategyID *uuid.UUID
}

func NewStrategyExecutionScope(accountID uuid.UUID, environment domain.AccountEnvironment, strategyVersionID uuid.UUID, run domain.PipelineRunRef, legacyStrategyID ...uuid.UUID) (ExecutionScope, error) {
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
	scope := ExecutionScope{
		executionAccount: binding,
		originType:       ledger.ExecutionOriginStrategyVersion,
		originID:         strategyVersionID.String(),
		pipelineRun:      run,
		hasPipelineRun:   true,
	}
	if len(legacyStrategyID) > 1 || len(legacyStrategyID) == 1 && legacyStrategyID[0] == uuid.Nil {
		return ExecutionScope{}, fmt.Errorf("legacy strategy ID must be a non-nil optional UUID")
	}
	if len(legacyStrategyID) == 1 {
		id := legacyStrategyID[0]
		scope.legacyStrategyID = &id
	}
	return scope, nil
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

// ExecutionScopeFromOrder rebuilds the exact durable scope used for restart recovery.
func ExecutionScopeFromOrder(order domain.Order) (ExecutionScope, error) {
	originType := ledger.ExecutionOriginType(order.OriginType)
	switch originType {
	case ledger.ExecutionOriginStrategyVersion:
		originID, err := uuid.Parse(order.OriginID)
		if err != nil || order.PipelineRunID == nil || order.PipelineRunTradeDate == nil {
			return ExecutionScope{}, fmt.Errorf("complete strategy order scope is required")
		}
		var legacy []uuid.UUID
		if order.StrategyID != nil {
			legacy = append(legacy, *order.StrategyID)
		}
		return NewStrategyExecutionScope(order.AccountID, order.Environment, originID, domain.PipelineRunRef{ID: *order.PipelineRunID, TradeDate: *order.PipelineRunTradeDate}, legacy...)
	case ledger.ExecutionOriginCopySubscription:
		originID, err := uuid.Parse(order.OriginID)
		if err != nil || order.CopyOriginRebalanceRunID == uuid.Nil {
			return ExecutionScope{}, fmt.Errorf("complete copy order scope is required")
		}
		return NewCopyExecutionScope(order.AccountID, order.Environment, originID, order.CopyOriginRebalanceRunID)
	default:
		return NewNonRunExecutionScope(order.AccountID, order.Environment, originType, order.OriginID)
	}
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

func (s ExecutionScope) LegacyStrategyID() *uuid.UUID {
	if s.legacyStrategyID == nil {
		return nil
	}
	id := *s.legacyStrategyID
	return &id
}

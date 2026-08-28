package copytrading

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type ExecutionProposalStore interface {
	ProposeExecutionIntent(context.Context, *lifecycle.Aggregate) (*lifecycle.Aggregate, error)
}

type OriginLifecycleExecutor struct{ store ExecutionProposalStore }

func NewOriginLifecycleExecutor(store ExecutionProposalStore) *OriginLifecycleExecutor {
	return &OriginLifecycleExecutor{store: store}
}

type OriginLifecycleResolver struct {
	executor      *OriginLifecycleExecutor
	accounts      repository.AccountRepository
	instruments   repository.InstrumentRepository
	quotes        repository.QuoteSnapshotRepository
	aliasProvider string
}

func NewOriginLifecycleResolver(store ExecutionProposalStore, accounts repository.AccountRepository, instruments repository.InstrumentRepository, quotes repository.QuoteSnapshotRepository, aliasProvider string) *OriginLifecycleResolver {
	return &OriginLifecycleResolver{executor: NewOriginLifecycleExecutor(store), accounts: accounts, instruments: instruments, quotes: quotes, aliasProvider: aliasProvider}
}

func (r *OriginLifecycleResolver) ProposeCopyIntent(ctx context.Context, subscription domain.CopySubscription, intent domain.CopyTradeIntent, runID uuid.UUID) error {
	if r == nil || r.executor == nil || r.accounts == nil || r.instruments == nil || r.quotes == nil || intent.DecisionQuoteSnapshotID == nil || intent.DecisionAt == nil || intent.ExecutablePrice == nil || *intent.ExecutablePrice <= 0 {
		return fmt.Errorf("copy common lifecycle dependencies or decision evidence are incomplete")
	}
	account, err := r.accounts.GetByID(ctx, subscription.AccountID)
	if err != nil {
		return fmt.Errorf("load copy lifecycle account: %w", err)
	}
	reference, err := r.instruments.ResolveAlias(ctx, r.aliasProvider, instrument.AliasTicker, intent.Ticker, *intent.DecisionAt)
	if err != nil {
		return fmt.Errorf("resolve copy lifecycle instrument: %w", err)
	}
	quote, err := r.quotes.GetQuoteSnapshotByID(ctx, *intent.DecisionQuoteSnapshotID)
	if err != nil {
		return fmt.Errorf("load copy lifecycle quote: %w", err)
	}
	quantity := decimal.NewFromFloat(intent.RequestedNotional).Div(decimal.NewFromFloat(*intent.ExecutablePrice))
	if intent.Side == domain.OrderSideSell {
		quantity = quantity.Neg()
	}
	_, err = r.executor.Propose(ctx, OriginProposalInput{Subscription: subscription, Intent: intent, Account: *account, Instrument: *reference, DecisionSnapshot: *quote, QuantityDelta: quantity, DecisionAt: *intent.DecisionAt, CreatedAt: intent.CreatedAt, CopyOriginRebalanceRunID: runID})
	return err
}

func (e *OriginLifecycleExecutor) Propose(ctx context.Context, input OriginProposalInput) (*lifecycle.Aggregate, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("copy origin lifecycle store is unavailable")
	}
	aggregate, err := BuildOriginProposal(input)
	if err != nil {
		return nil, err
	}
	return e.store.ProposeExecutionIntent(ctx, aggregate)
}

// OriginProposalInput is the exact OVR-203 handoff for one approved copy
// intent. OVR-502 supplies the fresh executable decision snapshot; this
// adapter owns attribution and cannot create a strategy-version origin.
type OriginProposalInput struct {
	Subscription             domain.CopySubscription
	Intent                   domain.CopyTradeIntent
	Account                  domain.Account
	Instrument               instrument.Instrument
	DecisionSnapshot         marketdata.QuoteSnapshot
	QuantityDelta            decimal.Decimal
	DecisionAt               time.Time
	CreatedAt                time.Time
	CopyOriginRebalanceRunID uuid.UUID
}

// BuildOriginProposal constructs the immutable common-lifecycle proposal for
// a copy subscription without routing or granting execution authority.
func BuildOriginProposal(input OriginProposalInput) (*lifecycle.Aggregate, error) {
	subscription := input.Subscription
	intent := input.Intent
	if subscription.ID == uuid.Nil ||
		subscription.OriginType != "copy_subscription" || subscription.OriginID != subscription.ID ||
		intent.ID == uuid.Nil || intent.SubscriptionID != subscription.ID ||
		intent.OriginType != subscription.OriginType || intent.OriginID != subscription.OriginID ||
		subscription.AccountID == uuid.Nil || subscription.AccountID != input.Account.ID ||
		intent.AccountID != subscription.AccountID || subscription.Environment != input.Account.Environment ||
		intent.Environment != subscription.Environment {
		return nil, fmt.Errorf("copy origin proposal attribution is invalid")
	}
	if !subscription.IsPaper || subscription.Status != domain.CopySubscriptionPaperActive || intent.PolicyStatus != "approved" {
		return nil, fmt.Errorf("copy origin proposal requires an approved active paper intent")
	}
	if input.QuantityDelta.IsZero() || (intent.Side == domain.OrderSideBuy && input.QuantityDelta.IsNegative()) || (intent.Side == domain.OrderSideSell && input.QuantityDelta.IsPositive()) {
		return nil, fmt.Errorf("copy origin proposal quantity does not match side")
	}
	metadata, err := json.Marshal(map[string]any{
		"calculation_version":   intent.CalculationVersion,
		"copy_intent_id":        intent.ID,
		"source_observation_id": intent.SourceObservationID,
		"subscription_id":       subscription.ID,
	})
	if err != nil {
		return nil, err
	}
	decisionAt := input.DecisionAt.UTC().Truncate(time.Microsecond)
	createdAt := input.CreatedAt.UTC().Truncate(time.Microsecond)
	key := fmt.Sprintf("copy_subscription/%s/%s/%s/%d", subscription.ID, intent.SourceObservationID, intent.InstrumentKey, intent.CalculationVersion)
	return lifecycle.Propose(lifecycle.ProposeInput{
		Account:                  input.Account,
		Instrument:               input.Instrument,
		DecisionSnapshot:         input.DecisionSnapshot,
		IdempotencyKey:           key,
		DesiredQuantityDelta:     input.QuantityDelta,
		DecisionAt:               decisionAt,
		OriginType:               ledger.ExecutionOriginCopySubscription,
		OriginID:                 subscription.ID.String(),
		CopyOriginRebalanceRunID: input.CopyOriginRebalanceRunID,
		StrategyVersionID:        "",
		Metadata:                 metadata,
		Event: lifecycle.EventInput{
			Source:          "augr",
			SourceNamespace: "copy_subscription/" + subscription.ID.String(),
			SourceEventID:   intent.ID.String(),
			SourceAt:        decisionAt,
			ReceivedAt:      createdAt,
			Actor:           "copy-rebalance",
			ReasonCode:      "copy_intent_approved",
			Evidence:        metadata,
		},
		CreatedAt: createdAt,
	})
}

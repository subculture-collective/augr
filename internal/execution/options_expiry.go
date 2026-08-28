package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

type OptionsExpirySummary struct {
	ExpiredWorthless int
	CashSettled      int
}

type OptionExpiryPriceKey struct {
	Underlying string
	ExpiryDate time.Time
}

func NewOptionExpiryPriceKey(underlying string, expiry time.Time) OptionExpiryPriceKey {
	date := expiry.UTC()
	return OptionExpiryPriceKey{Underlying: underlying, ExpiryDate: time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)}
}

type optionSettlement struct {
	scope      ExecutionScope
	positionID uuid.UUID
	intrinsic  float64
	reason     string
}

type OptionSettlementState interface {
	ApplyOptionSettlement(context.Context, uuid.UUID, float64) error
}

type OptionSettlementStateRebuilder interface {
	RebuildOptionSettlementState(context.Context) error
}

type OptionSettlementSyncEvidence interface {
	RecordOptionSettlementSyncFailure(context.Context, uuid.UUID, float64, error) error
}

type optionSettlementSyncFailureRepository interface {
	RecordOptionSettlementSyncFailure(context.Context, repository.OptionPositionSettlementInput, error) error
}

// SettleExpiredOptionPositions cash-settles expired paper options. It does not
// fabricate underlying-share assignment. Every candidate is validated before
// persistence begins so missing prices or contract metadata fail the batch.
func SettleExpiredOptionPositions(ctx context.Context, scope ExecutionScope, positions []domain.Position, underlyingPrices map[OptionExpiryPriceKey]float64, now time.Time, settlementRepo repository.OptionSettlementRepository, states ...OptionSettlementState) (OptionsExpirySummary, error) {
	if settlementRepo == nil {
		return OptionsExpirySummary{}, errors.New("options expiry: atomic settlement repository is required")
	}
	locker, ok := settlementRepo.(repository.ExecutionAccountLocker)
	if !ok {
		return OptionsExpirySummary{}, errors.New("options expiry: execution account locker is required")
	}
	var summary OptionsExpirySummary
	err := locker.WithExecutionAccountLock(ctx, scope.AccountID(), func() error {
		var settleErr error
		summary, settleErr = settleExpiredOptionPositionsLocked(ctx, scope, positions, underlyingPrices, now, settlementRepo, states...)
		return settleErr
	})
	return summary, err
}

func settleExpiredOptionPositionsLocked(ctx context.Context, scope ExecutionScope, positions []domain.Position, underlyingPrices map[OptionExpiryPriceKey]float64, now time.Time, settlementRepo repository.OptionSettlementRepository, states ...OptionSettlementState) (OptionsExpirySummary, error) {
	if retries, ok := settlementRepo.(repository.OptionSettlementSyncRetryRepository); ok {
		pending, err := retries.HasOptionSettlementSyncRetries(ctx, scope.AccountID(), scope.Environment())
		if err != nil {
			return OptionsExpirySummary{}, fmt.Errorf("options expiry: inspect broker sync retries: %w", err)
		}
		if pending {
			if len(states) == 0 || states[0] == nil {
				return OptionsExpirySummary{}, errors.New("options expiry: pending broker sync retry requires settlement state")
			}
			rebuilder, ok := states[0].(OptionSettlementStateRebuilder)
			if !ok {
				return OptionsExpirySummary{}, errors.New("options expiry: pending broker sync retry requires durable state rebuild")
			}
			rebuildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err = rebuilder.RebuildOptionSettlementState(rebuildCtx)
			cancel()
			if err != nil {
				return OptionsExpirySummary{}, fmt.Errorf("options expiry: rebuild broker sync retry state: %w", err)
			}
			resolveCtx, resolveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err = retries.ResolveOptionSettlementSyncRetries(resolveCtx, scope.AccountID(), scope.Environment())
			resolveCancel()
			if err != nil {
				return OptionsExpirySummary{}, fmt.Errorf("options expiry: resolve rebuilt broker sync retries: %w", err)
			}
		}
	}
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	settlements := make([]optionSettlement, 0)
	for index := range positions {
		position := &positions[index]
		if position.AssetClass != domain.AssetClassOption || position.ClosedAt != nil || position.Expiry == nil || position.Quantity <= 0 {
			continue
		}
		if position.AccountID != scope.AccountID() || position.Environment != scope.Environment() {
			return OptionsExpirySummary{}, fmt.Errorf("options expiry: position %s belongs to a foreign account", position.ID)
		}
		expiry := time.Date(position.Expiry.UTC().Year(), position.Expiry.UTC().Month(), position.Expiry.UTC().Day(), 0, 0, 0, 0, time.UTC)
		if expiry.After(today) {
			continue
		}
		if position.OptionType == nil || position.Strike == nil || position.UnderlyingTicker == "" {
			return OptionsExpirySummary{}, fmt.Errorf("options expiry: position %s lacks contract metadata", position.ID)
		}
		underlyingPrice, ok := underlyingPrices[NewOptionExpiryPriceKey(position.UnderlyingTicker, expiry)]
		if !ok || underlyingPrice <= 0 {
			return OptionsExpirySummary{}, fmt.Errorf("options expiry: missing underlying price for %s", position.UnderlyingTicker)
		}
		intrinsic := optionIntrinsicValue(*position.OptionType, *position.Strike, underlyingPrice)
		reason := "expired_worthless"
		if intrinsic > 0 {
			reason = "exercise_cash_settled"
		}
		settlements = append(settlements, optionSettlement{scope: scope, positionID: position.ID, intrinsic: intrinsic, reason: reason})
	}

	summary := OptionsExpirySummary{}
	for _, settlement := range settlements {
		originType, originID := settlement.scope.Origin()
		settlementInput := repository.OptionPositionSettlementInput{
			IdempotencyKey: "option_expiry:v1:" + settlement.scope.AccountID().String() + ":" + settlement.positionID.String(), AccountID: settlement.scope.AccountID(), Environment: settlement.scope.Environment(), OriginType: string(originType), OriginID: originID,
			PositionID: settlement.positionID, SettlementPrice: settlement.intrinsic,
			SettledAt: now.UTC(), ExitReason: settlement.reason,
		}
		if _, err := settlementRepo.SettleOptionPosition(ctx, settlementInput); err != nil {
			return summary, fmt.Errorf("options expiry: settle position %s: %w", settlement.positionID, err)
		}
		if len(states) > 0 && states[0] != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err := states[0].ApplyOptionSettlement(cleanupCtx, settlement.positionID, settlement.intrinsic)
			cancel()
			if err != nil {
				recovery, canRebuild := states[0].(OptionSettlementStateRebuilder)
				var rebuildErr error
				if canRebuild {
					rebuildCtx, rebuildCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					rebuildErr = recovery.RebuildOptionSettlementState(rebuildCtx)
					rebuildCancel()
				} else {
					rebuildErr = errors.New("paper broker state rebuild is unavailable")
				}
				if rebuildErr != nil {
					evidenceCtx, evidenceCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					var evidenceErr error
					if recorder, ok := settlementRepo.(optionSettlementSyncFailureRepository); ok {
						evidenceErr = recorder.RecordOptionSettlementSyncFailure(evidenceCtx, settlementInput, errors.Join(err, rebuildErr))
					} else if recorder, ok := states[0].(OptionSettlementSyncEvidence); ok {
						evidenceErr = recorder.RecordOptionSettlementSyncFailure(evidenceCtx, settlement.positionID, settlement.intrinsic, errors.Join(err, rebuildErr))
					} else {
						evidenceErr = errors.New("durable option broker sync evidence repository is unavailable")
					}
					evidenceCancel()
					if evidenceErr != nil {
						return summary, fmt.Errorf("options expiry: broker sync and retry evidence failed for %s: %w", settlement.positionID, errors.Join(err, rebuildErr, evidenceErr))
					}
					return summary, fmt.Errorf("options expiry: broker sync queued for retry for %s: %w", settlement.positionID, errors.Join(err, rebuildErr))
				}
			}
		}
		if settlement.intrinsic > 0 {
			summary.CashSettled++
		} else {
			summary.ExpiredWorthless++
		}
	}
	return summary, nil
}

func optionIntrinsicValue(optionType domain.OptionType, strike, underlying float64) float64 {
	if optionType == domain.OptionTypeCall && underlying > strike {
		return underlying - strike
	}
	if optionType == domain.OptionTypePut && strike > underlying {
		return strike - underlying
	}
	return 0
}

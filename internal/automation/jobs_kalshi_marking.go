package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/kalshi"
	"github.com/PatrickFanella/get-rich-quick/internal/ledger"
	"github.com/PatrickFanella/get-rich-quick/internal/scheduler"
)

var kalshiMarkingSpec = scheduler.ScheduleSpec{Type: scheduler.ScheduleTypeCron, Cron: "25 * * * *"}

func (o *JobOrchestrator) registerKalshiMarkingJob() {
	if o.deps.KalshiMarkProvider == nil || o.deps.KalshiProjectionRepo == nil || o.deps.KalshiMarkMaxAge <= 0 {
		return
	}
	o.Register("kalshi_marking", "Record conservative canonical Kalshi liquidation marks", kalshiMarkingSpec, o.kalshiMarking)
}

func (o *JobOrchestrator) kalshiMarking(ctx context.Context) error {
	inventoryAsOf := o.now().UTC().Truncate(time.Microsecond)
	maxAge := o.deps.KalshiMarkMaxAge
	lots, err := o.deps.KalshiProjectionRepo.ListCanonicalOpenLots(ctx, inventoryAsOf)
	if err != nil {
		return fmt.Errorf("kalshi_marking: list canonical open lots: %w", err)
	}
	summary := map[string]int{"lots": len(lots), "marked": 0, "unavailable": 0, "accounts_rebuilt": 0}
	defer func() { o.SetLastSummary("kalshi_marking", summary) }()
	accountAsOf := make(map[uuid.UUID]time.Time)
	accountFailed := make(map[uuid.UUID]bool)
	var failures []error
	for _, lot := range lots {
		if lot.Currency != "USD" {
			summary["unavailable"]++
			accountFailed[lot.AccountID] = true
			failures = append(failures, fmt.Errorf("%s: unsupported currency %q", lot.Ticker, lot.Currency))
			continue
		}
		if lot.Side == domain.PositionSideShort {
			summary["unavailable"]++
			accountFailed[lot.AccountID] = true
			failures = append(failures, fmt.Errorf("%s: short canonical lots are unavailable", lot.Ticker))
			continue
		}
		marketTicker, _, ok := strings.Cut(lot.Ticker, ":")
		if !ok {
			summary["unavailable"]++
			accountFailed[lot.AccountID] = true
			failures = append(failures, fmt.Errorf("%s: invalid canonical ticker", lot.Ticker))
			continue
		}
		quote, loadErr := o.deps.KalshiMarkProvider.LoadSnapshot(ctx, marketTicker)
		if loadErr != nil {
			summary["unavailable"]++
			accountFailed[lot.AccountID] = true
			failures = append(failures, fmt.Errorf("%s: load snapshot: %w", lot.Ticker, loadErr))
			continue
		}
		evaluatedAt := o.now().UTC().Truncate(time.Microsecond)
		mark, markErr := kalshi.NewMarkObservation(kalshi.KalshiMarkInput{
			AccountID: lot.AccountID, InstrumentID: lot.InstrumentID, VenueContractID: lot.VenueContractID,
			Side: lot.Side, Ticker: lot.Ticker, Quote: quote, ObservedAt: evaluatedAt, MaxAge: maxAge,
		})
		if markErr != nil {
			summary["unavailable"]++
			accountFailed[lot.AccountID] = true
			failures = append(failures, fmt.Errorf("%s: evaluate mark: %w", lot.Ticker, markErr))
			continue
		}
		if _, err := o.deps.KalshiProjectionRepo.RecordMarkObservation(ctx, mark); err != nil {
			summary["unavailable"]++
			accountFailed[lot.AccountID] = true
			failures = append(failures, fmt.Errorf("%s: record mark: %w", lot.Ticker, err))
			continue
		}
		summary["marked"]++
		if evaluatedAt.After(accountAsOf[lot.AccountID]) {
			accountAsOf[lot.AccountID] = evaluatedAt
		}
	}
	for accountID, asOf := range accountAsOf {
		if accountFailed[accountID] {
			continue
		}
		if _, err := o.deps.KalshiProjectionRepo.RebuildPortfolioProjection(ctx, ledger.ProjectionRequest{
			AccountID: accountID, AsOf: asOf, MarkSource: kalshi.KalshiMarkSource,
			MarkNamespace: kalshi.KalshiAccountMarkNamespace(accountID), MaxMarkAge: maxAge,
		}); err != nil {
			failures = append(failures, fmt.Errorf("rebuild account %s projection: %w", accountID, err))
			continue
		}
		summary["accounts_rebuilt"]++
	}
	if len(failures) > 0 {
		return fmt.Errorf("kalshi_marking incomplete (%d of %d canonical lots unmarked): %w", summary["unavailable"], len(lots), errors.Join(failures...))
	}
	return nil
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/PatrickFanella/get-rich-quick/internal/instrument"
	"github.com/PatrickFanella/get-rich-quick/internal/marketdata"
	pgrepo "github.com/PatrickFanella/get-rich-quick/internal/repository/postgres"
	"github.com/PatrickFanella/get-rich-quick/internal/simulation"
)

type smokePreparationFactory func(context.Context, execution.ExecutionScope, execution.TradingPlan, time.Time) (execution.SignalOrderPreparation, error)

// This factory is installed only by the smoke runtime branch. Its records are
// synthetic fixture evidence, never market-data or production readiness proof.
func newSmokePreparationFactory(environment string, pool *pgxpool.Pool) smokePreparationFactory {
	return func(ctx context.Context, scope execution.ExecutionScope, plan execution.TradingPlan, decisionAt time.Time) (execution.SignalOrderPreparation, error) {
		if !strings.EqualFold(environment, "smoke") || plan.Ticker != "SMOKE" || scope.Environment() != domain.AccountEnvironmentPaperScored ||
			pool == nil || decisionAt.IsZero() || math.IsNaN(plan.EntryPrice) || math.IsInf(plan.EntryPrice, 0) || plan.EntryPrice <= 0 {
			return nil, fmt.Errorf("synthetic preparation requires smoke environment, SMOKE ticker, paper account, and valid decision")
		}
		run, hasRun := scope.PipelineRun()
		if !hasRun {
			return nil, fmt.Errorf("smoke preparation requires composite pipeline run")
		}
		decisionAt = decisionAt.UTC().Truncate(time.Microsecond)
		key := "smoke:" + run.ID.String() + ":" + run.TradeDate.Format("2006-01-02")
		metadata := json.RawMessage(`{"fixture":"smoke-only","synthetic":true}`)
		account, err := pgrepo.NewAccountRepo(pool).GetByID(ctx, scope.AccountID())
		if err != nil {
			return nil, err
		}
		instruments := pgrepo.NewInstrumentRepo(pool)
		reference, err := instrument.NewInstrument(instrument.InstrumentInput{
			IdentityKey: key, AssetClass: instrument.AssetClassEquity, PrimaryVenue: "smoke", Currency: "USD",
			TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.NewFromInt(1), Multiplier: decimal.NewFromInt(1),
			SettlementMethod: instrument.SettlementPhysical, Status: instrument.StatusActive, Metadata: metadata, CreatedAt: decisionAt,
		})
		if err != nil {
			return nil, err
		}
		reference, err = instruments.CreateInstrument(ctx, reference)
		if err != nil {
			return nil, err
		}
		validTo := decisionAt.Add(time.Hour)
		contract, err := instrument.NewVenueContract(instrument.VenueContractInput{
			InstrumentID: reference.ID, Venue: "smoke", ContractID: key, Currency: "USD",
			TickSize: reference.TickSize, LotSize: reference.LotSize, Multiplier: reference.Multiplier, SettlementMethod: reference.SettlementMethod,
			ValidFrom: decisionAt, ValidTo: &validTo, Metadata: metadata, CreatedAt: decisionAt,
		})
		if err != nil {
			return nil, err
		}
		contract, err = instruments.RegisterVenueContract(ctx, contract)
		if err != nil {
			return nil, err
		}
		price := decimal.NewFromFloat(plan.EntryPrice)
		depth := decimal.NewFromInt(10000)
		quote, err := marketdata.NewQuoteSnapshot(marketdata.QuoteSnapshotInput{
			InstrumentID: reference.ID, VenueContractID: &contract.ID, Provider: "smoke-fixture", Venue: "smoke", Source: "synthetic-smoke",
			ObservationNamespace: "smoke-only", ObservationID: key, ExchangeAt: &decisionAt, ReceivedAt: decisionAt, AvailableAt: &decisionAt,
			Bid: &price, Ask: &price, BidSize: &depth, AskSize: &depth, MarketStatus: "open", SessionStatus: "regular", Metadata: metadata, CreatedAt: decisionAt,
			Bids: []marketdata.DepthLevelInput{{Price: price, Size: depth}}, Asks: []marketdata.DepthLevelInput{{Price: price, Size: depth}},
		})
		if err != nil {
			return nil, err
		}
		quote, err = pgrepo.NewQuoteSnapshotRepo(pool).RecordQuoteSnapshot(ctx, quote)
		if err != nil {
			return nil, err
		}
		requirements := marketdata.QuoteRequirements{
			RequireSource: true, RequireVenueContract: true, RequireBid: true, RequireAsk: true,
			RequireBidDepth: true, RequireAskDepth: true, RequireMarketStatus: true, RequireSessionStatus: true,
			AllowedMarketStatuses: []string{"open"}, AllowedSessionStatuses: []string{"regular"}, MaxAge: time.Minute,
		}
		policy, err := simulation.NewPolicy(simulation.PolicyInput{Schema: simulation.PolicySchemaV1, Assets: []simulation.AssetPolicy{{
			AssetClass: instrument.AssetClassEquity, OrderTypes: []lifecycle.OrderType{lifecycle.OrderMarket, lifecycle.OrderLimit},
			TimeInForce: []lifecycle.TimeInForce{lifecycle.TimeInForceDay}, QuoteRequirements: requirements, MaxDepthParticipation: decimal.NewFromInt(1),
			Calendar: simulation.CalendarPolicy{Kind: simulation.CalendarExplicitSessions, Sessions: []simulation.SessionWindow{{Label: "smoke-only", OpenAt: decisionAt, CloseAt: validTo}}},
			Fees:     simulation.FeePolicy{Scale: 4},
		}}})
		if err != nil {
			return nil, err
		}
		artifact, err := policy.NewArtifact(decisionAt)
		if err != nil {
			return nil, err
		}
		if _, err := pgrepo.NewSimulationPolicyRepo(pool).RegisterSimulationPolicy(ctx, artifact); err != nil {
			return nil, err
		}
		proposal := lifecycle.ProposeInput{
			Account: *account, Instrument: *reference, DecisionSnapshot: *quote, IdempotencyKey: key,
			DecisionAt: decisionAt, CreatedAt: decisionAt, Metadata: metadata,
			Event: lifecycle.EventInput{
				Source: "smoke-runner", SourceNamespace: "smoke-only", SourceEventID: key, SourceAt: decisionAt, ReceivedAt: decisionAt,
				Actor: "smoke-runner", ReasonCode: "synthetic_signal", Evidence: metadata,
			},
		}
		route := lifecycle.RouteInput{
			OrderIdempotencyKey: key, Instrument: *reference, VenueContract: *contract, RouteSnapshot: *quote,
			QuoteRequirements: requirements, TimeInForce: lifecycle.TimeInForceDay, PolicyKind: lifecycle.PolicySimulation, PolicyVersion: artifact.Version,
		}
		return pgrepo.NewSignalPreparation(pgrepo.NewExecutionLifecycleRepo(pool), plan.Ticker, proposal, route), nil
	}
}

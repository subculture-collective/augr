package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/PatrickFanella/get-rich-quick/internal/execution/lifecycle"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CanonicalSignalSnapshotType identifies a pre-decision, persisted evidence
// selection. Its producer must retain actual provider facts before recording it.
const CanonicalSignalSnapshotType = "canonical-signal-evidence-v1"

// CanonicalSignalSelection pins evidence explicitly; it never means latest.
type CanonicalSignalSelection struct {
	Ticker                  string                `json:"ticker"`
	AliasProvider           string                `json:"alias_provider"`
	VenueContractID         uuid.UUID             `json:"venue_contract_id"`
	QuoteSnapshotID         uuid.UUID             `json:"quote_snapshot_id"`
	SimulationPolicyVersion string                `json:"simulation_policy_version"`
	TimeInForce             lifecycle.TimeInForce `json:"time_in_force"`
}

// LoadPipelineSignalPreparation derives provenance from a completed durable run
// and one pre-decision selection, then loads its exact retained evidence graph.
// Missing selections fail closed; this function does not manufacture them.
func LoadPipelineSignalPreparation(ctx context.Context, pool *pgxpool.Pool, scope execution.ExecutionScope, plan execution.TradingPlan) (*SignalPreparation, error) {
	ref, ok := scope.PipelineRun()
	if pool == nil || !ok || scope.Environment() != domain.AccountEnvironmentPaperScored || plan.MarketType.Normalize() != domain.MarketTypeStock {
		return nil, fmt.Errorf("pipeline preparation requires a scoped paper stock run")
	}
	run, err := NewPipelineRunRepo(pool, scope.AccountID()).Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	snapshots, err := NewPipelineRunSnapshotRepo(pool, scope.AccountID()).GetByRun(ctx, ref)
	if err != nil {
		return nil, err
	}
	evidence, err := pipelineSignalEvidence(scope, plan, run, snapshots)
	if err != nil {
		return nil, err
	}
	return LoadSignalPreparation(ctx, pool, scope, plan, evidence)
}

func pipelineSignalEvidence(scope execution.ExecutionScope, plan execution.TradingPlan, run *domain.PipelineRun, snapshots []domain.PipelineRunSnapshot) (PersistedSignalEvidence, error) {
	fail := func(reason string) (PersistedSignalEvidence, error) {
		return PersistedSignalEvidence{}, fmt.Errorf("pipeline signal evidence: %s", reason)
	}
	ref, ok := scope.PipelineRun()
	origin, originID := scope.Origin()
	if !ok || run == nil || run.ID != ref.ID || !run.TradeDate.Equal(ref.TradeDate) || run.AccountID != scope.AccountID() || run.Environment != scope.Environment() || run.OriginType != string(origin) || run.OriginID != originID || run.Ticker != plan.Ticker || run.Status != domain.PipelineStatusCompleted || run.CompletedAt == nil || run.CompletedAt.IsZero() || run.Signal != plan.Action || (run.Signal != domain.PipelineSignalBuy && run.Signal != domain.PipelineSignalSell) {
		return fail("completed run does not match execution scope and signal")
	}
	var selected *domain.PipelineRunSnapshot
	for i := range snapshots {
		if snapshots[i].DataType != CanonicalSignalSnapshotType {
			continue
		}
		if selected != nil {
			return fail("ambiguous canonical selections")
		}
		selected = &snapshots[i]
	}
	if selected == nil || selected.ID == uuid.Nil || selected.AccountID != run.AccountID || selected.Environment != run.Environment || selected.OriginType != run.OriginType || selected.OriginID != run.OriginID || selected.PipelineRunID != ref.ID || !selected.PipelineRunTradeDate.Equal(ref.TradeDate) || selected.CreatedAt.IsZero() || selected.CreatedAt.After(*run.CompletedAt) {
		return fail("missing or out-of-scope pre-decision selection")
	}
	var selection CanonicalSignalSelection
	decoder := json.NewDecoder(bytes.NewReader(selected.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selection); err != nil {
		return fail("invalid canonical selection")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fail("trailing canonical selection content")
	}
	if selection.Ticker != plan.Ticker || selection.AliasProvider == "" || selection.VenueContractID == uuid.Nil || selection.QuoteSnapshotID == uuid.Nil || selection.SimulationPolicyVersion == "" || selection.TimeInForce == "" {
		return fail("incomplete canonical selection")
	}
	key := "pipeline-signal:" + ref.ID.String() + ":" + ref.TradeDate.Format("2006-01-02") + ":" + selected.ID.String()
	decisionAt := run.CompletedAt.UTC()
	return PersistedSignalEvidence{
		AliasProvider: selection.AliasProvider, VenueContractID: selection.VenueContractID, QuoteSnapshotID: selection.QuoteSnapshotID,
		SimulationPolicyVersion: selection.SimulationPolicyVersion, TimeInForce: selection.TimeInForce, DecisionAt: decisionAt, OrderIdempotencyKey: key,
		Proposal: lifecycle.ProposeInput{IdempotencyKey: key, CreatedAt: decisionAt, Metadata: append(json.RawMessage(nil), selected.Payload...), Event: lifecycle.EventInput{
			Source: "pipeline-run", SourceNamespace: CanonicalSignalSnapshotType, SourceEventID: key,
			SourceAt: decisionAt, ReceivedAt: decisionAt, Actor: "strategy-runner", ReasonCode: "completed-signal", Evidence: append(json.RawMessage(nil), selected.Payload...),
		}},
	}, nil
}

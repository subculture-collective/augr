package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/execution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RecordPipelineSignalSelection pins the ingestion owner's explicit selection
// while the run is still running. The run lock serializes this with completion;
// database time supplies retention time. This does not import or qualify facts.
func RecordPipelineSignalSelection(ctx context.Context, pool *pgxpool.Pool, scope execution.ExecutionScope, selection CanonicalSignalSelection) (*domain.PipelineRunSnapshot, error) {
	ref, ok := scope.PipelineRun()
	if pool == nil || !ok || scope.Environment() != domain.AccountEnvironmentPaperScored || selection.Schema != CanonicalSignalSelectionSchema || selection.Ticker == "" || selection.AliasProvider == "" || selection.VenueContractID == uuid.Nil || selection.QuoteSnapshotID == uuid.Nil || selection.SimulationPolicyVersion == "" || selection.TimeInForce == "" {
		return nil, fmt.Errorf("record canonical selection requires complete paper-run selectors")
	}
	payload, err := json.Marshal(selection)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Rollback is a no-op after Commit; the operation error remains authoritative.
	defer func() { _ = tx.Rollback(ctx) }()
	var status, ticker, environment, originType, originID string
	if err := tx.QueryRow(ctx, `SELECT status,ticker,environment,origin_type,origin_id FROM pipeline_runs WHERE id=$1 AND trade_date=$2::date AND account_id=$3 FOR UPDATE`, ref.ID, ref.TradeDate, scope.AccountID()).Scan(&status, &ticker, &environment, &originType, &originID); err != nil {
		return nil, err
	}
	wantOrigin, wantOriginID := scope.Origin()
	if status != string(domain.PipelineStatusRunning) || ticker != selection.Ticker || environment != string(scope.Environment()) || originType != string(wantOrigin) || originID != wantOriginID {
		return nil, fmt.Errorf("canonical selection requires matching running authority")
	}
	var existing int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pipeline_run_snapshots WHERE account_id=$1 AND pipeline_run_id=$2 AND pipeline_run_trade_date=$3::date AND data_type='market' AND payload->>'schema'=$4`, scope.AccountID(), ref.ID, ref.TradeDate, CanonicalSignalSelectionSchema).Scan(&existing); err != nil {
		return nil, err
	}
	if existing != 0 {
		return nil, fmt.Errorf("canonical run selection is already pinned")
	}
	// References are checked before pinning. A selected quote must belong to the
	// selected contract, and both must already be retained; no latest lookup.
	var bound bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quote_snapshots q JOIN venue_contracts v ON v.id=q.venue_contract_id AND v.instrument_id=q.instrument_id JOIN simulation_policy_artifacts p ON p.policy_version=$3 WHERE q.id=$1 AND v.id=$2 AND q.created_at<=clock_timestamp() AND v.created_at<=clock_timestamp() AND p.created_at<=clock_timestamp())`, selection.QuoteSnapshotID, selection.VenueContractID, selection.SimulationPolicyVersion).Scan(&bound); err != nil {
		return nil, err
	}
	if !bound {
		return nil, fmt.Errorf("canonical selection facts are missing or mismatched")
	}
	snapshot, err := scanPipelineRunSnapshot(tx.QueryRow(ctx, `INSERT INTO pipeline_run_snapshots (account_id,environment,origin_type,origin_id,pipeline_run_id,pipeline_run_trade_date,data_type,payload,created_at) VALUES ($1,$2,$3,$4,$5,$6,'market',$7,clock_timestamp()) RETURNING id,account_id,environment,origin_type,origin_id,pipeline_run_id,pipeline_run_trade_date,data_type,payload,created_at`, scope.AccountID(), scope.Environment(), originType, originID, ref.ID, ref.TradeDate, payload))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

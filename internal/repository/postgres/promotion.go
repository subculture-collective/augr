package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/economicid"
	"github.com/PatrickFanella/get-rich-quick/internal/promotion"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
	"github.com/PatrickFanella/get-rich-quick/internal/robustness"
	"github.com/PatrickFanella/get-rich-quick/internal/strategycatalog"
)

type PromotionRepo struct {
	pool       *pgxpool.Pool
	afterStage func(string) error
}

type PromotionEvaluationBatch struct {
	Eligible int
	Approved int
	Held     int
}

// EvaluateEligiblePromotions appends the first authoritative decision for
// every proposed paper deployment whose completed assessment is bound to the
// exact configured scope. It never activates a runtime strategy.
func (repo *PromotionRepo) EvaluateEligiblePromotions(ctx context.Context, accountID, scopeID uuid.UUID, readiness promotion.Readiness) (PromotionEvaluationBatch, error) {
	var summary PromotionEvaluationBatch
	if repo == nil || repo.pool == nil || accountID == uuid.Nil || scopeID == uuid.Nil || readiness.AccountID() != accountID || readiness.ScopeID() != scopeID || !readiness.Ready() {
		return summary, fmt.Errorf("postgres: promotion evaluation requires ready exact account scope")
	}
	policy, err := promotion.ReviewedPolicyV1()
	if err != nil {
		return summary, err
	}
	rows, err := repo.pool.Query(ctx, `SELECT deployment.id,assessment.id
		FROM strategy_deployments deployment
		JOIN robustness_assessment_candidates candidate ON candidate.version_id=deployment.version_id
		JOIN statistical_robustness_assessments assessment ON assessment.id=candidate.assessment_id
			AND assessment.scope_id=$2 AND assessment.mode='paper_scored' AND assessment.state='completed'
		JOIN paper_evaluation_scopes scope ON scope.id=assessment.scope_id AND scope.account_id=$1
			AND scope.capital_binding_id=deployment.capital_binding_id
		WHERE deployment.account_id=$1 AND deployment.mode='paper_scored' AND deployment.state='proposed'
		AND NOT EXISTS(SELECT 1 FROM promotion_retirement_decisions decision WHERE decision.deployment_id=deployment.id)
		ORDER BY deployment.id,assessment.id`, accountID, scopeID)
	if err != nil {
		return summary, err
	}
	type candidate struct{ deploymentID, assessmentID uuid.UUID }
	candidates := make([]candidate, 0)
	for rows.Next() {
		var value candidate
		if err = rows.Scan(&value.deploymentID, &value.assessmentID); err != nil {
			rows.Close()
			return summary, err
		}
		candidates = append(candidates, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return summary, err
	}
	seenDeployments := make(map[uuid.UUID]uuid.UUID, len(candidates))
	for _, candidate := range candidates {
		if prior, exists := seenDeployments[candidate.deploymentID]; exists && prior != candidate.assessmentID {
			return summary, fmt.Errorf("postgres: deployment %s has conflicting completed assessments in configured scope", candidate.deploymentID)
		}
		seenDeployments[candidate.deploymentID] = candidate.assessmentID
	}
	service, err := promotion.NewService(repo)
	if err != nil {
		return summary, err
	}
	for _, candidate := range candidates {
		decision, evaluateErr := service.Evaluate(ctx, promotion.Request{DeploymentID: candidate.deploymentID, AssessmentID: candidate.assessmentID, Policy: policy, Readiness: readiness})
		if evaluateErr != nil {
			return summary, evaluateErr
		}
		summary.Eligible++
		if decision.Outcome() == promotion.OutcomeApproved {
			summary.Approved++
		} else {
			summary.Held++
		}
	}
	return summary, nil
}

func NewPromotionRepo(pool *pgxpool.Pool) *PromotionRepo { return &PromotionRepo{pool: pool} }

var _ promotion.Store = (*PromotionRepo)(nil)

func (repo *PromotionRepo) GetDeployment(ctx context.Context, id uuid.UUID) (*strategycatalog.Deployment, error) {
	if repo == nil || repo.pool == nil {
		return nil, fmt.Errorf("postgres: promotion repository is required")
	}
	return NewStrategyCatalogRepo(repo.pool).GetStrategyDeployment(ctx, id)
}

func (repo *PromotionRepo) GetAssessment(ctx context.Context, id uuid.UUID) (*robustness.Assessment, error) {
	if repo == nil || repo.pool == nil {
		return nil, fmt.Errorf("postgres: promotion repository is required")
	}
	return NewRobustnessRepo(repo.pool).GetAssessment(ctx, id)
}

func (repo *PromotionRepo) RegisterPolicy(ctx context.Context, value *promotion.Policy) (*promotion.Policy, error) {
	if repo == nil || repo.pool == nil || value == nil {
		return nil, fmt.Errorf("postgres: promotion policy is required")
	}
	tx, err := repo.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO promotion_policy_artifacts(id,schema_name,version,pass_action,failure_action,required_gate_count,sha256,canonical_bytes,canonical_json,created_at)
		VALUES($1,'promotion-policy-v1',$2,'shadow',$3,$4,$5,$6,convert_from($6,'UTF8')::jsonb,$7) ON CONFLICT(id) DO NOTHING`,
		value.ID(), value.Version(), value.FailureAction(), len(value.RequiredGates()), value.Digest(), value.CanonicalBytes(), databaseNow())
	if err != nil {
		return nil, evaluationWriteError("insert promotion policy", err)
	}
	if err = repo.stage("promotion_policy"); err != nil {
		return nil, err
	}
	for sequence, name := range value.RequiredGates() {
		if _, err = tx.Exec(ctx, `INSERT INTO promotion_policy_required_gates(policy_id,sequence,name) VALUES($1,$2,$3) ON CONFLICT(policy_id,sequence) DO NOTHING`, value.ID(), sequence, name); err != nil {
			return nil, evaluationWriteError("insert promotion required gate", err)
		}
		if err = repo.stage("promotion_required_gate"); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, evaluationWriteError("commit promotion policy", err)
	}
	got, err := repo.GetPolicy(ctx, value.ID())
	if err != nil {
		return nil, err
	}
	if got.Digest() != value.Digest() || !bytes.Equal(got.CanonicalBytes(), value.CanonicalBytes()) {
		return nil, fmt.Errorf("postgres: promotion policy conflict: %w", repository.ErrIdempotencyConflict)
	}
	return got, nil
}

func (repo *PromotionRepo) GetPolicy(ctx context.Context, id uuid.UUID) (*promotion.Policy, error) {
	var digest string
	var raw []byte
	err := repo.pool.QueryRow(ctx, `SELECT sha256,canonical_bytes FROM promotion_policy_artifacts WHERE id=$1`, id).Scan(&digest, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return promotion.PolicyFromCanonical(id, digest, raw)
}

func (repo *PromotionRepo) RecordDecision(ctx context.Context, value *promotion.Decision) (*promotion.Decision, error) {
	if repo == nil || repo.pool == nil || value == nil {
		return nil, fmt.Errorf("postgres: promotion decision is required")
	}
	candidateSequence, err := repo.candidateSequence(ctx, value.AssessmentID(), value.VersionID())
	if err != nil {
		return nil, err
	}
	tx, err := repo.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, value.DeploymentID().String()); err != nil {
		return nil, err
	}
	created := databaseNow()
	var prior any
	if value.PriorDecisionID() != uuid.Nil {
		prior = value.PriorDecisionID()
	}
	_, err = tx.Exec(ctx, `INSERT INTO promotion_retirement_decisions(id,schema_name,deployment_id,deployment_sha256,version_id,assessment_id,assessment_sha256,
		family_id,robustness_policy_id,mode,policy_id,policy_sha256,prior_decision_id,prior_decision_sha256,candidate_sequence,prior_state,next_state,outcome,reason,
		observed_gate_count,sha256,canonical_bytes,canonical_json,created_at) VALUES($1,'promotion-retirement-decision-v1',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,convert_from($21,'UTF8')::jsonb,$22) ON CONFLICT(id) DO NOTHING`,
		value.ID(), value.DeploymentID(), value.DeploymentDigest(), value.VersionID(), value.AssessmentID(), value.AssessmentDigest(), value.FamilyID(),
		value.RobustnessPolicyID(), value.Mode(), value.PolicyID(), value.PolicyDigest(), prior, value.PriorDecisionDigest(), candidateSequence,
		value.PriorState(), value.NextState(), value.Outcome(), value.Reason(), len(value.ObservedGates()), value.Digest(), value.CanonicalBytes(), created)
	if err != nil {
		return nil, evaluationWriteError("insert promotion decision", err)
	}
	if err = repo.stage("promotion_decision"); err != nil {
		return nil, err
	}
	for sequence, gate := range value.ObservedGates() {
		_, err = tx.Exec(ctx, `INSERT INTO promotion_decision_observed_gates(decision_id,sequence,assessment_id,candidate_sequence,name,state,threshold,observed,reason,description)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(decision_id,sequence) DO NOTHING`, value.ID(), sequence, value.AssessmentID(), candidateSequence,
			gate.Name, gate.State, gate.Threshold, gate.Observed, gate.Reason, gate.Description)
		if err != nil {
			return nil, evaluationWriteError("insert promotion observed gate", err)
		}
		if err = repo.stage("promotion_observed_gate"); err != nil {
			return nil, err
		}
	}
	eventID, eventDigest, eventBytes := promotionEvent(value)
	_, err = tx.Exec(ctx, `INSERT INTO deployment_promotion_lifecycle_events(id,schema_name,deployment_id,decision_id,prior_state,next_state,outcome,sha256,canonical_bytes,canonical_json,created_at)
		VALUES($1,'deployment-promotion-lifecycle-event-v1',$2,$3,$4,$5,$6,$7,$8,convert_from($8,'UTF8')::jsonb,$9) ON CONFLICT(id) DO NOTHING`,
		eventID, value.DeploymentID(), value.ID(), value.PriorState(), value.NextState(), value.Outcome(), eventDigest, eventBytes, created)
	if err != nil {
		return nil, evaluationWriteError("insert deployment promotion lifecycle event", err)
	}
	if err = repo.stage("promotion_lifecycle_event"); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, evaluationWriteError("commit promotion decision", err)
	}
	got, err := repo.GetDecision(ctx, value.ID())
	if err != nil {
		return nil, err
	}
	if got.Digest() != value.Digest() || !bytes.Equal(got.CanonicalBytes(), value.CanonicalBytes()) {
		return nil, fmt.Errorf("postgres: promotion decision conflict: %w", repository.ErrIdempotencyConflict)
	}
	return got, nil
}

func (repo *PromotionRepo) GetDecision(ctx context.Context, id uuid.UUID) (*promotion.Decision, error) {
	var digest string
	var raw []byte
	var deploymentID, assessmentID, policyID uuid.UUID
	var priorID *uuid.UUID
	err := repo.pool.QueryRow(ctx, `SELECT sha256,canonical_bytes,deployment_id,assessment_id,policy_id,prior_decision_id FROM promotion_retirement_decisions WHERE id=$1`, id).
		Scan(&digest, &raw, &deploymentID, &assessmentID, &policyID, &priorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	deployment, err := NewStrategyCatalogRepo(repo.pool).GetStrategyDeployment(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	assessment, err := NewRobustnessRepo(repo.pool).GetAssessment(ctx, assessmentID)
	if err != nil {
		return nil, err
	}
	policy, err := repo.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	var prior *promotion.Decision
	if priorID != nil {
		prior, err = repo.GetDecision(ctx, *priorID)
		if err != nil {
			return nil, err
		}
	}
	value, err := promotion.DecisionFromCanonical(id, digest, raw, deployment, assessment, policy, prior)
	if err != nil {
		return nil, err
	}
	gates, err := repo.loadObservedGates(ctx, id)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(gates, value.ObservedGates()) {
		return nil, fmt.Errorf("postgres: normalized promotion decision %s does not reconstruct", id)
	}
	if err := repo.verifyEvent(ctx, value); err != nil {
		return nil, err
	}
	return value, nil
}

func (repo *PromotionRepo) candidateSequence(ctx context.Context, assessmentID, versionID uuid.UUID) (int, error) {
	var sequence int
	if err := repo.pool.QueryRow(ctx, `SELECT sequence FROM robustness_assessment_candidates WHERE assessment_id=$1 AND version_id=$2`, assessmentID, versionID).Scan(&sequence); err != nil {
		return 0, err
	}
	return sequence, nil
}

func (repo *PromotionRepo) loadObservedGates(ctx context.Context, id uuid.UUID) ([]promotion.ObservedGate, error) {
	rows, err := repo.pool.Query(ctx, `SELECT name,state,threshold,observed,reason,description FROM promotion_decision_observed_gates WHERE decision_id=$1 ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]promotion.ObservedGate, 0)
	for rows.Next() {
		var value promotion.ObservedGate
		if err := rows.Scan(&value.Name, &value.State, &value.Threshold, &value.Observed, &value.Reason, &value.Description); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (repo *PromotionRepo) verifyEvent(ctx context.Context, value *promotion.Decision) error {
	wantID, wantDigest, wantBytes := promotionEvent(value)
	var id uuid.UUID
	var digest string
	var raw []byte
	err := repo.pool.QueryRow(ctx, `SELECT id,sha256,canonical_bytes FROM deployment_promotion_lifecycle_events WHERE decision_id=$1`, value.ID()).Scan(&id, &digest, &raw)
	if err != nil {
		return fmt.Errorf("postgres: load promotion lifecycle event: %w", err)
	}
	if id != wantID || digest != wantDigest || !bytes.Equal(raw, wantBytes) {
		return fmt.Errorf("postgres: normalized promotion lifecycle event does not reconstruct")
	}
	return nil
}

func (repo *PromotionRepo) ListDeploymentDecisions(ctx context.Context, deploymentID uuid.UUID, limit, offset int) ([]*promotion.Decision, error) {
	return repo.listDecisions(ctx, `SELECT id FROM promotion_retirement_decisions WHERE deployment_id=$1 ORDER BY created_at,id LIMIT $2 OFFSET $3`, deploymentID, limit, offset)
}

func (repo *PromotionRepo) ListVersionDecisions(ctx context.Context, versionID uuid.UUID, limit, offset int) ([]*promotion.Decision, error) {
	return repo.listDecisions(ctx, `SELECT id FROM promotion_retirement_decisions WHERE version_id=$1 ORDER BY created_at,id LIMIT $2 OFFSET $3`, versionID, limit, offset)
}

func (repo *PromotionRepo) ListAssessmentDecisions(ctx context.Context, assessmentID uuid.UUID, limit, offset int) ([]*promotion.Decision, error) {
	return repo.listDecisions(ctx, `SELECT id FROM promotion_retirement_decisions WHERE assessment_id=$1 ORDER BY created_at,id LIMIT $2 OFFSET $3`, assessmentID, limit, offset)
}

func (repo *PromotionRepo) ListFamilyDecisions(ctx context.Context, familyID uuid.UUID, limit, offset int) ([]*promotion.Decision, error) {
	return repo.listDecisions(ctx, `SELECT id FROM promotion_retirement_decisions WHERE family_id=$1 ORDER BY created_at,id LIMIT $2 OFFSET $3`, familyID, limit, offset)
}

func (repo *PromotionRepo) listDecisions(ctx context.Context, query string, parent uuid.UUID, limit, offset int) ([]*promotion.Decision, error) {
	if repo == nil || repo.pool == nil || parent == uuid.Nil || limit <= 0 || limit > 1000 || offset < 0 {
		return nil, fmt.Errorf("postgres: list promotion decisions: valid parent and pagination are required")
	}
	rows, err := repo.pool.Query(ctx, query, parent, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]*promotion.Decision, 0, len(ids))
	for _, id := range ids {
		value, err := repo.GetDecision(ctx, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// ProjectDeploymentState derives the serialized chain head. No writable
// current-state pointer exists.
func (repo *PromotionRepo) ProjectDeploymentState(ctx context.Context, deploymentID uuid.UUID) (string, uuid.UUID, error) {
	if repo == nil || repo.pool == nil || deploymentID == uuid.Nil {
		return "", uuid.Nil, fmt.Errorf("postgres: deployment identity is required")
	}
	var state string
	var head *uuid.UUID
	err := repo.pool.QueryRow(ctx, `SELECT COALESCE(head.next_state,deployment.state),head.id FROM strategy_deployments deployment
		LEFT JOIN LATERAL (SELECT decision.id,decision.next_state FROM promotion_retirement_decisions decision WHERE decision.deployment_id=deployment.id AND
			NOT EXISTS(SELECT 1 FROM promotion_retirement_decisions child WHERE child.prior_decision_id=decision.id) LIMIT 1) head ON true WHERE deployment.id=$1`, deploymentID).Scan(&state, &head)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", uuid.Nil, repository.ErrNotFound
	}
	if err != nil {
		return "", uuid.Nil, err
	}
	if head == nil {
		return state, uuid.Nil, nil
	}
	return state, *head, nil
}

func (repo *PromotionRepo) stage(name string) error {
	if repo.afterStage == nil {
		return nil
	}
	return repo.afterStage(name)
}

type promotionEventCanonical struct {
	Schema       string `json:"schema"`
	DeploymentID string `json:"deployment_id"`
	DecisionID   string `json:"decision_id"`
	PriorState   string `json:"prior_state"`
	NextState    string `json:"next_state"`
	Outcome      string `json:"outcome"`
}

func promotionEvent(value *promotion.Decision) (uuid.UUID, string, []byte) {
	canonical := promotionEventCanonical{Schema: "deployment-promotion-lifecycle-event-v1", DeploymentID: value.DeploymentID().String(), DecisionID: value.ID().String(), PriorState: value.PriorState(), NextState: value.NextState(), Outcome: value.Outcome()}
	raw, _ := json.Marshal(canonical)
	digestValue := sha256.Sum256(raw)
	digest := hex.EncodeToString(digestValue[:])
	return economicid.DeterministicUUID("deployment-promotion-lifecycle-event", canonical.Schema+"@sha256:"+digest), digest, raw
}

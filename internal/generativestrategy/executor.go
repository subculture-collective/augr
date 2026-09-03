package generativestrategy

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PatrickFanella/get-rich-quick/internal/experimentrun"
)

type ExperimentRunner interface {
	Run(context.Context, experimentrun.RunRequest) (*experimentrun.Result, error)
}

type Executor struct{ runner ExperimentRunner }

func NewExecutor(runner ExperimentRunner) (*Executor, error) {
	if runner == nil {
		return nil, fmt.Errorf("generated strategy experiment runner is required")
	}
	return &Executor{runner: runner}, nil
}

type ExecutionRequest struct {
	Prepared   *PreparedResearch
	AttemptID  uuid.UUID
	StartedAt  time.Time
	FinishedAt time.Time
}

// Execute runs only the exact immutable experiment and Program returned by
// ResearchPreparer. It neither selects a latest artifact nor creates any
// evaluation, promotion, deployment, schedule, allocation, or broker effect.
func (executor *Executor) Execute(ctx context.Context, request ExecutionRequest) (*experimentrun.Result, error) {
	if executor == nil || executor.runner == nil || request.Prepared == nil || request.Prepared.Spec == nil ||
		request.Prepared.Version == nil || request.Prepared.Receipt == nil || request.Prepared.Scenario == nil ||
		request.Prepared.Experiment == nil || request.Prepared.Program == nil || request.AttemptID == uuid.Nil ||
		!canonicalExecutionTime(request.StartedAt) || !canonicalExecutionTime(request.FinishedAt) || request.FinishedAt.Before(request.StartedAt) {
		return nil, fmt.Errorf("generated strategy execution requires exact prepared research, attempt, and UTC microsecond bounds")
	}
	prepared := request.Prepared
	identity := prepared.Program.Identity()
	if prepared.Receipt.SpecID() != prepared.Spec.ID() || prepared.Receipt.VersionID() != prepared.Version.ID() ||
		prepared.Scenario.SpecID() != prepared.Spec.ID() || prepared.Experiment.VersionID() != prepared.Version.ID() ||
		prepared.Experiment.ManifestID() != prepared.Scenario.ManifestID() || identity.VersionID() != prepared.Version.ID() ||
		identity.VersionSHA256() != prepared.Version.Digest() {
		return nil, fmt.Errorf("generated strategy prepared research graph does not reconstruct")
	}
	result, err := executor.runner.Run(ctx, experimentrun.RunRequest{
		ExperimentID: prepared.Experiment.ID(), AttemptID: request.AttemptID,
		StartedAt: request.StartedAt, FinishedAt: request.FinishedAt, Program: prepared.Program,
	})
	if err != nil {
		return nil, fmt.Errorf("execute generated strategy experiment: %w", err)
	}
	if result == nil || result.ExperimentID() != prepared.Experiment.ID() || result.ProgramID() != identity.ID() ||
		result.AccountID() != prepared.Experiment.AccountID() || result.ManifestID() != prepared.Experiment.ManifestID() ||
		result.QualityResultID() != prepared.Experiment.QualityResultID() || result.SimulationPolicyVersion() != prepared.Experiment.SimulationPolicyVersion() ||
		result.CapitalPolicyVersion() != prepared.Experiment.CapitalPolicyVersion() || result.Mode() != prepared.Experiment.Mode() {
		return nil, fmt.Errorf("generated strategy experiment runner returned mismatched result evidence")
	}
	return result, nil
}

func canonicalExecutionTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Equal(value.Truncate(time.Microsecond))
}

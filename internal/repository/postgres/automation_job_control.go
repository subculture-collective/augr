package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PatrickFanella/get-rich-quick/internal/domain"
	"github.com/PatrickFanella/get-rich-quick/internal/repository"
)

// AutoDisableActor is the updated_by value written when the orchestrator
// disables a job itself after consecutive failures.
const AutoDisableActor = "auto-disable"

// AutoRearmActor is the updated_by value written when an auto-disabled job's
// cooldown expires and the orchestrator re-enables it.
const AutoRearmActor = "auto-rearm"

// AutomationJobControlDetail is an AutomationJobControl plus the auto-disable
// columns added in migration 000115.
type AutomationJobControlDetail struct {
	domain.AutomationJobControl
	Reason            string     `json:"reason,omitempty"`
	AutoDisabledUntil *time.Time `json:"auto_disabled_until,omitempty"`
}

// AutomationJobControlRepo persists explicit operator enable/disable choices.
type AutomationJobControlRepo struct {
	pool *pgxpool.Pool
}

var _ repository.AutomationJobControlRepository = (*AutomationJobControlRepo)(nil)

func NewAutomationJobControlRepo(pool *pgxpool.Pool) *AutomationJobControlRepo {
	return &AutomationJobControlRepo{pool: pool}
}

func (r *AutomationJobControlRepo) List(ctx context.Context) ([]domain.AutomationJobControl, error) {
	details, err := r.ListDetailed(ctx)
	if err != nil {
		return nil, err
	}
	controls := make([]domain.AutomationJobControl, 0, len(details))
	for _, detail := range details {
		controls = append(controls, detail.AutomationJobControl)
	}
	return controls, nil
}

// ListDetailed returns every control row including auto-disable reason and
// cooldown expiry.
func (r *AutomationJobControlRepo) ListDetailed(ctx context.Context) ([]AutomationJobControlDetail, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT job_name, enabled, updated_by, updated_at, COALESCE(reason, ''), auto_disabled_until
		 FROM automation_job_controls ORDER BY job_name`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list automation job controls: %w", err)
	}
	defer rows.Close()

	var controls []AutomationJobControlDetail
	for rows.Next() {
		var control AutomationJobControlDetail
		if err := rows.Scan(&control.JobName, &control.Enabled, &control.UpdatedBy, &control.UpdatedAt, &control.Reason, &control.AutoDisabledUntil); err != nil {
			return nil, fmt.Errorf("postgres: scan automation job control: %w", err)
		}
		controls = append(controls, control)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate automation job controls: %w", err)
	}
	return controls, nil
}

// SetEnabled records an explicit enable/disable choice. Any auto-disable
// reason and cooldown are cleared because an explicit write supersedes them.
func (r *AutomationJobControlRepo) SetEnabled(ctx context.Context, name string, enabled bool, actor string) error {
	name = strings.TrimSpace(name)
	actor = strings.TrimSpace(actor)
	if name == "" {
		return fmt.Errorf("postgres: set automation job control: job name is required")
	}
	if actor == "" {
		actor = "unknown"
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO automation_job_controls (job_name, enabled, updated_by, updated_at, reason, auto_disabled_until)
		 VALUES ($1, $2, $3, now(), NULL, NULL)
		 ON CONFLICT (job_name) DO UPDATE
		 SET enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = now(),
		     reason = NULL, auto_disabled_until = NULL`,
		name, enabled, actor,
	)
	if err != nil {
		return fmt.Errorf("postgres: set automation job control: %w", err)
	}
	return nil
}

// SetAutoDisabled durably records an orchestrator-initiated disable with its
// reason and the time after which the job may re-arm.
func (r *AutomationJobControlRepo) SetAutoDisabled(ctx context.Context, name, reason string, until time.Time) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("postgres: set automation job auto-disable: job name is required")
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO automation_job_controls (job_name, enabled, updated_by, updated_at, reason, auto_disabled_until)
		 VALUES ($1, false, $2, now(), $3, $4)
		 ON CONFLICT (job_name) DO UPDATE
		 SET enabled = false, updated_by = EXCLUDED.updated_by, updated_at = now(),
		     reason = EXCLUDED.reason, auto_disabled_until = EXCLUDED.auto_disabled_until`,
		name, AutoDisableActor, nullString(reason), until,
	)
	if err != nil {
		return fmt.Errorf("postgres: set automation job auto-disable: %w", err)
	}
	return nil
}

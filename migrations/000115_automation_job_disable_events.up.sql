-- Auto-disable provenance for automation jobs. reason records why the
-- orchestrator (updated_by = 'auto-disable') or an operator disabled the job;
-- auto_disabled_until is the cooldown expiry after which the orchestrator may
-- re-arm an auto-disabled job. Both are NULL for explicit operator writes.
ALTER TABLE automation_job_controls
    ADD COLUMN IF NOT EXISTS reason TEXT,
    ADD COLUMN IF NOT EXISTS auto_disabled_until TIMESTAMPTZ;

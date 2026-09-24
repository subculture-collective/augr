ALTER TABLE automation_job_controls
    DROP COLUMN IF EXISTS auto_disabled_until,
    DROP COLUMN IF EXISTS reason;

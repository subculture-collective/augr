ALTER TABLE app_settings
    ADD COLUMN IF NOT EXISTS prompt_overrides JSONB NOT NULL DEFAULT '{}';

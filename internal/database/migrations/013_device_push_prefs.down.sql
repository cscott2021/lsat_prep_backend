ALTER TABLE device_tokens
    DROP COLUMN IF EXISTS push_streak_enabled,
    DROP COLUMN IF EXISTS push_reengage_enabled;

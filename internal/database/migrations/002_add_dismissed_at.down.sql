DROP INDEX IF EXISTS idx_uqh_dismissed;
ALTER TABLE user_question_history DROP COLUMN IF EXISTS dismissed_at;

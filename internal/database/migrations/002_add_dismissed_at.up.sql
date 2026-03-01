-- Add dismissed_at column to user_question_history
ALTER TABLE user_question_history
  ADD COLUMN dismissed_at TIMESTAMP DEFAULT NULL;

-- Index for efficient filtering of dismissed/non-dismissed questions
CREATE INDEX idx_uqh_dismissed
  ON user_question_history (user_id, correct, dismissed_at)
  WHERE correct = false;

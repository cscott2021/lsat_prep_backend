-- Password-reset tokens. We store only a SHA-256 hash of the token; the raw token
-- goes in the emailed reset link, so a DB leak can't be used to reset passwords.
CREATE TABLE password_reset_tokens (
    token_hash TEXT        PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used       BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_password_reset_user ON password_reset_tokens (user_id);

-- Adds an admin flag to users. Admin-only endpoints (/admin/*) are gated on
-- this column via middleware.AdminMiddleware. Defaults to FALSE so no existing
-- or newly registered user is an admin unless explicitly promoted, e.g.:
--   UPDATE users SET is_admin = TRUE WHERE email = '<admin-email>';
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;

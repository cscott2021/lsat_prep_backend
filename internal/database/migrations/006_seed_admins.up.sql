-- Seed the initial admin accounts. These are the only users flagged is_admin,
-- so they are the only ones that can reach the /admin/* routes (gated by
-- middleware.AdminMiddleware, which reads is_admin from this table).
--
-- Passwords are bcrypt hashes (cost 10); the plaintext was generated out-of-band
-- and is intended to be ROTATED after first login.
--
-- username is NOT NULL in production (schema drift from the 001 baseline, which
-- had it nullable — an earlier NULL here failed the deploy). We use underscore
-- usernames, which the app's username generator can never produce (it emits
-- ^[a-z0-9]+[0-9]{4}$), so these cannot collide with a registered user's
-- username. They are also distinct from each other.
--
-- ON CONFLICT (email): if either account already exists it is *promoted* to admin
-- and its password reset to the seeded hash, rather than erroring. It does not
-- touch username on conflict. This is idempotent, so re-running is a no-op.
INSERT INTO users (email, name, username, password, is_admin, created_at, updated_at)
VALUES
    ('caleb@scoreright.app', 'Caleb', 'caleb_scoreright',
     '$2a$10$bJ/YgHyDY4D3wRsVKL/FQO6SuDXCUIbXWWlswWWBsm5.V9w1GUMpO', TRUE, NOW(), NOW()),
    ('hank@scoreright.app', 'Hank', 'hank_scoreright',
     '$2a$10$7Iw9zkKiUj8dWHxQHPmXAufvHIktvrU6EgKqD3nc/aVIKIl/D5AU2', TRUE, NOW(), NOW())
ON CONFLICT (email) DO UPDATE
    SET is_admin   = TRUE,
        password   = EXCLUDED.password,
        updated_at = NOW();

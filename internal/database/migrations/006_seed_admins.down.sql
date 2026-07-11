-- Revoke admin from the seeded accounts. We deliberately do NOT delete the rows:
-- if an account pre-existed and was promoted by the up migration, deleting it
-- would destroy a real user's data. Demoting is the safe inverse.
UPDATE users SET is_admin = FALSE, updated_at = NOW()
WHERE email IN ('caleb@scoreright.app', 'hank@scoreright.app');

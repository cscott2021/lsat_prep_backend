-- Per-device push opt-out, mirroring the two toggles the app already shows in
-- Notification Settings.
--
-- Before this migration the in-app toggles only cancelled LOCAL notifications:
-- ListPushCandidates selected every row in device_tokens with no preference
-- filter, so a user who switched reminders off kept receiving server pushes.
-- That is both a trust problem and an App Review 4.5.4 consent problem.
--
--   * push_streak_enabled   gates the streak-reminder and daily-digest threads
--                           (ThreadStreak, ThreadGoalMet) — the app's
--                           "Practice reminders" toggle.
--   * push_reengage_enabled gates the re-engagement thread (ThreadReengage) —
--                           the app's "Re-engagement" toggle.
--
-- Both default TRUE: the columns are backfilled for devices registered before
-- this shipped, and TRUE matches both the app's own default and the fact that
-- the user granted OS notification permission. The app overwrites them with
-- the real preference on its next registration (every app start).
ALTER TABLE device_tokens
    ADD COLUMN push_streak_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN push_reengage_enabled BOOLEAN NOT NULL DEFAULT TRUE;

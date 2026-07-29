-- Server push (APNs) support: APNs device tokens reported by the app, one row
-- per installed device. Distinct from the social `nudges` feature and the
-- email_outbox pipeline — this table only exists so the daily engagement
-- worker can reach a CLOSED app via Apple Push Notification service.
--
--   * token is unique per device: re-registration (token refresh, reinstall,
--     or the same device signing into another account) re-keys the row to the
--     latest user — an APNs token can only deliver to one app install, so the
--     newest association always wins.
--   * timezone is the device-reported IANA zone (e.g. "America/Chicago");
--     the daily worker sends in the USER's local evening and enforces
--     local quiet hours with it.
--   * last_notified_at enforces the hard cap of 1 push per device per day.
--   * Rows disappear with the user (ON DELETE CASCADE, account deletion) and
--     tokens are pruned when APNs reports 410 Unregistered.
CREATE TABLE device_tokens (
    id               BIGSERIAL   PRIMARY KEY,
    user_id          BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform         TEXT        NOT NULL DEFAULT 'ios'
                                 CHECK (platform IN ('ios', 'android')),
    token            TEXT        NOT NULL UNIQUE,
    timezone         TEXT        NOT NULL DEFAULT 'UTC',
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_notified_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_device_tokens_user ON device_tokens (user_id);

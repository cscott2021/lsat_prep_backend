-- Apple App Store IAP support, layered onto the SAME entitlement model Stripe
-- uses (migration 007): a subscription row with provider='apple' entitles the
-- user everywhere because GetEntitlement only reads `status`.
--
-- 1. apple_original_transaction_id links a subscriptions row to the Apple
--    subscription's STABLE id (original_transaction_id never changes across
--    renewals; per-period transaction_id does). App Store Server Notifications
--    resolve the local user through this column.
-- 2. apple_events is the idempotency ledger for notificationUUID, mirroring
--    how billing_events dedupes Stripe webhook deliveries.

ALTER TABLE subscriptions
    ADD COLUMN apple_original_transaction_id TEXT;

CREATE UNIQUE INDEX idx_subscriptions_apple_original_tx
    ON subscriptions (apple_original_transaction_id)
    WHERE apple_original_transaction_id IS NOT NULL;

CREATE TABLE apple_events (
    notification_uuid TEXT        PRIMARY KEY,
    type              TEXT        NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_apple_events_received_at ON apple_events (received_at);

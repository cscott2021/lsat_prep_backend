DROP TABLE IF EXISTS apple_events;

DROP INDEX IF EXISTS idx_subscriptions_apple_original_tx;

ALTER TABLE subscriptions
    DROP COLUMN IF EXISTS apple_original_transaction_id;

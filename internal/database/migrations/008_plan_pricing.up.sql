-- Admin-managed subscription pricing + change tracking + email outbox.
--
-- Prices move from static env (STRIPE_PRICE_*) to this DB table so admins can
-- edit them in-app. Stripe Prices are immutable, so an "edit" creates a NEW
-- Stripe Price and repoints the tier here; existing subscribers are grandfathered
-- on their old price until explicitly migrated. Every change is audited in
-- price_changes, and price-increase notices to existing subscribers are queued in
-- email_outbox.

-- Current active price for each plan tier (source of truth for /billing/config).
CREATE TABLE plan_prices (
    tier              TEXT PRIMARY KEY
                          CHECK (tier IN ('monthly', 'quarterly', 'annual')),
    stripe_price_id   TEXT        NOT NULL,
    stripe_product_id TEXT        NOT NULL,
    amount            BIGINT      NOT NULL,           -- minor units (cents)
    currency          TEXT        NOT NULL DEFAULT 'usd',
    interval          TEXT        NOT NULL,           -- 'month' | 'year'
    interval_count    INT         NOT NULL DEFAULT 1, -- 3 => quarterly
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Full audit trail of every price change (who, when, old -> new, why).
CREATE TABLE price_changes (
    id                   BIGSERIAL   PRIMARY KEY,
    tier                 TEXT        NOT NULL,
    old_amount           BIGINT,
    new_amount           BIGINT      NOT NULL,
    old_price_id         TEXT,
    new_price_id         TEXT        NOT NULL,
    changed_by           BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    reason               TEXT        NOT NULL DEFAULT '',
    notified_existing    BOOLEAN     NOT NULL DEFAULT FALSE,
    affected_subscribers INT         NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_price_changes_created ON price_changes (created_at DESC);

-- Transactional email queue. Rows are sent by a background worker when an email
-- provider (SES) is configured; until then they queue and are visible to admins
-- as an auditable record of what was/would be sent.
CREATE TABLE email_outbox (
    id         BIGSERIAL   PRIMARY KEY,
    to_email   TEXT        NOT NULL,
    to_user_id BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    subject    TEXT        NOT NULL,
    body       TEXT        NOT NULL,
    category   TEXT        NOT NULL DEFAULT 'transactional',
    status     TEXT        NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued', 'sent', 'failed', 'skipped')),
    error      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at    TIMESTAMPTZ
);
CREATE INDEX idx_email_outbox_status ON email_outbox (status, created_at);

-- Billing: per-user subscription state and webhook idempotency.
--
-- Entitlement is DERIVED, never stored as a boolean: a user is entitled when
-- their subscription status is one of ('trialing','active','comp') OR they are
-- an admin (is_admin on users — admins are auto-comped). See
-- internal/billing/store.go GetEntitlement.
--
-- provider is kept generic so Apple App Store IAP ('apple') and manual comps
-- ('comp') can populate the same table later without touching entitlement or
-- paywall logic; only Stripe is wired today.
CREATE TABLE subscriptions (
    -- One subscription row per user (FK + unique via PRIMARY KEY).
    user_id                BIGINT      PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    provider               TEXT        NOT NULL DEFAULT 'stripe'
                               CHECK (provider IN ('stripe', 'apple', 'comp')),
    stripe_customer_id     TEXT,
    stripe_subscription_id TEXT,
    status                 TEXT        NOT NULL DEFAULT 'none'
                               CHECK (status IN ('trialing', 'active', 'past_due',
                                                 'canceled', 'incomplete', 'comp', 'none')),
    plan                   TEXT        NOT NULL DEFAULT '',
    price_id               TEXT        NOT NULL DEFAULT '',
    current_period_end     TIMESTAMPTZ,
    cancel_at_period_end   BOOLEAN     NOT NULL DEFAULT FALSE,
    trial_end              TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Webhook handlers look up local rows by Stripe customer/subscription id. Unique
-- (partial, so multiple NULLs are allowed) to keep the mapping 1:1.
CREATE UNIQUE INDEX idx_subscriptions_stripe_customer
    ON subscriptions (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;
CREATE UNIQUE INDEX idx_subscriptions_stripe_subscription
    ON subscriptions (stripe_subscription_id) WHERE stripe_subscription_id IS NOT NULL;
CREATE INDEX idx_subscriptions_status ON subscriptions (status);

-- Idempotency ledger for Stripe webhooks. Stripe may deliver the same event more
-- than once; we record each processed event id and skip duplicates so state is
-- never applied twice.
CREATE TABLE billing_events (
    stripe_event_id TEXT        PRIMARY KEY,
    type            TEXT        NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_billing_events_received_at ON billing_events (received_at);

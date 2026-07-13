-- Admin-issued "N free days" promo codes, modeled as bounded trials (NOT Stripe
-- discount coupons, which can only waive whole billing periods). At subscribe a
-- redeemed code sets trial_period_days=N on the Stripe subscription and a card is
-- collected up front (SetupIntent) so it converts when the trial ends.
CREATE TABLE trial_offers (
    code            TEXT        PRIMARY KEY,
    free_days       INT         NOT NULL CHECK (free_days > 0 AND free_days <= 365),
    name            TEXT        NOT NULL DEFAULT '',
    max_redemptions INT,                 -- NULL = unlimited
    redeemed_count  INT         NOT NULL DEFAULT 0,
    expires_at      TIMESTAMPTZ,
    active          BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

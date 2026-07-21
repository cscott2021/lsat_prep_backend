package billing

import (
	"os"
	"strconv"
	"time"
)

// defaultTrialDays is used when TRIAL_DAYS is unset or unparseable.
const defaultTrialDays = 7

// Config holds all billing configuration, sourced entirely from the environment
// (SSM-backed in ECS). It is loaded once at startup by LoadConfig.
//
// Billing is treated as OPTIONAL infrastructure: if SecretKey is empty the whole
// feature is DISABLED (Enabled == false). The server still starts, billing
// endpoints return 503, and the paywall fails open (nobody is locked out on an
// environment that has no way to charge). This keeps nonprod usable without any
// Stripe keys.
type Config struct {
	SecretKey      string
	WebhookSecret  string
	PublishableKey string
	PriceMonthly   string
	PriceQuarterly string
	PriceAnnual    string
	AppBaseURL     string
	TrialDays      int
	// FoundingLaunchDate anchors the founding-member promo window. When nil the
	// feature is DISABLED (no founding_offer is ever surfaced and Subscribe never
	// auto-applies the founding code). Sourced from FOUNDING_LAUNCH_DATE.
	FoundingLaunchDate *time.Time
}

// LoadConfig reads billing configuration from the environment. It never fails:
// missing/invalid values degrade gracefully (see Enabled).
func LoadConfig() Config {
	trialDays := defaultTrialDays
	if v := os.Getenv("TRIAL_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			trialDays = n
		}
	}

	return Config{
		SecretKey:          os.Getenv("STRIPE_SECRET_KEY"),
		WebhookSecret:      os.Getenv("STRIPE_WEBHOOK_SECRET"),
		PublishableKey:     os.Getenv("STRIPE_PUBLISHABLE_KEY"),
		PriceMonthly:       os.Getenv("STRIPE_PRICE_MONTHLY"),
		PriceQuarterly:     os.Getenv("STRIPE_PRICE_QUARTERLY"),
		PriceAnnual:        os.Getenv("STRIPE_PRICE_ANNUAL"),
		AppBaseURL:         os.Getenv("APP_BASE_URL"),
		TrialDays:          trialDays,
		FoundingLaunchDate: parseFoundingLaunchDate(os.Getenv("FOUNDING_LAUNCH_DATE")),
	}
}

// parseFoundingLaunchDate parses FOUNDING_LAUNCH_DATE, accepting either a full
// RFC3339 timestamp ("2026-07-01T00:00:00Z") or a bare date ("2026-07-01",
// interpreted as midnight UTC). Returns nil for an empty or unparseable value so
// a misconfiguration disables the promo (LoadConfig never fails).
func parseFoundingLaunchDate(v string) *time.Time {
	if v == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

// Enabled reports whether Stripe billing is fully configured. Both the secret
// key AND the webhook signing secret are required: taking a card charge without
// a working webhook would leave every payer stuck 'incomplete' (never
// reconciled), so we treat a secret-key-without-webhook state as DISABLED (503 +
// paywall fails open) rather than half-live. When false every Stripe-backed
// endpoint returns 503 and the paywall is a no-op.
func (c Config) Enabled() bool {
	return c.SecretKey != "" && c.WebhookSecret != ""
}

// WebhookConfigured reports whether inbound webhooks can be verified. The webhook
// endpoint refuses to process events without a signing secret.
func (c Config) WebhookConfigured() bool {
	return c.SecretKey != "" && c.WebhookSecret != ""
}

package billing

import (
	"testing"

	stripe "github.com/stripe/stripe-go/v81"

	"github.com/lsat-prep/backend/internal/models"
)

func TestLoadConfigDefaults(t *testing.T) {
	// With nothing set, billing is disabled and trial defaults to 7.
	t.Setenv("STRIPE_SECRET_KEY", "")
	t.Setenv("TRIAL_DAYS", "")
	cfg := LoadConfig()
	if cfg.Enabled() {
		t.Errorf("expected billing disabled with empty secret key")
	}
	if cfg.TrialDays != defaultTrialDays {
		t.Errorf("TrialDays = %d, want %d", cfg.TrialDays, defaultTrialDays)
	}
}

// TestSubscribePriceAllowlist proves the money-safety guarantee that Subscribe
// only accepts our configured plan prices — a client cannot pass an arbitrary,
// foreign, or archived Stripe price id and still get entitlement.
func TestSubscribePriceAllowlist(t *testing.T) {
	svc := &Service{cfg: Config{
		PriceMonthly:   "price_m",
		PriceQuarterly: "price_q",
		PriceAnnual:    "price_a",
	}}
	for _, id := range []string{"price_m", "price_q", "price_a"} {
		if !svc.isConfiguredPrice(id) {
			t.Errorf("configured price %q should be allowed", id)
		}
	}
	for _, id := range []string{"price_bogus", "price_cheap_001", "", "PRICE_M", "price_"} {
		if svc.isConfiguredPrice(id) {
			t.Errorf("non-configured price %q must be rejected", id)
		}
	}
	// An unset configured slot must never match an empty candidate id.
	unset := &Service{cfg: Config{PriceMonthly: "", PriceQuarterly: "", PriceAnnual: ""}}
	if unset.isConfiguredPrice("") {
		t.Errorf("empty price must not match unset configured prices")
	}
}

func TestConfigEnabledAndWebhook(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_x")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("TRIAL_DAYS", "14")
	cfg := LoadConfig()
	// Enabled now requires BOTH the secret key and the webhook secret: charging
	// cards without a working webhook would leave every payer unreconciled, so a
	// secret-key-only config is deliberately treated as DISABLED (fail-open).
	if cfg.Enabled() {
		t.Errorf("expected billing DISABLED with only the secret key set (no webhook secret)")
	}
	if cfg.WebhookConfigured() {
		t.Errorf("expected webhook not configured without webhook secret")
	}
	if cfg.TrialDays != 14 {
		t.Errorf("TrialDays = %d, want 14", cfg.TrialDays)
	}

	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_x")
	cfg = LoadConfig()
	if !cfg.Enabled() {
		t.Errorf("expected billing enabled with both secrets set")
	}
	if !cfg.WebhookConfigured() {
		t.Errorf("expected webhook configured with both secrets set")
	}
}

func TestTrialDaysInvalidFallsBack(t *testing.T) {
	t.Setenv("TRIAL_DAYS", "not-a-number")
	if got := LoadConfig().TrialDays; got != defaultTrialDays {
		t.Errorf("TrialDays = %d, want default %d", got, defaultTrialDays)
	}
	t.Setenv("TRIAL_DAYS", "0")
	if got := LoadConfig().TrialDays; got != 0 {
		t.Errorf("TrialDays = %d, want 0 (explicitly no trial)", got)
	}
}

func TestMapStripeStatus(t *testing.T) {
	cases := map[stripe.SubscriptionStatus]string{
		stripe.SubscriptionStatusTrialing:          models.SubStatusTrialing,
		stripe.SubscriptionStatusActive:            models.SubStatusActive,
		stripe.SubscriptionStatusPastDue:           models.SubStatusPastDue,
		stripe.SubscriptionStatusUnpaid:            models.SubStatusPastDue,
		stripe.SubscriptionStatusCanceled:          models.SubStatusCanceled,
		stripe.SubscriptionStatusPaused:            models.SubStatusCanceled,
		stripe.SubscriptionStatusIncomplete:        models.SubStatusIncomplete,
		stripe.SubscriptionStatusIncompleteExpired: models.SubStatusIncomplete,
	}
	for in, want := range cases {
		if got := mapStripeStatus(in); got != want {
			t.Errorf("mapStripeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEntitlementDerivation checks the derived-entitlement rule the paywall and
// the /billing/subscription response both rely on.
func TestEntitlementDerivation(t *testing.T) {
	entitledStatuses := []string{models.SubStatusTrialing, models.SubStatusActive, models.SubStatusComp}
	for _, s := range entitledStatuses {
		if !models.EntitledStatuses[s] {
			t.Errorf("status %q should be entitled", s)
		}
	}
	deniedStatuses := []string{
		models.SubStatusPastDue, models.SubStatusCanceled,
		models.SubStatusIncomplete, models.SubStatusNone,
	}
	for _, s := range deniedStatuses {
		if models.EntitledStatuses[s] {
			t.Errorf("status %q should NOT be entitled", s)
		}
	}
}

func TestFreeDaysFromCoupon(t *testing.T) {
	if got := freeDaysFromCoupon(nil); got != 0 {
		t.Errorf("nil coupon free days = %d, want 0", got)
	}
	percent := &stripe.Coupon{PercentOff: 25}
	if got := freeDaysFromCoupon(percent); got != 0 {
		t.Errorf("percent coupon free days = %d, want 0", got)
	}
	free := &stripe.Coupon{Metadata: map[string]string{"free_days": "30"}}
	if got := freeDaysFromCoupon(free); got != 30 {
		t.Errorf("free_days coupon = %d, want 30", got)
	}
}

func TestPlanForPrice(t *testing.T) {
	p := &stripeProvider{cfg: Config{PriceMonthly: "price_m", PriceAnnual: "price_a"}}
	if got := p.planForPrice("price_m"); got != "monthly" {
		t.Errorf("planForPrice(monthly) = %q", got)
	}
	if got := p.planForPrice("price_a"); got != "annual" {
		t.Errorf("planForPrice(annual) = %q", got)
	}
	if got := p.planForPrice("price_unknown"); got != "price_unknown" {
		t.Errorf("planForPrice(unknown) = %q, want passthrough", got)
	}
}

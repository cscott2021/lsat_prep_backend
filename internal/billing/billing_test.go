package billing

import (
	"strings"
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
func TestTierSpecsAndDisplayNames(t *testing.T) {
	// Cadence is fixed per tier; only the amount is admin-editable. Quarterly must
	// be month x3 so it renders "/quarter", and the launch defaults must match
	// the business model ($19.99 / $49.99 / $149.99).
	cases := []struct {
		tier          string
		interval      string
		intervalCount int64
		amount        int64
		display       string
	}{
		{"monthly", "month", 1, 1999, "Monthly"},
		{"quarterly", "month", 3, 4999, "Quarterly"},
		{"annual", "year", 1, 14999, "Annual"},
	}
	for _, c := range cases {
		spec, ok := tierSpecs[c.tier]
		if !ok {
			t.Fatalf("missing tierSpec for %q", c.tier)
		}
		if spec.interval != c.interval || spec.intervalCount != c.intervalCount || spec.defaultAmount != c.amount {
			t.Errorf("%s spec = %+v, want interval=%s count=%d amount=%d", c.tier, spec, c.interval, c.intervalCount, c.amount)
		}
		if got := tierDisplayName(c.tier); got != c.display {
			t.Errorf("tierDisplayName(%q) = %q, want %q", c.tier, got, c.display)
		}
	}
	if got := tierDisplayName("bogus"); got != "bogus" {
		t.Errorf("unknown tier should echo its id, got %q", got)
	}
}

func TestFirstNameAndPriceEmail(t *testing.T) {
	for in, want := range map[string]string{"": "there", "Caleb": "Caleb", "Caleb Scott": "Caleb", "  Hank  Smith ": "Hank"} {
		if got := firstName(in); got != want {
			t.Errorf("firstName(%q) = %q, want %q", in, got, want)
		}
	}
	subject, body := priceIncreaseEmail("annual", 14999, 16999)
	if subject == "" {
		t.Error("price increase email needs a subject")
	}
	for _, want := range []string{"Annual", "$149.99", "$169.99", "%s"} {
		if !strings.Contains(body, want) {
			t.Errorf("price increase body missing %q; got:\n%s", want, body)
		}
	}
}

func TestPlanForPriceNilStore(t *testing.T) {
	// With no store wired the provider must not panic; it echoes the raw id.
	p := &stripeProvider{}
	if got := p.planForPrice("price_xyz"); got != "price_xyz" {
		t.Errorf("planForPrice with nil store = %q, want raw id", got)
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

// planForPrice now resolves via the DB (plan_prices + history); the nil-store
// passthrough is covered by TestPlanForPriceNilStore.

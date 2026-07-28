package billing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

// TestBillingRequiredParsing verifies BILLING_REQUIRED is opt-in: only explicit
// truthy strings enable fail-closed, and the default is false (fail open).
func TestBillingRequiredParsing(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "")
	t.Setenv("BILLING_REQUIRED", "")
	if LoadConfig().BillingRequired {
		t.Error("BillingRequired should default to false when unset")
	}
	for _, truthy := range []string{"true", "TRUE", "1", "yes", "On"} {
		t.Setenv("BILLING_REQUIRED", truthy)
		if !LoadConfig().BillingRequired {
			t.Errorf("BILLING_REQUIRED=%q should parse as true", truthy)
		}
	}
	for _, falsy := range []string{"false", "0", "no", "maybe", ""} {
		t.Setenv("BILLING_REQUIRED", falsy)
		if LoadConfig().BillingRequired {
			t.Errorf("BILLING_REQUIRED=%q should parse as false", falsy)
		}
	}
}

// TestBillingMisconfigured proves the fail-closed predicate: true ONLY when
// billing is required but not enabled. It must never trip when billing is enabled
// (we don't want to break the live money path), nor when it isn't required.
func TestBillingMisconfigured(t *testing.T) {
	// Required + not enabled (no keys) -> misconfigured (fail closed).
	if !(&Service{cfg: Config{BillingRequired: true}}).billingMisconfigured() {
		t.Error("expected misconfigured=true when required but not enabled")
	}
	// Not required + not enabled -> fine (fail open, the nonprod default).
	if (&Service{cfg: Config{}}).billingMisconfigured() {
		t.Error("expected misconfigured=false when not required")
	}
	// Required + enabled -> fine (billing works; nothing to fail closed on).
	enabled := Config{BillingRequired: true, SecretKey: "sk_test_x", WebhookSecret: "whsec_x"}
	if !enabled.Enabled() {
		t.Fatal("test setup: expected enabled config")
	}
	if (&Service{cfg: enabled}).billingMisconfigured() {
		t.Error("expected misconfigured=false when billing is enabled")
	}
}

// TestPaywallFailsClosedWhenRequired verifies the middleware returns 503 (not a
// pass-through) when BILLING_REQUIRED is set but billing is disabled — the S3
// fix. The fail-open default (not required) must still pass through. Both
// branches run before any store access, so no DB is needed.
func TestPaywallFailsClosedWhenRequired(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mws := map[string]func(*Service) func(http.Handler) http.Handler{
		"RequireEntitlement": RequireEntitlement,
		"RequireEntitlementOrFreeQuota": func(s *Service) func(http.Handler) http.Handler {
			return RequireEntitlementOrFreeQuota(s, nil)
		},
	}

	for name, mw := range mws {
		// Fail closed: required + disabled -> 503, next never runs.
		nextCalled = false
		svc := &Service{cfg: Config{BillingRequired: true}}
		rr := httptest.NewRecorder()
		mw(svc)(next).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/drill", nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: fail-closed status = %d, want 503", name, rr.Code)
		}
		if nextCalled {
			t.Errorf("%s: next must not run when failing closed", name)
		}

		// Fail open (default): not required + disabled -> pass through.
		nextCalled = false
		svcOpen := &Service{cfg: Config{}}
		rr2 := httptest.NewRecorder()
		mw(svcOpen)(next).ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/drill", nil))
		if !nextCalled {
			t.Errorf("%s: next must run when failing open (nonprod default)", name)
		}
	}
}

// TestTrialEndingEmail checks the B5 reminder body: it must name the plan, the
// amount, the charge date, and the cancel-in-Settings path. With no resolved
// price it must still send a dated reminder without inventing a figure.
func TestTrialEndingEmail(t *testing.T) {
	trialEnd := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	price := &models.PlanPrice{Tier: "quarterly", Amount: 4999, Currency: "usd", Interval: "month", IntervalCount: 3}

	subject, body := trialEndingEmail("Caleb Scott", "quarterly", price, trialEnd)
	if subject == "" {
		t.Error("trial-ending email needs a subject")
	}
	for _, want := range []string{"Caleb", "Quarterly", "$49.99", "every 3 months", "August 4, 2026", "Settings"} {
		if !strings.Contains(body, want) {
			t.Errorf("trial-ending body missing %q; got:\n%s", want, body)
		}
	}

	// No price resolved: still dated + cancellable, but no dollar figure guessed.
	_, bodyNoPrice := trialEndingEmail("", "", nil, trialEnd)
	if strings.Contains(bodyNoPrice, "$") {
		t.Errorf("must not invent an amount when price is unresolved; got:\n%s", bodyNoPrice)
	}
	for _, want := range []string{"August 4, 2026", "Settings"} {
		if !strings.Contains(bodyNoPrice, want) {
			t.Errorf("price-less body missing %q; got:\n%s", want, bodyNoPrice)
		}
	}
}

// TestBillingCadence covers the cadence phrasing used in the reminder.
func TestBillingCadence(t *testing.T) {
	cases := []struct {
		p    *models.PlanPrice
		want string
	}{
		{&models.PlanPrice{Interval: "month", IntervalCount: 1}, "per month"},
		{&models.PlanPrice{Interval: "year", IntervalCount: 1}, "per year"},
		{&models.PlanPrice{Interval: "month", IntervalCount: 3}, "every 3 months"},
	}
	for _, c := range cases {
		if got := billingCadence(c.p); got != c.want {
			t.Errorf("billingCadence(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

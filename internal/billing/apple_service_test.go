package billing

import (
	"testing"
	"time"

	"github.com/lsat-prep/backend/internal/billing/apple"
	"github.com/lsat-prep/backend/internal/models"
)

// These tests cover the pure mapping logic of Apple IAP — transaction ->
// status and notification -> state effect — without any database or network,
// mirroring the style of billing_test.go.

func TestAppleProductTier(t *testing.T) {
	cases := map[string]string{
		"app.scoreright.monthly.ios":   "monthly",
		"app.scoreright.quarterly.ios": "quarterly",
		"app.scoreright.annual.ios":    "annual",
	}
	for productID, want := range cases {
		got, ok := AppleProductTier(productID)
		if !ok || got != want {
			t.Errorf("AppleProductTier(%q) = %q, %v; want %q, true", productID, got, ok, want)
		}
	}
	if _, ok := AppleProductTier("com.scoreright.lifetime.ios"); ok {
		t.Error("unknown product id must not map to a tier")
	}
}

func TestStatusForTransaction(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(30 * 24 * time.Hour).UnixMilli()
	past := now.Add(-24 * time.Hour).UnixMilli()

	cases := []struct {
		name       string
		tx         *apple.Transaction
		wantStatus string
		wantTrial  bool
	}{
		{
			name:       "unexpired regular purchase is active",
			tx:         &apple.Transaction{ExpiresDate: future},
			wantStatus: models.SubStatusActive,
		},
		{
			name:       "unexpired intro offer is trialing",
			tx:         &apple.Transaction{ExpiresDate: future, OfferType: apple.OfferTypeIntroductory},
			wantStatus: models.SubStatusTrialing,
			wantTrial:  true,
		},
		{
			name:       "expired transaction is canceled (no entitlement)",
			tx:         &apple.Transaction{ExpiresDate: past},
			wantStatus: models.SubStatusCanceled,
		},
		{
			name:       "missing expiry is treated as not entitled",
			tx:         &apple.Transaction{ExpiresDate: 0},
			wantStatus: models.SubStatusCanceled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, trialEnd, periodEnd := statusForTransaction(tc.tx, now)
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
			if (trialEnd != nil) != tc.wantTrial {
				t.Errorf("trialEnd set = %v, want %v", trialEnd != nil, tc.wantTrial)
			}
			if tc.wantStatus != models.SubStatusCanceled && periodEnd == nil {
				t.Error("periodEnd must be set for an entitled status")
			}
		})
	}

	// Sanity: entitled statuses produced here must be in the shared entitlement
	// set the paywall middleware uses — otherwise an iOS buyer would pay and
	// stay locked out.
	for _, status := range []string{models.SubStatusActive, models.SubStatusTrialing} {
		if !models.EntitledStatuses[status] {
			t.Errorf("%q is not in models.EntitledStatuses — Apple purchases would not unlock", status)
		}
	}
}

func TestEffectForNotification(t *testing.T) {
	cases := []struct {
		name       string
		notifType  string
		subtype    string
		renewal    *apple.RenewalInfo
		wantApply  bool
		wantStatus string
		wantCancel *bool
		wantUpsert bool
	}{
		{"SUBSCRIBED rewrites the row", apple.NotifSubscribed, "", nil, true, "", nil, true},
		{"DID_RENEW rewrites the row", apple.NotifDidRenew, "", nil, true, "", nil, true},
		{
			"auto-renew off sets cancel flag", apple.NotifDidChangeRenewalStatus,
			apple.SubtypeAutoRenewDisabled, nil, true, "", boolPtrForTest(true), false,
		},
		{
			"auto-renew on clears cancel flag", apple.NotifDidChangeRenewalStatus,
			apple.SubtypeAutoRenewEnabled, nil, true, "", boolPtrForTest(false), false,
		},
		{
			"renewal info fallback: status 0 means canceled", apple.NotifDidChangeRenewalStatus,
			"", &apple.RenewalInfo{AutoRenewStatus: 0}, true, "", boolPtrForTest(true), false,
		},
		{
			"renewal info fallback: status 1 means renewing", apple.NotifDidChangeRenewalStatus,
			"", &apple.RenewalInfo{AutoRenewStatus: 1}, true, "", boolPtrForTest(false), false,
		},
		{
			"billing retry outside grace is past_due", apple.NotifDidFailToRenew,
			"", nil, true, models.SubStatusPastDue, nil, false,
		},
		{
			"billing retry inside grace keeps access", apple.NotifDidFailToRenew,
			apple.SubtypeGracePeriod, nil, false, "", nil, false,
		},
		{"grace period expiry is past_due", apple.NotifGracePeriodExpired, "", nil, true, models.SubStatusPastDue, nil, false},
		{"EXPIRED cancels", apple.NotifExpired, "", nil, true, models.SubStatusCanceled, boolPtrForTest(false), false},
		{"REFUND cancels", apple.NotifRefund, "", nil, true, models.SubStatusCanceled, boolPtrForTest(false), false},
		{"REVOKE cancels", apple.NotifRevoke, "", nil, true, models.SubStatusCanceled, boolPtrForTest(false), false},
		{"unhandled type is a no-op", "CONSUMPTION_REQUEST", "", nil, false, "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := effectForNotification(tc.notifType, tc.subtype, tc.renewal)
			if got.apply != tc.wantApply {
				t.Fatalf("apply = %v, want %v", got.apply, tc.wantApply)
			}
			if got.status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.status, tc.wantStatus)
			}
			if got.fullUpsert != tc.wantUpsert {
				t.Errorf("fullUpsert = %v, want %v", got.fullUpsert, tc.wantUpsert)
			}
			if tc.wantCancel == nil {
				if got.cancelAtPeriodEnd != nil {
					t.Errorf("cancelAtPeriodEnd = %v, want nil", *got.cancelAtPeriodEnd)
				}
			} else {
				if got.cancelAtPeriodEnd == nil || *got.cancelAtPeriodEnd != *tc.wantCancel {
					t.Errorf("cancelAtPeriodEnd = %v, want %v", got.cancelAtPeriodEnd, *tc.wantCancel)
				}
			}
		})
	}
}

func boolPtrForTest(b bool) *bool { return &b }

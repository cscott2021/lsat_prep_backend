package models

import "time"

// ── Subscription status / provider constants ──────────────

// Subscription statuses mirror the CHECK constraint in migration 007. They are a
// superset of Stripe's statuses plus 'comp' (manual/admin comp) and 'none' (no
// subscription yet).
const (
	SubStatusTrialing   = "trialing"
	SubStatusActive     = "active"
	SubStatusPastDue    = "past_due"
	SubStatusCanceled   = "canceled"
	SubStatusIncomplete = "incomplete"
	SubStatusComp       = "comp"
	SubStatusNone       = "none"
)

const (
	ProviderStripe = "stripe"
	ProviderApple  = "apple"
	ProviderComp   = "comp"
)

// EntitledStatuses is the set of subscription statuses that grant access. A user
// is also entitled if they are an admin (auto-comped). Kept in one place so the
// paywall middleware and the /billing/subscription response agree.
var EntitledStatuses = map[string]bool{
	SubStatusTrialing: true,
	SubStatusActive:   true,
	SubStatusComp:     true,
}

// Subscription is the local mirror of a user's billing state. The source of
// truth for Stripe fields is Stripe; webhooks keep this row in sync. Entitlement
// is derived from Status (+ the user's is_admin), never stored.
type Subscription struct {
	UserID               int64      `json:"user_id"`
	Provider             string     `json:"provider"`
	StripeCustomerID     string     `json:"-"`
	StripeSubscriptionID string     `json:"-"`
	Status               string     `json:"status"`
	Plan                 string     `json:"plan"`
	PriceID              string     `json:"price_id"`
	CurrentPeriodEnd     *time.Time `json:"current_period_end"`
	CancelAtPeriodEnd    bool       `json:"cancel_at_period_end"`
	TrialEnd             *time.Time `json:"trial_end"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// ── Billing DTOs (request/response contracts) ─────────────

// BillingPlan describes one purchasable plan in the /billing/config response.
type BillingPlan struct {
	ID       string `json:"id"`       // stable client id, e.g. "monthly"
	Name     string `json:"name"`     // display name
	PriceID  string `json:"price_id"` // Stripe price id
	Interval string `json:"interval"` // "month" | "year"
	// IntervalCount is the number of intervals per billing cycle (e.g. 3 for a
	// quarterly plan billed every 3 months). Lets the client render the correct
	// cadence ("/quarter") instead of assuming "/mo".
	IntervalCount int64  `json:"interval_count"`
	Amount        int64  `json:"amount"`   // smallest currency unit (e.g. cents)
	Currency      string `json:"currency"` // ISO currency, lowercased (Stripe convention)
}

// BillingConfigResponse is returned by GET /billing/config so the client can
// initialize the Stripe SDK and render the plan picker.
type BillingConfigResponse struct {
	PublishableKey string        `json:"publishable_key"`
	TrialDays      int           `json:"trial_days"`
	Plans          []BillingPlan `json:"plans"`
}

// SubscriptionResponse is returned by GET /billing/subscription. entitled is the
// derived access flag the client should gate premium UI on.
type SubscriptionResponse struct {
	Status            string     `json:"status"`
	Plan              string     `json:"plan"`
	Provider          string     `json:"provider"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	TrialEnd          *time.Time `json:"trial_end"`
	Entitled          bool       `json:"entitled"`
	// FreeQuota reports the metered free-tier usage for a NON-entitled user. It is
	// null for entitled users (they have no quota) and when the free tier is not
	// enforced (billing disabled → paywall open).
	FreeQuota *FreeQuotaStatus `json:"free_quota"`
	// FoundingOffer is the launch-window promo the user is eligible for, or null.
	FoundingOffer *FoundingOffer `json:"founding_offer"`
}

// FreeQuotaStatus reports a non-entitled user's metered free-tier usage. Used is
// the number of questions answered in the rolling 24h window; ResetAt is when
// the OLDEST answer in that window ages out (null when nothing has been used).
type FreeQuotaStatus struct {
	Used    int        `json:"used"`
	Limit   int        `json:"limit"`
	ResetAt *time.Time `json:"reset_at"`
}

// FoundingOffer describes the founding-member launch promo surfaced on GET
// /billing/subscription. It is only ever emitted when the user is eligible (the
// account was created inside the launch window), so Eligible is always true when
// the object is present; the field is null otherwise.
type FoundingOffer struct {
	Eligible   bool      `json:"eligible"`
	PercentOff int       `json:"percent_off"`
	Months     int       `json:"months"`
	EndsAt     time.Time `json:"ends_at"`
}

type SubscribeRequest struct {
	PriceID       string `json:"price_id"`
	PromotionCode string `json:"promotion_code,omitempty"`
}

// SubscribeResponse carries the PaymentIntent client secret the client feeds to
// the Stripe Payment Sheet to collect card details. The raw card number never
// touches this server.
type SubscribeResponse struct {
	ClientSecret   string `json:"client_secret"`
	SubscriptionID string `json:"subscription_id"`
	// Mode tells the client which kind of Stripe intent client_secret is for:
	// "payment" (charge now) or "setup" (collect a card for a free-days trial
	// that converts when the trial ends). Empty when no client action is needed.
	Mode string `json:"mode"`
}

// TrialOffer is an admin-issued "N free days" promo code — a bounded trial, not
// a Stripe discount coupon.
type TrialOffer struct {
	Code           string     `json:"code"`
	FreeDays       int        `json:"free_days"`
	Name           string     `json:"name"`
	MaxRedemptions *int       `json:"max_redemptions,omitempty"`
	RedeemedCount  int        `json:"redeemed_count"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Active         bool       `json:"active"`
	CreatedAt      time.Time  `json:"created_at"`
}

type ApplyCouponRequest struct {
	Code string `json:"code"`
}

// ApplyCouponResponse describes a promotion code the client entered so it can
// preview the discount before subscribing.
type ApplyCouponResponse struct {
	Valid       bool    `json:"valid"`
	Description string  `json:"description"`
	PercentOff  float64 `json:"percent_off,omitempty"`
	FreeDays    int     `json:"free_days,omitempty"`
}

// UpdatePaymentResponse / PortalResponse are thin wrappers around the SetupIntent
// client secret and Billing Portal URL respectively.
type UpdatePaymentResponse struct {
	ClientSecret string `json:"client_secret"`
}

type PortalResponse struct {
	URL string `json:"url"`
}

// ── Admin billing DTOs ────────────────────────────────────

type CreateCouponRequest struct {
	Type           string     `json:"type"`  // "percent" | "free_days"
	Value          int64      `json:"value"` // percent (1-100) or number of free days
	Name           string     `json:"name"`
	MaxRedemptions *int64     `json:"max_redemptions,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

type CreateCouponResponse struct {
	Code     string `json:"code"`
	CouponID string `json:"coupon_id"`
}

// CouponOffer is one row in GET /admin/coupons, joining the promotion code with
// its coupon and live redemption count.
type CouponOffer struct {
	Code           string     `json:"code"`
	CouponID       string     `json:"coupon_id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"` // "percent" | "free_days"
	PercentOff     float64    `json:"percent_off,omitempty"`
	FreeDays       int        `json:"free_days,omitempty"`
	TimesRedeemed  int64      `json:"times_redeemed"`
	MaxRedemptions int64      `json:"max_redemptions,omitempty"`
	Active         bool       `json:"active"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

type CompRequest struct {
	UserID int64  `json:"user_id"`
	Action string `json:"action"` // "grant" | "revoke"
}

// ── Admin pricing DTOs ────────────────────────────────────

// PlanPrice is the current active price for a plan tier. Source of truth is the
// plan_prices table; /billing/config is built from these.
type PlanPrice struct {
	Tier            string    `json:"tier"`
	StripePriceID   string    `json:"stripe_price_id"`
	StripeProductID string    `json:"-"`
	Amount          int64     `json:"amount"` // minor units (cents)
	Currency        string    `json:"currency"`
	Interval        string    `json:"interval"`
	IntervalCount   int64     `json:"interval_count"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PriceChange is one audited price change (the tracking record).
type PriceChange struct {
	ID                  int64     `json:"id"`
	Tier                string    `json:"tier"`
	OldAmount           *int64    `json:"old_amount"`
	NewAmount           int64     `json:"new_amount"`
	OldPriceID          string    `json:"old_price_id,omitempty"`
	NewPriceID          string    `json:"new_price_id"`
	ChangedBy           *int64    `json:"changed_by"`
	ChangedByName       string    `json:"changed_by_name,omitempty"`
	Reason              string    `json:"reason"`
	NotifiedExisting    bool      `json:"notified_existing"`
	AffectedSubscribers int       `json:"affected_subscribers"`
	CreatedAt           time.Time `json:"created_at"`
}

// UpdatePriceRequest is the admin request to change a tier's price.
type UpdatePriceRequest struct {
	Amount         int64  `json:"amount"` // new price in minor units (cents)
	Reason         string `json:"reason"`
	NotifyExisting bool   `json:"notify_existing"` // email active subscribers about an increase
}

// AdminPricingResponse powers the admin Pricing tab: current prices + history.
type AdminPricingResponse struct {
	Enabled bool          `json:"enabled"`
	Prices  []PlanPrice   `json:"prices"`
	History []PriceChange `json:"history"`
}

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
	Amount   int64  `json:"amount"`   // smallest currency unit (e.g. cents)
	Currency string `json:"currency"` // ISO currency, lowercased (Stripe convention)
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

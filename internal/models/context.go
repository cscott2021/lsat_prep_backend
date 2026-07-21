package models

// ctxKey is the private type for request-context keys owned by this package.
// Using a dedicated type (rather than a bare string) prevents collisions with
// keys set by other packages on the same request context.
type ctxKey string

// FreeQuotaRemainingKey is the request-context key carrying the caller's
// remaining metered free-tier allowance. It is set by the billing paywall
// middleware (RequireEntitlementOrFreeQuota) and read by the drill handlers so a
// free user can never be served more questions than their remaining allowance.
//
// The value is an int:
//   - -1 means UNLIMITED (entitled/admin, or billing disabled → paywall open).
//   - >= 0 is the number of questions still allowed in the rolling 24h window.
const FreeQuotaRemainingKey ctxKey = "free_quota_remaining"

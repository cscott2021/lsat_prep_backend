package billing

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

// freeTierLimit is the number of questions a non-entitled user may answer per
// rolling 24h window on the metered drill endpoints.
const freeTierLimit = 3

// FreeQuotaCounter reports a user's rolling-24h answered-question count and the
// oldest answer time within that window. The questions store satisfies it
// structurally; it is an interface here to keep billing free of a dependency on
// the questions package (and to keep the money-path testable).
type FreeQuotaCounter interface {
	CountAnsweredLast24h(userID int64) (count int, oldestInWindow *time.Time, err error)
}

// RequireEntitlement gates a route behind an active entitlement. It must be
// layered on top of AuthMiddleware (which populates user_id in the request
// context).
//
// Responses:
//   - entitled            -> passes through
//   - past_due            -> 402 {"error":"payment_past_due"}
//   - anything else       -> 402 {"error":"subscription_required"}
//
// When billing is DISABLED (no Stripe key configured) the paywall FAILS OPEN:
// there is no way to purchase, so locking users out would break the app. This is
// what keeps nonprod usable without any Stripe configuration. Admins and comped
// users always pass (entitlement is derived with is_admin auto-comped).
func RequireEntitlement(svc *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail open when billing isn't configured.
			if !svc.Enabled() {
				next.ServeHTTP(w, r)
				return
			}

			userID, ok := r.Context().Value("user_id").(int64)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
				return
			}

			ent, err := svc.GetEntitlement(userID)
			if err != nil {
				// Transient infra error (e.g. an RDS failover): fail with a
				// retryable 503 rather than 402, so active subscribers aren't
				// thrown to the paywall mid-session for a backend hiccup.
				log.Printf("[billing] entitlement check failed for user %d: %v", userID, err)
				writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing temporarily unavailable"})
				return
			}

			if ent.Entitled {
				next.ServeHTTP(w, r)
				return
			}

			if ent.Status == models.SubStatusPastDue {
				writeJSON(w, http.StatusPaymentRequired, models.ErrorResponse{Error: "payment_past_due"})
				return
			}
			writeJSON(w, http.StatusPaymentRequired, models.ErrorResponse{Error: "subscription_required"})
		})
	}
}

// RequireEntitlementOrFreeQuota gates the metered drill endpoints. Unlike
// RequireEntitlement (which is all-or-nothing), a NON-entitled user is allowed a
// small metered free tier: freeTierLimit questions per rolling 24h window.
//
// It always records the caller's remaining allowance in the request context
// under models.FreeQuotaRemainingKey so the drill handlers can clamp how many
// questions they serve (a free user must never receive more than they have left).
//
// Decision table:
//   - billing disabled          -> pass, remaining = -1 (UNLIMITED; paywall open)
//   - entitled / admin          -> pass, remaining = -1 (UNLIMITED)
//   - free, used < limit        -> pass, remaining = limit - used  (>= 1)
//   - free, used >= limit        -> 402 {"error":"free_limit_reached", ...}
//
// Transient errors (entitlement lookup or the quota count) fail with a retryable
// 503 rather than granting free access beyond the limit. Entitled/paying users
// are resolved BEFORE the quota count, so they are never blocked by a counter
// failure and never touch the free-tier path.
func RequireEntitlementOrFreeQuota(svc *Service, counter FreeQuotaCounter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail open when billing isn't configured: no way to purchase, so the
			// free tier is not enforced either (matches RequireEntitlement).
			if !svc.Enabled() {
				next.ServeHTTP(w, withFreeQuotaRemaining(r, -1))
				return
			}

			userID, ok := r.Context().Value("user_id").(int64)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
				return
			}

			ent, err := svc.GetEntitlement(userID)
			if err != nil {
				log.Printf("[billing] entitlement check failed for user %d: %v", userID, err)
				writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing temporarily unavailable"})
				return
			}

			// Entitled/admin: unlimited. Resolved before any quota work so a paying
			// user is never blocked by a free-tier counter hiccup.
			if ent.Entitled {
				next.ServeHTTP(w, withFreeQuotaRemaining(r, -1))
				return
			}

			if counter == nil {
				// Misconfiguration (counter not wired). Fail closed for the free tier
				// with a retryable 503 rather than silently granting unlimited access.
				log.Printf("[billing] free-quota counter not configured; refusing free access for user %d", userID)
				writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing temporarily unavailable"})
				return
			}

			count, oldest, cerr := counter.CountAnsweredLast24h(userID)
			if cerr != nil {
				log.Printf("[billing] free-quota count failed for user %d: %v", userID, cerr)
				writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing temporarily unavailable"})
				return
			}

			if count >= freeTierLimit {
				resetAt := ""
				if oldest != nil {
					resetAt = oldest.Add(24 * time.Hour).Format(time.RFC3339)
				}
				writeJSON(w, http.StatusPaymentRequired, map[string]interface{}{
					"error":    "free_limit_reached",
					"reset_at": resetAt,
					"limit":    freeTierLimit,
				})
				return
			}

			next.ServeHTTP(w, withFreeQuotaRemaining(r, freeTierLimit-count))
		})
	}
}

// withFreeQuotaRemaining returns r with the remaining free-tier allowance stored
// in its context (-1 means unlimited).
func withFreeQuotaRemaining(r *http.Request, remaining int) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), models.FreeQuotaRemainingKey, remaining))
}

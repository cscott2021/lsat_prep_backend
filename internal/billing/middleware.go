package billing

import (
	"log"
	"net/http"

	"github.com/lsat-prep/backend/internal/models"
)

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
				// Do not hard-fail the request path on a transient DB error here;
				// log and deny with the generic paywall response.
				log.Printf("[billing] entitlement check failed for user %d: %v", userID, err)
				writeJSON(w, http.StatusPaymentRequired, models.ErrorResponse{Error: "subscription_required"})
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

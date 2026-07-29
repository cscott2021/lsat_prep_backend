package auth

import (
	"log"
	"net/http"

	"github.com/lsat-prep/backend/internal/models"
)

// AccountCleanup is an optional hook invoked BEFORE a user's row is deleted,
// letting main wire billing teardown (cancel Stripe subs, delete the customer)
// without the auth package importing billing. Best-effort by contract: the
// deletion proceeds even if cleanup fails. Wired once at startup from
// cmd/server/main.go; nil in tests and when billing is not in play.
var AccountCleanup func(userID int64)

// DeleteAccount permanently deletes the authenticated user's account, as Apple
// App Review requires for any app with account creation (guideline 5.1.1(v)).
//
// Deletion is a real DELETE on the users row: every per-user table
// (subscriptions, ability, gamification, history, bookmarks, friends, nudges,
// learn progress, password resets) references users(id) ON DELETE CASCADE, so
// the whole account footprint is removed in one statement. Tables that keep
// audit references (price_changes.changed_by, coupon redemption to_user_id)
// use ON DELETE SET NULL, so they survive anonymized.
//
// The request needs only a valid bearer token — no password re-entry — because
// the token IS the proof of ownership (same bar as every other authenticated
// endpoint), and extra friction here is itself a review risk.
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(int64)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	// Best-effort billing teardown first (cancel Stripe subs / delete customer);
	// an Apple sub can't be canceled server-side and is logged instead.
	if AccountCleanup != nil {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[auth] account cleanup panicked for user %d: %v", userID, rec)
				}
			}()
			AccountCleanup(userID)
		}()
	}

	res, err := h.db.Exec(`DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		log.Printf("[auth] delete account for user %d: %v", userID, err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to delete account"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "User not found"})
		return
	}

	log.Printf("[auth] account deleted: user %d", userID)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Your account has been permanently deleted."})
}

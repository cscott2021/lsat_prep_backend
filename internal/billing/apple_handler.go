package billing

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lsat-prep/backend/internal/models"
)

// AppleHandler exposes Apple App Store IAP endpoints: an authenticated verify
// endpoint the iOS app calls after a StoreKit purchase, and a PUBLIC
// notifications endpoint for App Store Server Notifications V2.
type AppleHandler struct {
	service *AppleService
}

func NewAppleHandler(service *AppleService) *AppleHandler {
	return &AppleHandler{service: service}
}

// RegisterRoutes wires the authenticated Apple route(s). The iOS app calls
// verify right after purchase/restore so entitlement activates immediately
// without waiting for Apple's server notification.
func (h *AppleHandler) RegisterRoutes(protected *mux.Router) {
	protected.HandleFunc("/billing/apple/verify", h.Verify).Methods("POST")
}

// RegisterWebhook mounts the PUBLIC App Store Server Notifications V2 route
// OUTSIDE AuthMiddleware (Apple cannot present a bearer token). Authenticity
// is established by verifying the signed JWS payload against Apple's root CAs
// inside the handler — analogous to the Stripe webhook signature.
func (h *AppleHandler) RegisterWebhook(public *mux.Router) {
	public.HandleFunc("/billing/apple/notifications", h.Notifications).Methods("POST")
}

// Verify handles POST /billing/apple/verify. Body: {signed_transaction,
// product_id}. Returns the post-purchase entitlement state.
func (h *AppleHandler) Verify(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	var req models.AppleVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	resp, err := h.service.VerifyPurchase(userID, req)
	if errors.Is(err, ErrAppleInvalid) {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		log.Printf("[billing/apple] verify: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "verification failed"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Notifications handles POST /billing/apple/notifications. Always 200 for a
// verified-and-handled (or safely ignorable) payload so Apple stops retrying;
// 400 for unverifiable payloads (forgeries, wrong app); 500 for transient
// processing errors (Apple will redeliver).
func (h *AppleHandler) Notifications(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxAppleNotificationBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "could not read body"})
		return
	}
	err = h.service.HandleNotification(body)
	if errors.Is(err, ErrAppleInvalid) {
		log.Printf("[billing/apple] notification rejected: %v", err)
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid notification"})
		return
	}
	if err != nil {
		log.Printf("[billing/apple] notification processing error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "processing failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

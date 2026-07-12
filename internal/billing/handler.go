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

// maxWebhookBody caps the webhook payload we buffer for signature verification.
const maxWebhookBody = 1 << 20 // 1 MiB

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func getUserID(r *http.Request) (int64, bool) {
	uid, ok := r.Context().Value("user_id").(int64)
	return uid, ok
}

// RegisterRoutes wires the protected user-facing billing routes. The webhook is
// registered separately (it must be PUBLIC — see RegisterWebhook).
func (h *Handler) RegisterRoutes(protected *mux.Router) {
	protected.HandleFunc("/billing/config", h.GetConfig).Methods("GET")
	protected.HandleFunc("/billing/subscription", h.GetSubscription).Methods("GET")
	protected.HandleFunc("/billing/subscribe", h.Subscribe).Methods("POST")
	protected.HandleFunc("/billing/apply-coupon", h.ApplyCoupon).Methods("POST")
	protected.HandleFunc("/billing/cancel", h.Cancel).Methods("POST")
	protected.HandleFunc("/billing/update-payment", h.UpdatePayment).Methods("POST")
	protected.HandleFunc("/billing/portal", h.Portal).Methods("POST")
}

// RegisterWebhook mounts the PUBLIC webhook route on the given (unauthenticated)
// router. Stripe cannot present a bearer token, so this must live OUTSIDE
// AuthMiddleware; the Stripe signature is the authentication.
func (h *Handler) RegisterWebhook(public *mux.Router) {
	public.HandleFunc("/billing/webhook", h.Webhook).Methods("POST")
}

// RegisterAdminRoutes wires admin-only billing routes (mounted behind
// AdminMiddleware by the caller).
func (h *Handler) RegisterAdminRoutes(admin *mux.Router) {
	admin.HandleFunc("/coupons", h.CreateCoupon).Methods("POST")
	admin.HandleFunc("/coupons", h.ListCoupons).Methods("GET")
	admin.HandleFunc("/comp", h.Comp).Methods("POST")
}

// writeDisabledOrError maps a service error to a response: ErrBillingDisabled ->
// 503, everything else -> 500. Returns true if it wrote a response.
func writeDisabledOrError(w http.ResponseWriter, err error, logCtx string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrBillingDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing not configured"})
		return true
	}
	log.Printf("[billing] %s: %v", logCtx, err)
	writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "billing operation failed"})
	return true
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	resp, err := h.service.BuildConfigResponse()
	if writeDisabledOrError(w, err, "GetConfig") {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	resp, err := h.service.GetSubscriptionResponse(userID)
	if err != nil {
		log.Printf("[billing] GetSubscription: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load subscription"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	var req models.SubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	resp, err := h.service.Subscribe(userID, req)
	if errors.Is(err, ErrBillingDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing not configured"})
		return
	}
	if err != nil {
		log.Printf("[billing] Subscribe: %v", err)
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ApplyCoupon(w http.ResponseWriter, r *http.Request) {
	var req models.ApplyCouponRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	resp, err := h.service.ApplyCoupon(req.Code)
	if errors.Is(err, ErrBillingDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	resp, err := h.service.Cancel(userID)
	if errors.Is(err, ErrBillingDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdatePayment(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	resp, err := h.service.UpdatePayment(userID)
	if writeDisabledOrError(w, err, "UpdatePayment") {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Portal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	resp, err := h.service.Portal(userID)
	if writeDisabledOrError(w, err, "Portal") {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Webhook is the PUBLIC Stripe webhook receiver. It verifies the signature and
// processes the event idempotently. It always returns 200 for a
// successfully-verified event (even for unhandled types) so Stripe stops
// retrying; only signature/verification failures return 4xx.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "could not read body"})
		return
	}
	signature := r.Header.Get("Stripe-Signature")

	err = h.service.HandleWebhook(payload, signature)
	if errors.Is(err, ErrBillingDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing not configured"})
		return
	}
	if err != nil {
		// Signature failures and unexpected processing errors: 400 so Stripe
		// retries (transient) and so a forged request is rejected.
		log.Printf("[billing] webhook rejected: %v", err)
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "webhook error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// ── Admin handlers ────────────────────────────────────────

func (h *Handler) CreateCoupon(w http.ResponseWriter, r *http.Request) {
	var req models.CreateCouponRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	resp, err := h.service.CreateOffer(req)
	if errors.Is(err, ErrBillingDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, models.ErrorResponse{Error: "billing not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) ListCoupons(w http.ResponseWriter, r *http.Request) {
	offers, err := h.service.ListOffers()
	if writeDisabledOrError(w, err, "ListCoupons") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"offers": offers})
}

func (h *Handler) Comp(w http.ResponseWriter, r *http.Request) {
	var req models.CompRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	if req.UserID <= 0 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "user_id is required"})
		return
	}
	if err := h.service.Comp(req.UserID, req.Action); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": req.Action + "ed", "user_id": int64ToStr(req.UserID)})
}

// writeJSON matches the JSON response helper used across the other packages.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

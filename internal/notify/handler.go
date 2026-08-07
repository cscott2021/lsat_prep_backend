package notify

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/lsat-prep/backend/internal/models"
)

// Handler exposes device-token registration to the app. Routes are
// AUTHENTICATED — a token is always bound to the calling user, never to a
// client-supplied user id.
type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes wires the device endpoints onto the authenticated subrouter.
func (h *Handler) RegisterRoutes(protected *mux.Router) {
	protected.HandleFunc("/devices", h.RegisterDevice).Methods("POST")
	protected.HandleFunc("/devices/{token}", h.UnregisterDevice).Methods("DELETE")
}

// RegisterDeviceRequest is POST /devices. Timezone is the device-reported
// IANA zone; unparseable/empty degrades to UTC rather than failing.
//
// The two preference fields are POINTERS on purpose: a client that predates
// them omits the keys entirely, and omission must mean "unchanged/opted in"
// rather than the zero value false, which would silently mute push for every
// user still on an older build.
type RegisterDeviceRequest struct {
	Platform            string `json:"platform"`
	Token               string `json:"token"`
	Timezone            string `json:"timezone"`
	PushStreakEnabled   *bool  `json:"push_streak_enabled"`
	PushReengageEnabled *bool  `json:"push_reengage_enabled"`
}

// optInOrDefault treats an absent preference as opted in.
func optInOrDefault(v *bool) bool { return v == nil || *v }

// RegisterDevice handles POST /devices — called on login, app start (when
// permission is granted), and APNs token refresh.
func (h *Handler) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(int64)
	if !ok {
		writeNotifyJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	var req RegisterDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeNotifyJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" || len(req.Token) > 256 {
		writeNotifyJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "token is required"})
		return
	}
	if req.Platform != "ios" && req.Platform != "android" {
		writeNotifyJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "platform must be 'ios' or 'android'"})
		return
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		req.Timezone = "UTC"
	}

	if err := h.store.UpsertToken(DeviceRegistration{
		UserID:        userID,
		Platform:      req.Platform,
		Token:         req.Token,
		Timezone:      req.Timezone,
		StreakOptIn:   optInOrDefault(req.PushStreakEnabled),
		ReengageOptIn: optInOrDefault(req.PushReengageEnabled),
	}); err != nil {
		writeNotifyJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to register device"})
		return
	}
	writeNotifyJSON(w, http.StatusOK, map[string]bool{"registered": true})
}

// UnregisterDevice handles DELETE /devices/{token} — called on logout so the
// signed-out device stops receiving pushes. Idempotent: deleting a token that
// isn't ours (or doesn't exist) is still a 200 — logout must never fail.
func (h *Handler) UnregisterDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(int64)
	if !ok {
		writeNotifyJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	token := mux.Vars(r)["token"]
	if token == "" {
		writeNotifyJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "token is required"})
		return
	}
	if err := h.store.DeleteToken(userID, token); err != nil {
		writeNotifyJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to unregister device"})
		return
	}
	writeNotifyJSON(w, http.StatusOK, map[string]bool{"unregistered": true})
}

// writeNotifyJSON matches the JSON response helper used across other packages.
func writeNotifyJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

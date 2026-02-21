package gamification

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/lsat-prep/backend/internal/models"
)

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

// ── Gamification State ──────────────────────────────────

func (h *Handler) GetGamification(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	resp, err := h.service.GetGamification(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get gamification state"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) BuyStreakFreeze(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	resp, err := h.service.BuyStreakFreeze(userID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) SetDailyGoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.SetDailyGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if err := h.service.SetDailyGoal(userID, req.Target); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"daily_goal_target": req.Target})
}

func (h *Handler) CompleteDrill(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.CompleteDrillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if len(req.QuestionIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "question_ids is required"})
		return
	}

	resp, err := h.service.CompleteDrill(userID, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to complete drill"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── Leaderboard ─────────────────────────────────────────

func (h *Handler) GlobalLeaderboard(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	limit := intQueryParam(r.URL.Query(), "limit", 20)

	resp, err := h.service.GetGlobalLeaderboard(userID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get leaderboard"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) FriendsLeaderboard(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	resp, err := h.service.GetFriendsLeaderboard(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get friends leaderboard"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── Friends ─────────────────────────────────────────────

func (h *Handler) ListFriends(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	resp, err := h.service.ListFriends(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get friends"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) SendFriendRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.FriendRequestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.ToUserID == 0 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "to_user_id is required"})
		return
	}

	resp, err := h.service.SendFriendRequest(userID, req.ToUserID)
	if err != nil {
		status := http.StatusBadRequest
		switch err.Error() {
		case "user not found":
			status = http.StatusNotFound
		case "friend request already exists":
			status = http.StatusConflict
		}
		writeJSON(w, status, models.ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) RespondFriendRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.FriendRespondReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.Action != "accept" && req.Action != "reject" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "action must be 'accept' or 'reject'"})
		return
	}

	if err := h.service.RespondFriendRequest(userID, req.FriendshipID, req.Action); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": req.Action + "ed"})
}

func (h *Handler) RemoveFriend(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	vars := mux.Vars(r)
	friendshipID, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid friendship ID"})
		return
	}

	if err := h.service.RemoveFriend(userID, friendshipID); err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *Handler) SearchUsers(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "q parameter is required"})
		return
	}

	results, err := h.service.SearchUsers(userID, q)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Search failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
}

// ── Nudges ──────────────────────────────────────────────

func (h *Handler) ListNudges(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	resp, err := h.service.GetNudges(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get nudges"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) SendNudge(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.SendNudgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.ReceiverID == 0 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "receiver_id is required"})
		return
	}

	id, err := h.service.SendNudge(userID, req)
	if err != nil {
		status := http.StatusBadRequest
		switch err.Error() {
		case "you can only nudge friends":
			status = http.StatusForbidden
		case "already nudged this person today":
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, models.ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{"nudge_id": id, "sent": true})
}

func (h *Handler) MarkNudgeRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	vars := mux.Vars(r)
	nudgeID, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid nudge ID"})
		return
	}

	if err := h.service.MarkNudgeRead(userID, nudgeID); err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "read"})
}

// ── Helpers ─────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func intQueryParam(query url.Values, key string, defaultVal int) int {
	s := query.Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return defaultVal
	}
	return v
}

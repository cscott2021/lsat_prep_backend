package questions

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/lsat-prep/backend/internal/models"
)

// RegisterHistoryRoutes registers history and bookmark endpoints on the protected subrouter.
func (h *Handler) RegisterHistoryRoutes(protected *mux.Router) {
	protected.HandleFunc("/history", h.GetHistory).Methods("GET")
	protected.HandleFunc("/history/mistakes", h.GetMistakes).Methods("GET")
	protected.HandleFunc("/history/stats", h.GetHistoryStats).Methods("GET")
	protected.HandleFunc("/history/drill-review", h.GetDrillReview).Methods("POST")

	protected.HandleFunc("/bookmarks", h.GetBookmarks).Methods("GET")
	protected.HandleFunc("/bookmarks/{questionID}", h.CreateBookmark).Methods("POST")
	protected.HandleFunc("/bookmarks/{questionID}", h.DeleteBookmark).Methods("DELETE")
}

func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	req := models.HistoryListRequest{
		Section:   queryStringPtr(r, "section"),
		Subtype:   queryStringPtr(r, "subtype"),
		Correct:   queryBoolPtr(r, "correct"),
		DateFrom:  queryStringPtr(r, "date_from"),
		DateTo:    queryStringPtr(r, "date_to"),
		SortBy:    queryStringDefault(r, "sort_by", "answered_at"),
		SortOrder: queryStringDefault(r, "sort_order", "desc"),
		Page:      intQueryParam(r.URL.Query(), "page", 1),
		PageSize:  intQueryParam(r.URL.Query(), "page_size", 20),
	}

	resp, err := h.service.GetUserHistory(userID, req)
	if err != nil {
		log.Printf("[handler] GetHistory error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get history"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetMistakes(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	page := intQueryParam(r.URL.Query(), "page", 1)
	pageSize := intQueryParam(r.URL.Query(), "page_size", 20)

	resp, err := h.service.GetUserMistakes(userID, page, pageSize)
	if err != nil {
		log.Printf("[handler] GetMistakes error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get mistakes"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetHistoryStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	resp, err := h.service.GetUserHistoryStats(userID)
	if err != nil {
		log.Printf("[handler] GetHistoryStats error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get history stats"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetDrillReview(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.DrillReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if len(req.QuestionIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "question_ids is required"})
		return
	}
	if len(req.QuestionIDs) > 50 {
		req.QuestionIDs = req.QuestionIDs[:50]
	}

	questions, err := h.service.GetDrillReview(userID, req.QuestionIDs)
	if err != nil {
		log.Printf("[handler] GetDrillReview error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get drill review"})
		return
	}

	if questions == nil {
		questions = []models.HistoryQuestion{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"questions": questions,
	})
}

func (h *Handler) CreateBookmark(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	questionID, err := strconv.ParseInt(mux.Vars(r)["questionID"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid question ID"})
		return
	}

	var req models.BookmarkRequest
	json.NewDecoder(r.Body).Decode(&req) // optional body

	var note *string
	if req.Note != "" {
		note = &req.Note
	}

	if err := h.service.CreateBookmark(userID, questionID, note); err != nil {
		log.Printf("[handler] CreateBookmark error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create bookmark"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"message": "bookmarked"})
}

func (h *Handler) DeleteBookmark(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	questionID, err := strconv.ParseInt(mux.Vars(r)["questionID"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid question ID"})
		return
	}

	if err := h.service.DeleteBookmark(userID, questionID); err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "Bookmark not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "unbookmarked"})
}

func (h *Handler) GetBookmarks(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	page := intQueryParam(r.URL.Query(), "page", 1)
	pageSize := intQueryParam(r.URL.Query(), "page_size", 20)

	resp, err := h.service.GetBookmarks(userID, page, pageSize)
	if err != nil {
		log.Printf("[handler] GetBookmarks error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get bookmarks"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── Query param helpers ──────────────────────────────────

func queryStringPtr(r *http.Request, key string) *string {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil
	}
	return &v
}

func queryBoolPtr(r *http.Request, key string) *bool {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil
	}
	b := v == "true"
	return &b
}

func queryStringDefault(r *http.Request, key, defaultVal string) string {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal
	}
	return v
}

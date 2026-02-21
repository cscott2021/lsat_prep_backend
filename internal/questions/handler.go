package questions

import (
	"encoding/json"
	"log"
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

// getUserID extracts the authenticated user ID from the request context.
func getUserID(r *http.Request) (int64, bool) {
	uid, ok := r.Context().Value("user_id").(int64)
	return uid, ok
}

func (h *Handler) GenerateBatch(w http.ResponseWriter, r *http.Request) {
	var req models.GenerateBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	// Validate section
	if req.Section != models.SectionLR && req.Section != models.SectionRC {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "section must be 'logical_reasoning' or 'reading_comprehension'"})
		return
	}

	// Validate LR subtype
	if req.Section == models.SectionLR {
		if req.LRSubtype == nil {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "lr_subtype is required for logical_reasoning"})
			return
		}
		if !models.ValidLRSubtypes[*req.LRSubtype] {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid lr_subtype"})
			return
		}
	}

	// Validate difficulty
	if req.Difficulty != models.DifficultyEasy && req.Difficulty != models.DifficultyMedium && req.Difficulty != models.DifficultyHard {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "difficulty must be 'easy', 'medium', or 'hard'"})
		return
	}

	// Default count
	if req.Count <= 0 {
		req.Count = 6
	}

	resp, err := h.service.GenerateBatch(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Generation failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	var status *models.BatchStatus
	if s := query.Get("status"); s != "" {
		bs := models.BatchStatus(s)
		status = &bs
	}

	limit := intQueryParam(query, "limit", 20)
	offset := intQueryParam(query, "offset", 0)

	batches, err := h.service.ListBatches(status, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to list batches"})
		return
	}

	if batches == nil {
		batches = []models.QuestionBatch{}
	}
	writeJSON(w, http.StatusOK, batches)
}

func (h *Handler) GetBatch(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid batch ID"})
		return
	}

	batch, err := h.service.GetBatch(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "Batch not found"})
		return
	}

	writeJSON(w, http.StatusOK, batch)
}

func (h *Handler) GetQuestion(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid question ID"})
		return
	}

	question, err := h.service.GetQuestion(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "Question not found"})
		return
	}

	writeJSON(w, http.StatusOK, question)
}

func (h *Handler) SubmitAnswer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid question ID"})
		return
	}

	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.SubmitAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.SelectedChoiceID == "" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "selected_choice_id is required"})
		return
	}

	validChoices := map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true}
	if !validChoices[req.SelectedChoiceID] {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "selected_choice_id must be A, B, C, D, or E"})
		return
	}

	resp, err := h.service.SubmitAnswer(userID, id, req.SelectedChoiceID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "Question not found"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── Adaptive System Handlers ────────────────────────────

func (h *Handler) GetAbility(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	abilities, err := h.service.GetAbilities(userID)
	if err != nil {
		log.Printf("[handler] GetAbility error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get ability scores"})
		return
	}

	writeJSON(w, http.StatusOK, abilities)
}

func (h *Handler) SetDifficultySlider(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.DifficultySliderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	if req.SliderValue < 0 || req.SliderValue > 100 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "slider value must be between 0 and 100"})
		return
	}

	if err := h.service.SetDifficultySlider(userID, req.SliderValue); err != nil {
		log.Printf("[handler] SetDifficultySlider error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to update difficulty slider"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"difficulty_slider": req.SliderValue})
}

func (h *Handler) QuickDrill(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.QuickDrillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	// Validate section
	if req.Section != string(models.SectionLR) && req.Section != string(models.SectionRC) && req.Section != "both" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "section must be 'logical_reasoning', 'reading_comprehension', or 'both'"})
		return
	}

	// Default count
	if req.Count <= 0 {
		req.Count = 6
	}

	questions, err := h.service.GetQuickDrill(r.Context(), userID, req)
	if err != nil {
		log.Printf("[handler] QuickDrill error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get drill questions"})
		return
	}

	writeJSON(w, http.StatusOK, models.DrillListResponse{
		Questions: questions,
		Total:     len(questions),
		Page:      1,
		PageSize:  req.Count,
	})
}

func (h *Handler) SubtypeDrill(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	var req models.SubtypeDrillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	// Validate section
	if req.Section != string(models.SectionLR) && req.Section != string(models.SectionRC) {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "section must be 'logical_reasoning' or 'reading_comprehension'"})
		return
	}

	// Validate subtype
	if req.Section == string(models.SectionLR) {
		if req.LRSubtype == nil {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "lr_subtype is required for logical_reasoning"})
			return
		}
		if !models.ValidLRSubtypes[models.LRSubtype(*req.LRSubtype)] {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid LR subtype"})
			return
		}
	} else {
		if req.RCSubtype == nil {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "rc_subtype is required for reading_comprehension"})
			return
		}
		if !models.ValidRCSubtypes[models.RCSubtype(*req.RCSubtype)] {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid RC subtype"})
			return
		}
	}

	// Default count
	if req.Count <= 0 {
		req.Count = 6
	}

	questions, err := h.service.GetSubtypeDrill(r.Context(), userID, req)
	if err != nil {
		log.Printf("[handler] SubtypeDrill error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get drill questions"})
		return
	}

	writeJSON(w, http.StatusOK, models.DrillListResponse{
		Questions: questions,
		Total:     len(questions),
		Page:      1,
		PageSize:  req.Count,
	})
}

// ── Admin Handlers ──────────────────────────────────────

func (h *Handler) GetQualityStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.GetQualityStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get quality stats"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) GetGenerationStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.GetGenerationStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get generation stats"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) Recalibrate(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.RecalibrateDifficulty()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Recalibration failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) GetFlaggedQuestions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := intQueryParam(query, "limit", 20)
	offset := intQueryParam(query, "offset", 0)

	questions, total, err := h.service.GetFlaggedQuestions(limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get flagged questions"})
		return
	}

	if questions == nil {
		questions = []models.Question{}
	}

	writeJSON(w, http.StatusOK, models.QuestionListResponse{
		Questions: questions,
		Total:     total,
		Page:      offset/limit + 1,
		PageSize:  limit,
	})
}

func (h *Handler) ExportQuestions(w http.ResponseWriter, r *http.Request) {
	envelope, err := h.service.ExportQuestions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Export failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, envelope)
}

func (h *Handler) ImportQuestions(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20) // 50MB limit

	var envelope models.ExportEnvelope
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body: " + err.Error()})
		return
	}

	if len(envelope.Questions) == 0 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "No questions in payload"})
		return
	}

	result, err := h.service.ImportQuestions(r.Context(), envelope)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Import failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

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

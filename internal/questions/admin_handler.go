package questions

import (
	"log"
	"net/http"

	"github.com/lsat-prep/backend/internal/models"
)

// ── Service wrappers ──────────────────────────────────────

func (s *Service) GetAdminQuestions(f AdminQuestionFilter, limit, offset int) ([]models.Question, int, error) {
	return s.store.GetAdminQuestions(f, limit, offset)
}

func (s *Service) GetQuestionSpread() (*models.QuestionSpread, error) {
	return s.store.GetQuestionSpread()
}

func (s *Service) GetUserProgress(limit, offset int) (*models.UserProgressResponse, error) {
	return s.store.GetUserProgress(limit, offset)
}

// ── Handlers (admin-only; mounted behind AdminMiddleware) ──

// GetAdminQuestions is the question-bank browser. Filters: section, subtype,
// difficulty, status, flagged, search; paginated via limit/offset.
func (h *Handler) GetAdminQuestions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var flagged *bool
	if fv := q.Get("flagged"); fv != "" {
		b := fv == "true"
		flagged = &b
	}
	f := AdminQuestionFilter{
		Section:    q.Get("section"),
		Subtype:    q.Get("subtype"),
		Difficulty: q.Get("difficulty"),
		Status:     q.Get("status"),
		Flagged:    flagged,
		Search:     q.Get("search"),
	}
	limit := limitQueryParam(q, "limit", 20, 100)
	offset := intQueryParam(q, "offset", 0)

	questions, total, err := h.service.GetAdminQuestions(f, limit, offset)
	if err != nil {
		log.Printf("[handler] GetAdminQuestions error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load questions"})
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

func (h *Handler) GetQuestionSpread(w http.ResponseWriter, r *http.Request) {
	spread, err := h.service.GetQuestionSpread()
	if err != nil {
		log.Printf("[handler] GetQuestionSpread error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load question spread"})
		return
	}
	writeJSON(w, http.StatusOK, spread)
}

func (h *Handler) GetUserProgress(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := limitQueryParam(q, "limit", 50, 200)
	offset := intQueryParam(q, "offset", 0)

	progress, err := h.service.GetUserProgress(limit, offset)
	if err != nil {
		log.Printf("[handler] GetUserProgress error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load user progress"})
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

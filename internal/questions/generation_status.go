package questions

import (
	"log"
	"net/http"

	"github.com/lsat-prep/backend/internal/models"
)

// CountUnseenForUserSection counts servable questions in a section that the user
// has not yet answered (across all subtypes).
func (s *Store) CountUnseenForUserSection(userID int64, section string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*)
		 FROM questions q
		 LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
		 WHERE q.section = $2
		   AND h.id IS NULL
		   AND q.validation_status IN ('passed', 'unvalidated')
		   AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)`,
		userID, section,
	).Scan(&count)
	return count, err
}

// HasPendingGeneration reports whether a generation is queued or in-flight for
// the given section, optionally scoped to a subtype.
func (s *Store) HasPendingGeneration(section, subtype string) (bool, error) {
	var n int
	var err error
	if subtype == "" {
		err = s.db.QueryRow(
			`SELECT COUNT(*) FROM generation_queue
			 WHERE section = $1 AND status IN ('pending', 'generating')`,
			section,
		).Scan(&n)
	} else {
		err = s.db.QueryRow(
			`SELECT COUNT(*) FROM generation_queue
			 WHERE section = $1 AND (lr_subtype = $2 OR rc_subtype = $2)
			   AND status IN ('pending', 'generating')`,
			section, subtype,
		).Scan(&n)
	}
	return n > 0, err
}

// GenerationStatus reports how many unseen questions are ready for the user in
// this section (optionally subtype) and whether more are being generated. The
// client polls this from the "preparing your questions" offramp and starts the
// drill once enough are ready.
func (s *Service) GenerationStatus(userID int64, section, subtype string) (*models.GenerationStatus, error) {
	var unseen int
	var err error
	if subtype == "" {
		unseen, err = s.store.CountUnseenForUserSection(userID, section)
	} else {
		unseen, err = s.store.CountUnseenForUser(userID, section, subtype)
	}
	if err != nil {
		return nil, err
	}
	generating, err := s.store.HasPendingGeneration(section, subtype)
	if err != nil {
		return nil, err
	}
	return &models.GenerationStatus{
		Ready:      unseen,
		Generating: generating,
		Target:     s.autoGenMinUnseen,
	}, nil
}

func (h *Handler) GenerationStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}
	q := r.URL.Query()
	section := q.Get("section")
	if section == "" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "section is required"})
		return
	}
	status, err := h.service.GenerationStatus(userID, section, q.Get("subtype"))
	if err != nil {
		log.Printf("[handler] GenerationStatus error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to get generation status"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

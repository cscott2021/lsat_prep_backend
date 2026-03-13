package learn

import (
	"encoding/json"
	"log"
	"net/http"
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

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func getUserID(r *http.Request) (int64, bool) {
	uid, ok := r.Context().Value("user_id").(int64)
	return uid, ok
}

// GET /learn/guides?section=logical_reasoning
func (h *Handler) ListGuides(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	section := r.URL.Query().Get("section")
	if section == "" {
		section = "logical_reasoning"
	}

	if section != string(models.SectionLR) && section != string(models.SectionRC) {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{
			Error: "Invalid section. Must be 'logical_reasoning' or 'reading_comprehension'.",
		})
		return
	}

	resp, err := h.service.ListGuides(userID, models.Section(section))
	if err != nil {
		log.Printf("[learn] ListGuides error: %v", err)
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch guides."})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// GET /learn/guides/{id}
func (h *Handler) GetGuide(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	guideID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid guide ID."})
		return
	}

	resp, err := h.service.GetGuide(userID, guideID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "Guide not found."})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// GET /learn/guides/by-subtype?section=logical_reasoning&subtype=strengthen
func (h *Handler) GetGuideBySubtype(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required"})
		return
	}

	section := r.URL.Query().Get("section")
	subtype := r.URL.Query().Get("subtype")

	if section == "" || subtype == "" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{
			Error: "Both 'section' and 'subtype' query parameters are required.",
		})
		return
	}

	if section != string(models.SectionLR) && section != string(models.SectionRC) {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid section."})
		return
	}

	if section == string(models.SectionLR) {
		if _, ok := models.ValidLRSubtypes[models.LRSubtype(subtype)]; !ok {
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid LR subtype."})
			return
		}
	}

	resp, err := h.service.GetGuideBySubtype(userID, models.Section(section), subtype)
	if err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "Guide not found."})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

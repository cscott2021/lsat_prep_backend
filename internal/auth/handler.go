package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lsat-prep/backend/internal/database"
	"github.com/lsat-prep/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// JWTSecret is the HMAC signing key for auth tokens.
// This is a server-side secret — it never leaves the backend.
var JWTSecret = []byte("lsat-prep-staging-signing-key-2026")

type Handler struct {
	db *sql.DB
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)

	if req.Email == "" || req.Name == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Email, name, and password are required"})
		return
	}

	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}

	// Generate a unique username
	username := database.GenerateUsername(req.Name)

	var user models.User
	// Try up to 5 times in case of username collision
	var insertErr error
	for attempt := 0; attempt < 5; attempt++ {
		insertErr = h.db.QueryRow(
			`INSERT INTO users (email, name, username, password, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id, email, name, username, created_at, updated_at`,
			req.Email, req.Name, username, string(hashedPassword), time.Now(), time.Now(),
		).Scan(&user.ID, &user.Email, &user.Name, &user.Username, &user.CreatedAt, &user.UpdatedAt)

		if insertErr == nil {
			break
		}
		if strings.Contains(insertErr.Error(), "users_username_key") {
			// Username collision — regenerate and retry
			username = database.GenerateUsername(req.Name)
			continue
		}
		break // Other error, stop retrying
	}
	err = insertErr

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeJSON(w, http.StatusConflict, models.ErrorResponse{Error: "An account with this email already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create account"})
		return
	}

	token, err := generateToken(user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to generate token"})
		return
	}

	writeJSON(w, http.StatusCreated, models.AuthResponse{Token: token, User: user})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Email and password are required"})
		return
	}

	var user models.User
	var hashedPassword string
	err := h.db.QueryRow(
		`SELECT id, email, name, COALESCE(username, ''), password, created_at, updated_at FROM users WHERE email = $1`,
		req.Email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Username, &hashedPassword, &user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Invalid email or password"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, models.ErrorResponse{Error: "Invalid email or password"})
		return
	}

	token, err := generateToken(user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to generate token"})
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{Token: token, User: user})
}

func (h *Handler) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)

	var user models.User
	err := h.db.QueryRow(
		`SELECT id, email, name, COALESCE(username, ''), created_at, updated_at FROM users WHERE id = $1`,
		userID,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Username, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "User not found"})
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func generateToken(userID int64) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(72 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(JWTSecret)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

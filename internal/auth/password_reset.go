package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lsat-prep/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// ForgotPassword issues a password-reset token and queues a reset-link email.
// It ALWAYS responds 200 (even for unknown emails) so it can't be used to probe
// which addresses are registered.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	ok := map[string]string{"message": "If that email is registered, a reset link is on its way."}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		writeJSON(w, http.StatusOK, ok)
		return
	}

	var userID int64
	var name string
	err := h.db.QueryRow(`SELECT id, name FROM users WHERE email = $1`, email).Scan(&userID, &name)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusOK, ok) // don't leak existence
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}

	// Random token; store only its hash. The raw token goes in the email link.
	raw := make([]byte, 32)
	if _, rerr := rand.Read(raw); rerr != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}
	token := hex.EncodeToString(raw)
	tokenHash := sha256Hex(token)

	// Invalidate any prior unused tokens, then store the new one (1h expiry).
	_, _ = h.db.Exec(`UPDATE password_reset_tokens SET used = TRUE WHERE user_id = $1 AND used = FALSE`, userID)
	if _, ierr := h.db.Exec(
		`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, time.Now().Add(time.Hour),
	); ierr != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}

	base := os.Getenv("APP_BASE_URL")
	if base == "" {
		base = "https://app.scoreright.app"
	}
	// Hash route — the web app uses Flutter's default hash URL strategy.
	link := strings.TrimRight(base, "/") + "/#/reset-password?token=" + token
	subject := "Reset your Score Right password"
	body := "Hi " + firstName(name) + ",\n\n" +
		"We received a request to reset your Score Right password. Click the link below to choose a new one — it expires in 1 hour:\n\n" +
		link + "\n\n" +
		"If you didn't request this, you can safely ignore this email; your password won't change.\n\n" +
		"— The Score Right team"

	// Queue via the shared email outbox (drained by the billing email worker).
	if _, qerr := h.db.Exec(
		`INSERT INTO email_outbox (to_email, to_user_id, subject, body, category)
		 VALUES ($1, $2, $3, $4, 'password_reset')`,
		email, userID, subject, body,
	); qerr != nil {
		log.Printf("[auth] queue reset email failed for %s: %v", email, qerr)
	}
	writeJSON(w, http.StatusOK, ok)
}

// ResetPassword consumes a valid reset token and sets a new password.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body"})
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Reset token is required"})
		return
	}
	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "Password must be at least 8 characters"})
		return
	}

	var userID int64
	err := h.db.QueryRow(
		`SELECT user_id FROM password_reset_tokens
		  WHERE token_hash = $1 AND used = FALSE AND expires_at > NOW()`,
		sha256Hex(req.Token),
	).Scan(&userID)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "This reset link is invalid or has expired. Request a new one."})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}

	hashed, herr := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if herr != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}
	if _, uerr := h.db.Exec(`UPDATE users SET password = $1, updated_at = NOW() WHERE id = $2`, string(hashed), userID); uerr != nil {
		writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: "Internal server error"})
		return
	}
	// Consume the token and invalidate any others for this user.
	_, _ = h.db.Exec(`UPDATE password_reset_tokens SET used = TRUE WHERE user_id = $1`, userID)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Your password has been reset. You can now sign in."})
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func firstName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "there"
	}
	if i := strings.IndexByte(name, ' '); i > 0 {
		return name[:i]
	}
	return name
}

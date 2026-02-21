package models

import (
	"strings"
	"time"
)

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Username  string    `json:"username"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DisplayName returns "FirstName L." format (first name + last initial).
func (u User) DisplayName() string {
	parts := splitName(u.Name)
	if len(parts) <= 1 {
		return u.Name
	}
	lastName := parts[len(parts)-1]
	if len(lastName) > 0 {
		return parts[0] + " " + string([]rune(lastName)[0]) + "."
	}
	return parts[0]
}

func splitName(name string) []string {
	var parts []string
	for _, p := range strings.Split(strings.TrimSpace(name), " ") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

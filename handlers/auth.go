package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"authentication-backend/utils"

	"github.com/jackc/pgx/v5"
)

type AuthHandler struct {
	DB *pgx.Conn
}

type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {

	var request RegisterRequest

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if request.Name == "" || request.Email == "" || request.Password == "" {
		http.Error(w, "Name, email and password are required", http.StatusBadRequest)
		return
	}

	var existingEmail string

	err = h.DB.QueryRow(
		context.Background(),
		"SELECT email FROM users WHERE email = $1",
		request.Email,
	).Scan(&existingEmail)

	if err == nil {
		http.Error(w, "Email already registered", http.StatusConflict)
		return
	}

	if err != pgx.ErrNoRows {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	hashedPassword, err := utils.HashPassword(request.Password)
	if err != nil {
		http.Error(w, "Password hashing failed", http.StatusInternalServerError)
		return
	}

	var userID int64

	err = h.DB.QueryRow(
		context.Background(),
		`INSERT INTO users 
		(name, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id`,
		request.Name,
		request.Email,
		hashedPassword,
	).Scan(&userID)

	if err != nil {
		http.Error(w, "User creation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User registered successfully",
		"user_id": userID,
	})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {

	var request LoginRequest

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var userID int64
	var passwordHash string

	err = h.DB.QueryRow(
		context.Background(),
		"SELECT id, password_hash FROM users WHERE email = $1",
		request.Email,
	).Scan(&userID, &passwordHash)

	if err != nil {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}

	err = utils.CheckPassword(request.Password, passwordHash)

	if err != nil {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Login successful",
		"user_id": userID,
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Logout successful",
	})
}

func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {

	var request struct {
		Email string `json:"email"`
	}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var userID int64

	err = h.DB.QueryRow(
		context.Background(),
		"SELECT id FROM users WHERE email = $1",
		request.Email,
	).Scan(&userID)

	if err != nil {
		http.Error(w, "If the email exists, a reset process has been started", http.StatusOK)
		return
	}

	resetToken := fmt.Sprintf("%d-%d", userID, time.Now().UnixNano())

	expiry := time.Now().Add(15 * time.Minute)

	_, err = h.DB.Exec(
		context.Background(),
		`UPDATE users
		SET reset_token = $1,
		    reset_token_expires_at = $2
		WHERE id = $3`,
		resetToken,
		expiry,
		userID,
	)

	if err != nil {
		http.Error(w, "Could not create reset token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Password reset process started",
		"reset_token": resetToken,
	})
}

func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {

	var request struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var userID int64

	err = h.DB.QueryRow(
		context.Background(),
		`SELECT id FROM users
		WHERE reset_token = $1
		AND reset_token_expires_at > CURRENT_TIMESTAMP`,
		request.Token,
	).Scan(&userID)

	if err != nil {
		http.Error(w, "Invalid or expired reset token", http.StatusBadRequest)
		return
	}

	hashedPassword, err := utils.HashPassword(request.NewPassword)
	if err != nil {
		http.Error(w, "Password hashing failed", http.StatusInternalServerError)
		return
	}

	_, err = h.DB.Exec(
		context.Background(),
		`UPDATE users
		SET password_hash = $1,
		    reset_token = NULL,
		    reset_token_expires_at = NULL,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $2`,
		hashedPassword,
		userID,
	)

	if err != nil {
		http.Error(w, "Password reset failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Password reset successful",
	})
}

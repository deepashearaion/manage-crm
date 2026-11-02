package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/utils"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	resetTokenTTL = 30 * time.Minute

	// forgotPasswordMessage is returned for every forgot-password request, so
	// the response never reveals whether an account exists.
	forgotPasswordMessage = "If an account exists for that email, a password reset link has been sent" //nolint:gosec // G101: a message, not a credential
)

type AuthHandler struct {
	DB     *pgxpool.Pool
	Tokens *utils.JWT
	Mailer mail.Mailer
	Logger *slog.Logger

	// FrontendURL is the base of the reset link sent by email.
	FrontendURL string

	// ReturnResetTokenInResponse keeps the legacy behavior of returning the
	// reset token in the forgot-password response (development only).
	ReturnResetTokenInResponse bool
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

	request.Name = strings.TrimSpace(request.Name)
	request.Email = utils.NormalizeEmail(request.Email)

	if request.Name == "" ||
		request.Email == "" ||
		request.Password == "" {

		http.Error(
			w,
			"Name, email and password are required",
			http.StatusBadRequest,
		)
		return
	}

	if !utils.ValidEmail(request.Email) {
		http.Error(w, utils.InvalidEmailMessage, http.StatusBadRequest)
		return
	}

	if !utils.ValidPassword(request.Password) {
		http.Error(w, utils.PasswordLengthMessage, http.StatusBadRequest)
		return
	}

	var existingEmail string

	err = h.DB.QueryRow(
		r.Context(),
		"SELECT email FROM users WHERE email = $1",
		request.Email,
	).Scan(&existingEmail)

	if err == nil {
		http.Error(
			w,
			"Email already registered",
			http.StatusConflict,
		)
		return
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		h.Logger.ErrorContext(r.Context(), "register: look up email", slog.Any("error", err))
		http.Error(
			w,
			"Database error",
			http.StatusInternalServerError,
		)
		return
	}

	hashedPassword, err := utils.HashPassword(request.Password)

	if err != nil {
		http.Error(
			w,
			"Password hashing failed",
			http.StatusInternalServerError,
		)
		return
	}

	var userID int64

	err = h.DB.QueryRow(
		r.Context(),
		`INSERT INTO users
		(name, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id`,
		request.Name,
		request.Email,
		hashedPassword,
	).Scan(&userID)

	if err != nil {
		// A concurrent registration can win the race after the check above.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "Email already registered", http.StatusConflict)
			return
		}
		h.Logger.ErrorContext(r.Context(), "register: insert user", slog.Any("error", err))
		http.Error(
			w,
			"User creation failed",
			http.StatusInternalServerError,
		)
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
		http.Error(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	var userID int64
	var passwordHash string

	err = h.DB.QueryRow(
		r.Context(),
		"SELECT id, password_hash FROM users WHERE email = $1",
		utils.NormalizeEmail(request.Email),
	).Scan(&userID, &passwordHash)

	if err != nil {
		http.Error(
			w,
			"Invalid email or password",
			http.StatusUnauthorized,
		)
		return
	}

	err = utils.CheckPassword(
		request.Password,
		passwordHash,
	)

	if err != nil {
		http.Error(
			w,
			"Invalid email or password",
			http.StatusUnauthorized,
		)
		return
	}

	// Generate JWT token after successful login
	token, err := h.Tokens.Generate(userID)

	if err != nil {
		http.Error(
			w,
			"Failed to generate token",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Login successful",
		"user_id": userID,
		"token":   token,
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Logout successful",
	})
}

// ForgotPassword always answers 200 with the same message. When the account
// exists it stores a hashed, single-use token and emails a reset link.
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {

	var request struct {
		Email string `json:"email"`
	}

	err := json.NewDecoder(r.Body).Decode(&request)

	if err != nil {
		http.Error(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	response := map[string]interface{}{
		"message": forgotPasswordMessage,
	}

	token, err := h.startPasswordReset(r.Context(), utils.NormalizeEmail(request.Email))
	if err != nil {
		// Logged, not returned: an error here only happens for existing
		// accounts, so reporting it would reveal that the account exists.
		h.Logger.ErrorContext(r.Context(), "forgot password", slog.Any("error", err))
	}

	if token != "" && h.ReturnResetTokenInResponse {
		response["reset_token"] = token
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(response)
}

// startPasswordReset returns the new token, or "" when no account matches.
func (h *AuthHandler) startPasswordReset(ctx context.Context, email string) (string, error) {

	var userID int64
	var name string

	err := h.DB.QueryRow(
		ctx,
		"SELECT id, name FROM users WHERE email = $1",
		email,
	).Scan(&userID, &name)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up user: %w", err)
	}

	token, tokenHash, err := newResetToken()
	if err != nil {
		return "", err
	}

	// Only the newest link works: earlier unused tokens are invalidated.
	err = pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE password_reset_tokens SET used_at = now()
			WHERE user_id = $1 AND used_at IS NULL`,
			userID,
		); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
			VALUES ($1, $2, now() + make_interval(secs => $3))`,
			userID,
			tokenHash,
			resetTokenTTL.Seconds(),
		)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("store reset token: %w", err)
	}

	link := h.FrontendURL + "/reset-password?token=" + url.QueryEscape(token)

	err = h.Mailer.Send(ctx, mail.Message{
		To:      email,
		Subject: "Reset your password",
		Text: fmt.Sprintf(
			"Hi %s,\n\nWe received a request to reset your password. Open this link within %d minutes to choose a new one:\n\n%s\n\nIf you did not ask for this, you can ignore this email; your password will not change.\n",
			name, int(resetTokenTTL.Minutes()), link,
		),
	})
	if err != nil {
		return token, fmt.Errorf("send reset email: %w", err)
	}

	return token, nil
}

// newResetToken returns a random URL-safe token and the SHA-256 hash that is
// stored in its place.
func newResetToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashResetToken(token), nil
}

func hashResetToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {

	var request struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}

	err := json.NewDecoder(r.Body).Decode(&request)

	if err != nil {
		http.Error(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	if !utils.ValidPassword(request.NewPassword) {
		http.Error(w, utils.PasswordLengthMessage, http.StatusBadRequest)
		return
	}

	hashedPassword, err := utils.HashPassword(
		request.NewPassword,
	)

	if err != nil {
		http.Error(
			w,
			"Password hashing failed",
			http.StatusInternalServerError,
		)
		return
	}

	errInvalidToken := errors.New("invalid or expired reset token")

	// Consume the token, set the password and invalidate any other
	// outstanding tokens in one transaction, so a token works exactly once.
	err = pgx.BeginFunc(r.Context(), h.DB, func(tx pgx.Tx) error {
		var userID int64
		err := tx.QueryRow(
			r.Context(),
			`UPDATE password_reset_tokens SET used_at = now()
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
			RETURNING user_id`,
			hashResetToken(request.Token),
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken
		}
		if err != nil {
			return err
		}

		if _, err := tx.Exec(
			r.Context(),
			`UPDATE users
			SET password_hash = $1,
			    updated_at = CURRENT_TIMESTAMP
			WHERE id = $2`,
			hashedPassword,
			userID,
		); err != nil {
			return err
		}

		_, err = tx.Exec(
			r.Context(),
			`UPDATE password_reset_tokens SET used_at = now()
			WHERE user_id = $1 AND used_at IS NULL`,
			userID,
		)
		return err
	})

	if errors.Is(err, errInvalidToken) {
		http.Error(
			w,
			"Invalid or expired reset token",
			http.StatusBadRequest,
		)
		return
	}

	if err != nil {
		h.Logger.ErrorContext(r.Context(), "reset password", slog.Any("error", err))
		http.Error(
			w,
			"Password reset failed",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]string{
		"message": "Password reset successful",
	})
}

// Protected API
func (h *AuthHandler) Profile(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]string{
		"message": "You accessed a protected API successfully",
	})
}

package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
)

// RefreshCookieName is the httpOnly cookie that carries the refresh token.
// It is scoped to /api/auth so it is only sent to login, refresh and logout.
const (
	RefreshCookieName = "crm_refresh_token"
	refreshCookiePath = "/api/auth"
)

// forgotPasswordMessage is returned for every forgot-password request, so
// the response never reveals whether an account exists.
const forgotPasswordMessage = "If an account exists for that email, a password reset link has been sent" //nolint:gosec // G101: a message, not a credential

// HandlerSettings configures the HTTP layer.
type HandlerSettings struct {
	// CookieSecure marks the refresh cookie HTTPS-only (off for local HTTP).
	CookieSecure bool
	// ReturnResetTokenInResponse keeps the legacy behavior of returning the
	// reset token in the forgot-password response (development only).
	ReturnResetTokenInResponse bool
}

// Handler serves the legacy /api/auth routes and /api/profile, whose errors
// are plain text (http.Error) with the messages the frontend already shows.
type Handler struct {
	svc      *Service
	logger   *slog.Logger
	settings HandlerSettings
}

func NewHandler(svc *Service, logger *slog.Logger, settings HandlerSettings) *Handler {
	return &Handler{svc: svc, logger: logger, settings: settings}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		// OrganizationName is optional; it defaults to "<name>'s organization".
		OrganizationName string `json:"organization_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	userID, err := h.svc.Register(r.Context(), req.Name, req.Email, req.Password, req.OrganizationName)
	switch {
	case errors.Is(err, ErrMissingFields):
		http.Error(w, "Name, email and password are required", http.StatusBadRequest)
		return
	case errors.Is(err, ErrInvalidEmail):
		http.Error(w, "Invalid email address", http.StatusBadRequest)
		return
	case errors.Is(err, ErrPasswordLength):
		http.Error(w, "Password must be 8 to 72 characters long", http.StatusBadRequest)
		return
	case errors.Is(err, ErrEmailTaken):
		http.Error(w, "Email already registered", http.StatusConflict)
		return
	case err != nil:
		h.serverError(r, "register", err)
		http.Error(w, "User creation failed", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"message": "User registered successfully",
		"user_id": userID,
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	session, err := h.svc.Login(r.Context(), req.Email, req.Password)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	case err != nil:
		h.serverError(r, "login", err)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	h.setRefreshCookie(w, session.RefreshToken)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"message":    "Login successful",
		"user_id":    session.UserID,
		"token":      session.AccessToken,
		"expires_in": int(h.svc.AccessTokenTTL().Seconds()),
	})
}

// Refresh exchanges the refresh cookie for a new access token and rotates
// the cookie. Reusing an already-rotated refresh token ends the session.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var token string
	if cookie, err := r.Cookie(RefreshCookieName); err == nil {
		token = cookie.Value
	}

	session, err := h.svc.Refresh(r.Context(), token)
	switch {
	case errors.Is(err, ErrRefreshTokenReused):
		h.logger.WarnContext(r.Context(), "refresh token reuse detected; session revoked", slog.Int64("user_id", session.UserID))
		fallthrough
	case errors.Is(err, ErrInvalidRefreshToken):
		h.clearRefreshCookie(w)
		http.Error(w, "Invalid or expired refresh token", http.StatusUnauthorized)
		return
	case err != nil:
		h.serverError(r, "refresh", err)
		http.Error(w, "Failed to refresh token", http.StatusInternalServerError)
		return
	}

	h.setRefreshCookie(w, session.RefreshToken)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id":    session.UserID,
		"token":      session.AccessToken,
		"expires_in": int(h.svc.AccessTokenTTL().Seconds()),
	})
}

// Logout ends the session in the refresh cookie (if any) and clears it.
// It always succeeds, so it is safe to call more than once.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(RefreshCookieName); err == nil {
		if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
			h.serverError(r, "logout: revoke session", err)
		}
	}

	h.clearRefreshCookie(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Logout successful",
	})
}

// ForgotPassword always answers 200 with the same message. When the account
// exists a reset link is emailed.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	token, err := h.svc.ForgotPassword(r.Context(), req.Email)
	if err != nil {
		// Logged, not returned: errors only happen for existing accounts, so
		// reporting them would reveal that the account exists.
		h.serverError(r, "forgot password", err)
	}

	response := map[string]any{"message": forgotPasswordMessage}
	if token != "" && h.settings.ReturnResetTokenInResponse {
		response["reset_token"] = token
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword)
	switch {
	case errors.Is(err, ErrPasswordLength):
		http.Error(w, "Password must be 8 to 72 characters long", http.StatusBadRequest)
		return
	case errors.Is(err, ErrInvalidResetToken):
		http.Error(w, "Invalid or expired reset token", http.StatusBadRequest)
		return
	case err != nil:
		h.serverError(r, "reset password", err)
		http.Error(w, "Password reset failed", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Password reset successful",
	})
}

// Profile is a placeholder protected endpoint.
func (h *Handler) Profile(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "You accessed a protected API successfully",
	})
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token RefreshToken) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure comes from config (off only for local HTTP); HttpOnly and SameSite are set
		Name:     RefreshCookieName,
		Value:    token.Token,
		Path:     refreshCookiePath,
		Expires:  token.ExpiresAt,
		MaxAge:   int(time.Until(token.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   h.settings.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: as above
		Name:     RefreshCookieName,
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.settings.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) serverError(r *http.Request, action string, err error) {
	h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
}

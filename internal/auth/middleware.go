package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// AccessLoader loads a signed-in user's current role and permissions.
type AccessLoader interface {
	Access(ctx context.Context, userID, orgID int64) (principal.Principal, error)
}

// RequireUser rejects requests without a valid "Authorization: Bearer <JWT>"
// header with the legacy plain-text 401s, and puts the caller in the request
// context as a principal.Principal with their role's current permissions.
func RequireUser(tokens *JWT, access AccessLoader) func(http.Handler) http.Handler {
	return requireUser(tokens, access, func(w http.ResponseWriter, _ *http.Request, status int, msg string) {
		http.Error(w, msg, status)
	})
}

// RequireUserV1 is RequireUser for /api/v1, answering with problem+json.
func RequireUserV1(tokens *JWT, access AccessLoader) func(http.Handler) http.Handler {
	return requireUser(tokens, access, func(w http.ResponseWriter, r *http.Request, status int, msg string) {
		if status == http.StatusUnauthorized {
			httpx.WriteProblem(w, r, status, "unauthorized", msg)
			return
		}
		httpx.WriteProblem(w, r, status, "internal", msg)
	})
}

func requireUser(tokens *JWT, access AccessLoader, reject func(http.ResponseWriter, *http.Request, int, string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, orgID, msg := authenticate(tokens, r.Header.Get("Authorization"))
			if msg != "" {
				reject(w, r, http.StatusUnauthorized, msg)
				return
			}
			p, err := access.Access(r.Context(), userID, orgID)
			if errors.Is(err, ErrUserNotFound) {
				reject(w, r, http.StatusUnauthorized, "Invalid or expired token")
				return
			}
			if err != nil {
				reject(w, r, http.StatusInternalServerError, "Something went wrong.")
				return
			}
			next.ServeHTTP(w, r.WithContext(principal.With(r.Context(), p)))
		})
	}
}

// authenticate validates an Authorization header, returning the caller's
// user and org or the reason it was rejected.
func authenticate(tokens *JWT, header string) (userID, orgID int64, msg string) {
	if header == "" {
		return 0, 0, "Authorization token required"
	}

	parts := strings.Split(header, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return 0, 0, "Invalid authorization format"
	}

	token, err := tokens.Validate(parts[1])
	if err != nil || !token.Valid {
		return 0, 0, "Invalid or expired token"
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, 0, "Invalid token claims"
	}
	raw, ok := claims["user_id"]
	if !ok {
		return 0, 0, "User ID not found in token"
	}
	uid, ok := raw.(float64)
	if !ok {
		return 0, 0, "Invalid user ID in token"
	}
	// Tokens issued before multi-tenancy carry no org and are rejected, so
	// their holders sign in again. The role is not taken from the token: it
	// is loaded for every request.
	org, okOrg := claims["org_id"].(float64)
	if !okOrg || org <= 0 {
		return 0, 0, "Invalid token claims"
	}
	return int64(uid), int64(org), ""
}

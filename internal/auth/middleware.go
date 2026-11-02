package auth

import (
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// RequireUser rejects requests without a valid "Authorization: Bearer <JWT>"
// header with the legacy plain-text 401s, and puts the caller in the request
// context as a principal.Principal.
func RequireUser(tokens *JWT) func(http.Handler) http.Handler {
	return requireUser(tokens, func(w http.ResponseWriter, _ *http.Request, msg string) {
		http.Error(w, msg, http.StatusUnauthorized)
	})
}

// RequireUserV1 is RequireUser for /api/v1, answering with problem+json.
func RequireUserV1(tokens *JWT) func(http.Handler) http.Handler {
	return requireUser(tokens, func(w http.ResponseWriter, r *http.Request, msg string) {
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "unauthorized", msg)
	})
}

func requireUser(tokens *JWT, reject func(http.ResponseWriter, *http.Request, string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, msg := authenticate(tokens, r.Header.Get("Authorization"))
			if msg != "" {
				reject(w, r, msg)
				return
			}
			next.ServeHTTP(w, r.WithContext(principal.With(r.Context(), p)))
		})
	}
}

// authenticate validates an Authorization header, returning the caller or
// the reason it was rejected.
func authenticate(tokens *JWT, header string) (principal.Principal, string) {
	if header == "" {
		return principal.Principal{}, "Authorization token required"
	}

	parts := strings.Split(header, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return principal.Principal{}, "Invalid authorization format"
	}

	token, err := tokens.Validate(parts[1])
	if err != nil || !token.Valid {
		return principal.Principal{}, "Invalid or expired token"
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return principal.Principal{}, "Invalid token claims"
	}
	raw, ok := claims["user_id"]
	if !ok {
		return principal.Principal{}, "User ID not found in token"
	}
	userID, ok := raw.(float64)
	if !ok {
		return principal.Principal{}, "Invalid user ID in token"
	}
	// Tokens issued before multi-tenancy carry no org or role and are
	// rejected, so their holders sign in again.
	orgID, okOrg := claims["org_id"].(float64)
	role, okRole := claims["role"].(string)
	if !okOrg || !okRole || orgID <= 0 || !principal.Role(role).Valid() {
		return principal.Principal{}, "Invalid token claims"
	}

	return principal.Principal{
		UserID: int64(userID),
		OrgID:  int64(orgID),
		Role:   principal.Role(role),
	}, ""
}

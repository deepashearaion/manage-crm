package auth

import (
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// RequireUser rejects requests without a valid "Authorization: Bearer <JWT>"
// header (plain-text 401s, as the legacy API did) and puts the caller in the
// request context as a principal.Principal.
func RequireUser(tokens *JWT) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				http.Error(w, "Authorization token required", http.StatusUnauthorized)
				return
			}

			parts := strings.Split(header, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				http.Error(w, "Invalid authorization format", http.StatusUnauthorized)
				return
			}

			token, err := tokens.Validate(parts[1])
			if err != nil || !token.Valid {
				http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				http.Error(w, "Invalid token claims", http.StatusUnauthorized)
				return
			}
			raw, ok := claims["user_id"]
			if !ok {
				http.Error(w, "User ID not found in token", http.StatusUnauthorized)
				return
			}
			userID, ok := raw.(float64)
			if !ok {
				http.Error(w, "Invalid user ID in token", http.StatusUnauthorized)
				return
			}

			ctx := principal.With(r.Context(), principal.Principal{UserID: int64(userID)})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

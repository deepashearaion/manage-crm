package middleware

import (
	"net/http"
	"strings"

	"authentication-backend/utils"
)

func JWTValidation(next http.HandlerFunc) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		authHeader := r.Header.Get("Authorization")

		if authHeader == "" {
			http.Error(
				w,
				"Authorization token required",
				http.StatusUnauthorized,
			)
			return
		}

		parts := strings.Split(authHeader, " ")

		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(
				w,
				"Invalid authorization format",
				http.StatusUnauthorized,
			)
			return
		}

		tokenString := parts[1]

		token, err := utils.ValidateJWT(tokenString)

		if err != nil || !token.Valid {
			http.Error(
				w,
				"Invalid or expired token",
				http.StatusUnauthorized,
			)
			return
		}

		next.ServeHTTP(w, r)
	}
}

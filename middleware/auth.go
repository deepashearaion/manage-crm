package middleware

import (
	"context"
	"net/http"
	"strings"

	"authentication-backend/utils"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "userID"

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

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(
				w,
				"Invalid token claims",
				http.StatusUnauthorized,
			)
			return
		}

		userIDValue, ok := claims["user_id"]
		if !ok {
			http.Error(
				w,
				"User ID not found in token",
				http.StatusUnauthorized,
			)
			return
		}

		userIDFloat, ok := userIDValue.(float64)
		if !ok {
			http.Error(
				w,
				"Invalid user ID in token",
				http.StatusUnauthorized,
			)
			return
		}

		userID := int64(userIDFloat)

		ctx := context.WithValue(
			r.Context(),
			UserIDKey,
			userID,
		)

		r = r.WithContext(ctx)

		next.ServeHTTP(w, r)
	}
}

package middleware

import (
	"net/http"
	"os"
	"strings"
)

// CORS allows the frontend (CORS_ALLOWED_ORIGINS, comma-separated) to call the API
// directly from the browser and answers preflight requests.
func CORS(next http.Handler) http.Handler {

	allowed := os.Getenv("CORS_ALLOWED_ORIGINS")

	if allowed == "" {
		allowed = "http://localhost:5173"
	}

	origins := map[string]bool{}

	for _, origin := range strings.Split(allowed, ",") {
		origins[strings.TrimSpace(origin)] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		origin := r.Header.Get("Origin")

		if origins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

package middleware

import (
	"net/http"
)

// CORS allows the given frontend origins to call the API directly from the
// browser and answers preflight requests.
func CORS(allowedOrigins []string, next http.Handler) http.Handler {

	origins := map[string]bool{}

	for _, origin := range allowedOrigins {
		origins[origin] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		origin := r.Header.Get("Origin")

		if origins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			// Lets the browser send the refresh-token cookie with credentials: "include".
			w.Header().Set("Access-Control-Allow-Credentials", "true")
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

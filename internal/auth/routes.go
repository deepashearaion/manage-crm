package auth

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the auth endpoints. Profile requires a signed-in user;
// the others are public.
func Routes(r *mux.Router, h *Handler, requireUser func(http.Handler) http.Handler) {
	r.HandleFunc("/api/auth/register", h.Register).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/login", h.Login).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/logout", h.Logout).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/refresh", h.Refresh).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/forgot-password", h.ForgotPassword).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/reset-password", h.ResetPassword).Methods(http.MethodPost)

	r.Handle("/api/profile", requireUser(http.HandlerFunc(h.Profile))).Methods(http.MethodGet)
}

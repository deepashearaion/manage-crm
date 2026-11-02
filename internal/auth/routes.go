package auth

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the auth endpoints. The /api/auth routes are public;
// /api/profile and /api/v1/users need a signed-in user (legacy plain-text
// and problem+json 401s respectively).
func Routes(r *mux.Router, h *Handler, tokens *JWT) {
	r.HandleFunc("/api/auth/register", h.Register).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/login", h.Login).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/logout", h.Logout).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/refresh", h.Refresh).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/forgot-password", h.ForgotPassword).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/reset-password", h.ResetPassword).Methods(http.MethodPost)

	r.Handle("/api/profile", RequireUser(tokens)(http.HandlerFunc(h.Profile))).Methods(http.MethodGet)

	v1 := RequireUserV1(tokens)
	r.Handle("/api/v1/users", v1(http.HandlerFunc(h.ListUsers))).Methods(http.MethodGet)
	r.Handle("/api/v1/users", v1(http.HandlerFunc(h.InviteUser))).Methods(http.MethodPost)
	r.Handle("/api/v1/users/{id}", v1(http.HandlerFunc(h.UpdateUser))).Methods(http.MethodPatch)
}

package auth

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the auth endpoints. The /api/auth routes are public;
// /api/profile and the /api/v1 user and role routes need a signed-in user
// (legacy plain-text and problem+json 401s respectively).
func Routes(r *mux.Router, h *Handler, requireUser, requireUserV1 func(http.Handler) http.Handler) {
	r.HandleFunc("/api/auth/register", h.Register).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/login", h.Login).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/logout", h.Logout).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/refresh", h.Refresh).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/forgot-password", h.ForgotPassword).Methods(http.MethodPost)
	r.HandleFunc("/api/auth/reset-password", h.ResetPassword).Methods(http.MethodPost)

	r.Handle("/api/profile", requireUser(http.HandlerFunc(h.Profile))).Methods(http.MethodGet)

	v1 := func(path, method string, f http.HandlerFunc) { r.Handle(path, requireUserV1(f)).Methods(method) }
	v1("/api/v1/me", http.MethodGet, h.Me)
	v1("/api/v1/permissions", http.MethodGet, h.Catalog)

	v1("/api/v1/users", http.MethodGet, h.ListUsers)
	v1("/api/v1/users", http.MethodPost, h.CreateUser)
	v1("/api/v1/users/{id}", http.MethodGet, h.GetUser)
	v1("/api/v1/users/{id}", http.MethodPatch, h.UpdateUser)
	v1("/api/v1/users/{id}/role", http.MethodPut, h.AssignRole)
	v1("/api/v1/users/{id}/password", http.MethodPut, h.SetPassword)

	v1("/api/v1/roles", http.MethodGet, h.ListRoles)
	v1("/api/v1/roles", http.MethodPost, h.CreateRole)
	v1("/api/v1/roles/{id}", http.MethodGet, h.GetRole)
	v1("/api/v1/roles/{id}", http.MethodPatch, h.UpdateRole)
	v1("/api/v1/roles/{id}", http.MethodDelete, h.DeleteRole)
}

package contacts

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the contact endpoints; every one requires a signed-in user.
func Routes(r *mux.Router, h *Handler, requireUser func(http.Handler) http.Handler) {
	r.Handle("/api/contacts", requireUser(http.HandlerFunc(h.List))).Methods(http.MethodGet)
	r.Handle("/api/contacts", requireUser(http.HandlerFunc(h.Create))).Methods(http.MethodPost)
	r.Handle("/api/contacts/{id}", requireUser(http.HandlerFunc(h.Get))).Methods(http.MethodGet)
	r.Handle("/api/contacts/{id}", requireUser(http.HandlerFunc(h.Update))).Methods(http.MethodPatch)
	r.Handle("/api/contacts/{id}", requireUser(http.HandlerFunc(h.Delete))).Methods(http.MethodDelete)
}

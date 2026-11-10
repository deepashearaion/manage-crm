package contacts

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the contact endpoints; every one requires a signed-in
// user. requireUser guards the legacy /api routes (plain-text 401s) and
// requireUserV1 the /api/v1 routes (problem+json).
func Routes(r *mux.Router, h *Handler, requireUser, requireUserV1 func(http.Handler) http.Handler) {
	r.Handle("/api/contacts", requireUser(http.HandlerFunc(h.List))).Methods(http.MethodGet)
	r.Handle("/api/contacts", requireUser(http.HandlerFunc(h.Create))).Methods(http.MethodPost)
	r.Handle("/api/contacts/{id}", requireUser(http.HandlerFunc(h.Get))).Methods(http.MethodGet)
	r.Handle("/api/contacts/{id}", requireUser(http.HandlerFunc(h.Update))).Methods(http.MethodPatch)
	r.Handle("/api/contacts/{id}", requireUser(http.HandlerFunc(h.Delete))).Methods(http.MethodDelete)

	v1 := requireUserV1
	r.Handle("/api/v1/contacts", v1(http.HandlerFunc(h.ListV1))).Methods(http.MethodGet)
	r.Handle("/api/v1/contacts/{id}", v1(http.HandlerFunc(h.GetV1))).Methods(http.MethodGet)
	r.Handle("/api/v1/contacts/{id}/timeline", v1(http.HandlerFunc(h.Timeline))).Methods(http.MethodGet)
	r.Handle("/api/v1/contacts/{id}/activities", v1(http.HandlerFunc(h.LogActivity))).Methods(http.MethodPost)
	r.Handle("/api/v1/contacts/{id}/lifecycle-stage", v1(http.HandlerFunc(h.SetStage))).Methods(http.MethodPut)
}

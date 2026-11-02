package reports

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the report endpoints; every one requires a signed-in user.
func Routes(r *mux.Router, h *Handler, requireUser func(http.Handler) http.Handler) {
	r.Handle("/api/dashboard/sales", requireUser(http.HandlerFunc(h.SalesDashboard))).Methods(http.MethodGet)
}

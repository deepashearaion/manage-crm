package reports

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the report endpoints; every one requires a signed-in
// user. requireUser guards the legacy dashboard (plain-text 401s) and
// requireUserV1 the /api/v1 reports.
func Routes(r *mux.Router, h *Handler, requireUser, requireUserV1 func(http.Handler) http.Handler) {
	r.Handle("/api/dashboard/sales", requireUser(http.HandlerFunc(h.SalesDashboard))).Methods(http.MethodGet)

	v1 := func(path, method string, f http.HandlerFunc) { r.Handle(path, requireUserV1(f)).Methods(method) }
	v1("/api/v1/reports/dashboard", http.MethodGet, h.Dashboard)
	v1("/api/v1/reports/funnel", http.MethodGet, h.Funnel)
	v1("/api/v1/reports/pipeline", http.MethodGet, h.Pipeline)
	v1("/api/v1/reports/sources", http.MethodGet, h.Sources)
	v1("/api/v1/reports/reps", http.MethodGet, h.Reps)
	v1("/api/v1/reports/revenue", http.MethodGet, h.Revenue)
	v1("/api/v1/reports/settings", http.MethodGet, h.GetSettings)
	v1("/api/v1/reports/settings", http.MethodPut, h.SaveSettings)
}

package reports

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

type Handler struct {
	svc    *Service
	logger *slog.Logger
}

func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// SalesDashboard is the legacy GET /api/dashboard/sales (plain-text errors).
func (h *Handler) SalesDashboard(w http.ResponseWriter, r *http.Request) {
	p, ok := principal.From(r.Context())
	if !ok {
		http.Error(w, "User authentication information not found", http.StatusUnauthorized)
		return
	}

	d, err := h.svc.SalesDashboard(r.Context(), p)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "sales dashboard", slog.Any("error", err))
		http.Error(w, "Failed to load sales dashboard", http.StatusInternalServerError)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// period reads ?from=&to=.
func period(r *http.Request) (string, string) {
	q := r.URL.Query()
	return q.Get("from"), q.Get("to")
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	from, to := period(r)
	d, err := h.svc.Dashboard(r.Context(), p, from, to)
	h.respond(w, r, "dashboard", "dashboard", d, err)
}

func (h *Handler) Funnel(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	from, to := period(r)
	f, err := h.svc.Funnel(r.Context(), p, from, to)
	h.respond(w, r, "funnel", "funnel", f, err)
}

func (h *Handler) Pipeline(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	list, err := h.svc.Pipeline(r.Context(), p)
	h.respond(w, r, "pipeline report", "stages", list, err)
}

func (h *Handler) Sources(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	from, to := period(r)
	list, err := h.svc.Sources(r.Context(), p, from, to)
	h.respond(w, r, "sources report", "sources", list, err)
}

func (h *Handler) Reps(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	from, to := period(r)
	list, err := h.svc.Reps(r.Context(), p, from, to)
	h.respond(w, r, "reps report", "reps", list, err)
}

// Revenue handles GET /api/v1/reports/revenue?months=12.
func (h *Handler) Revenue(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	months := 0
	if raw := r.URL.Query().Get("months"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "months must be a number.")
			return
		}
		months = n
	}
	list, err := h.svc.Revenue(r.Context(), p, months)
	h.respond(w, r, "revenue report", "months", list, err)
}

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	s, err := h.svc.Settings(r.Context(), p)
	h.respond(w, r, "report settings", "settings", s, err)
}

func (h *Handler) SaveSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var req struct {
		WeeklyEmail bool `json:"weekly_email"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, err := h.svc.SaveSettings(r.Context(), p, req.WeeklyEmail)
	h.respond(w, r, "save report settings", "settings", s, err)
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, action, key string, v any, err error) {
	var fe *FieldError
	switch {
	case err == nil:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{key: v})
	case errors.As(err, &fe):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", fe.Message,
			map[string]string{fe.Field: fe.Problem})
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	default:
		h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

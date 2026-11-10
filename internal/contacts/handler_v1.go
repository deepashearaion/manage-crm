package contacts

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// ListV1 handles GET /api/v1/contacts?temperature=&lifecycle_stage=&limit=&before_id=.
func (h *Handler) ListV1(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	q := r.URL.Query()
	limit, okLimit := queryInt(q.Get("limit"))
	before, okBefore := queryInt(q.Get("before_id"))
	if !okLimit || !okBefore {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "limit and before_id must be positive integers.")
		return
	}
	list, err := h.svc.ListViews(r.Context(), p, ListFilter{
		Temperature: q.Get("temperature"),
		Stage:       q.Get("lifecycle_stage"),
		Limit:       int(limit),
		BeforeID:    before,
	})
	if err != nil {
		h.v1Error(w, r, "list contacts", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contacts": list})
}

// GetV1 handles GET /api/v1/contacts/{id}.
func (h *Handler) GetV1(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := v1ID(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Visible(r.Context(), p, id)
	if err != nil {
		h.v1Error(w, r, "get contact", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contact": c})
}

// Timeline handles GET /api/v1/contacts/{id}/timeline?limit=&before_id=.
func (h *Handler) Timeline(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := v1ID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, okLimit := queryInt(q.Get("limit"))
	before, okBefore := queryInt(q.Get("before_id"))
	if !okLimit || !okBefore {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "limit and before_id must be positive integers.")
		return
	}
	list, err := h.svc.Timeline(r.Context(), p, id, int(limit), before)
	if err != nil {
		h.v1Error(w, r, "contact timeline", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"activities": list})
}

// LogActivity handles POST /api/v1/contacts/{id}/activities.
func (h *Handler) LogActivity(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := v1ID(w, r)
	if !ok {
		return
	}
	var in ActivityInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	a, err := h.svc.LogActivity(r.Context(), p, id, in)
	if err != nil {
		h.v1Error(w, r, "log activity", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"activity": a})
}

// SetStage handles PUT /api/v1/contacts/{id}/lifecycle-stage.
func (h *Handler) SetStage(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := v1ID(w, r)
	if !ok {
		return
	}
	var req struct {
		Stage string `json:"lifecycle_stage"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	c, err := h.svc.SetStage(r.Context(), p, id, req.Stage)
	if err != nil {
		h.v1Error(w, r, "set lifecycle stage", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"contact": c})
}

func (h *Handler) v1Error(w http.ResponseWriter, r *http.Request, action string, err error) {
	invalid := func(field, msg string) {
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", msg,
			map[string]string{field: "invalid"})
	}
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such contact.")
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrInvalidStage):
		invalid("lifecycle_stage", "Lifecycle stage must be lead, mql, sql, opportunity, customer or lost.")
	case errors.Is(err, ErrInvalidTemperature):
		invalid("temperature", "Temperature must be cold, warm or hot.")
	case errors.Is(err, ErrInvalidKind):
		invalid("kind", "Kind must be note, call or email.")
	case errors.Is(err, ErrInvalidDirection):
		invalid("direction", "Direction must be inbound or outbound.")
	case errors.Is(err, ErrEmptyBody):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "Body is required.",
			map[string]string{"body": "required"})
	default:
		h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

// v1ID parses the {id} path variable; a malformed id cannot name a contact,
// so it is answered 404.
func v1ID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such contact.")
		return 0, false
	}
	return id, true
}

// queryInt parses an optional positive integer query parameter ("" is 0).
func queryInt(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0
}

package tasks

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

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

// List handles GET /api/v1/tasks?status=&assignee_id=&contact_id=&overdue=true&limit=&offset=.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	q := r.URL.Query()
	var f Filter
	ok := true
	num := func(key string) int64 {
		raw := q.Get(key)
		if raw == "" {
			return 0
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			ok = false
		}
		return n
	}
	f.Status = q.Get("status")
	f.AssigneeID = num("assignee_id")
	f.ContactID = num("contact_id")
	f.Limit = int(num("limit"))
	f.Offset = int(num("offset"))
	f.Overdue = q.Get("overdue") == "true"
	if !ok {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "assignee_id, contact_id, limit and offset must be non-negative integers.")
		return
	}
	list, err := h.svc.List(r.Context(), p, f)
	h.respond(w, r, "list tasks", http.StatusOK, "tasks", list, err)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := h.svc.Get(r.Context(), p, id)
	h.respond(w, r, "get task", http.StatusOK, "task", t, err)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	t, err := h.svc.Create(r.Context(), p, in)
	h.respond(w, r, "create task", http.StatusCreated, "task", t, err)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var patch Patch
	if !httpx.DecodeJSON(w, r, &patch) {
		return
	}
	t, err := h.svc.Update(r.Context(), p, id, patch)
	h.respond(w, r, "update task", http.StatusOK, "task", t, err)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), p, id); err != nil {
		h.writeError(w, r, "delete task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, action string, status int, key string, v any, err error) {
	if err != nil {
		h.writeError(w, r, action, err)
		return
	}
	httpx.WriteJSON(w, status, map[string]any{key: v})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, action string, err error) {
	var fe *FieldError
	switch {
	case errors.As(err, &fe):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", fe.Message,
			map[string]string{fe.Field: fe.Problem})
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such task.")
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrContactNotFound):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "No such contact.",
			map[string]string{"contact_id": "invalid"})
	case errors.Is(err, ErrInvalidAssignee):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "That user is not in your organization.",
			map[string]string{"assignee_id": "invalid"})
	default:
		h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such task.")
		return 0, false
	}
	return id, true
}

// Routes registers the task endpoints; all need a signed-in user.
func Routes(r *mux.Router, h *Handler, requireUserV1 func(http.Handler) http.Handler) {
	r.Handle("/api/v1/tasks", requireUserV1(http.HandlerFunc(h.List))).Methods(http.MethodGet)
	r.Handle("/api/v1/tasks", requireUserV1(http.HandlerFunc(h.Create))).Methods(http.MethodPost)
	r.Handle("/api/v1/tasks/{id}", requireUserV1(http.HandlerFunc(h.Get))).Methods(http.MethodGet)
	r.Handle("/api/v1/tasks/{id}", requireUserV1(http.HandlerFunc(h.Update))).Methods(http.MethodPatch)
	r.Handle("/api/v1/tasks/{id}", requireUserV1(http.HandlerFunc(h.Delete))).Methods(http.MethodDelete)
}

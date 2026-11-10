package nurture

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

func (h *Handler) ListSequences(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	list, err := h.svc.Sequences(r.Context(), p)
	h.respond(w, r, "list sequences", http.StatusOK, "sequences", list, err)
}

func (h *Handler) GetSequence(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		seq, err := h.svc.Sequence(r.Context(), p, id)
		h.respond(w, r, "get sequence", http.StatusOK, "sequence", seq, err)
	}
}

func (h *Handler) CreateSequence(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in SequenceInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	seq, err := h.svc.CreateSequence(r.Context(), p, in)
	h.respond(w, r, "create sequence", http.StatusCreated, "sequence", seq, err)
}

func (h *Handler) UpdateSequence(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in SequenceInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	seq, err := h.svc.UpdateSequence(r.Context(), p, id, in)
	h.respond(w, r, "update sequence", http.StatusOK, "sequence", seq, err)
}

func (h *Handler) DeleteSequence(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		if err := h.svc.DeleteSequence(r.Context(), p, id); err != nil {
			h.writeError(w, r, "delete sequence", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Enroll handles POST /api/v1/nurture-sequences/{id}/enrollments {contact_id}.
func (h *Handler) Enroll(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ContactID int64 `json:"contact_id"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	e, err := h.svc.Enroll(r.Context(), p, id, req.ContactID)
	h.respond(w, r, "enroll", http.StatusCreated, "enrollment", e, err)
}

// ListEnrollments handles GET /api/v1/nurture-enrollments?contact_id=&sequence_id=&status=&limit=&offset=.
func (h *Handler) ListEnrollments(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	q := r.URL.Query()
	var n [4]int64
	for i, k := range []string{"contact_id", "sequence_id", "limit", "offset"} {
		if raw := q.Get(k); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v < 0 {
				httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", k+" must be a non-negative integer.")
				return
			}
			n[i] = v
		}
	}
	list, err := h.svc.Enrollments(r.Context(), p, n[0], n[1], q.Get("status"), int(n[2]), int(n[3]))
	h.respond(w, r, "list enrollments", http.StatusOK, "enrollments", list, err)
}

// StopEnrollment handles POST /api/v1/nurture-enrollments/{id}/stop.
func (h *Handler) StopEnrollment(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		e, err := h.svc.Stop(r.Context(), p, id)
		h.respond(w, r, "stop enrollment", http.StatusOK, "enrollment", e, err)
	}
}

// Unsubscribe handles the public POST /api/v1/unsubscribe {token}, called
// by the frontend's /unsubscribe page with the token from an email link.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.svc.Unsubscribe(r.Context(), req.Token); err != nil {
		h.writeError(w, r, "unsubscribe", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
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
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrContactNotFound):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "No such contact.",
			map[string]string{"contact_id": "invalid"})
	case errors.Is(err, ErrNameTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "name_taken", "A sequence with that name exists.",
			map[string]string{"name": "taken"})
	case errors.Is(err, ErrTriggerTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "trigger_taken",
			"Another active sequence already has this trigger; deactivate it first.", map[string]string{"trigger": "taken"})
	case errors.Is(err, ErrAlreadyEnrolled):
		httpx.WriteProblem(w, r, http.StatusConflict, "already_enrolled", "This contact is already in the sequence.")
	case errors.Is(err, ErrInvalidToken):
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_token", "This unsubscribe link is not valid.")
	default:
		h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "Not found.")
		return 0, false
	}
	return id, true
}

// Routes registers the nurture endpoints. /api/v1/unsubscribe is public
// (the signed token identifies the contact); the rest need a signed-in user.
func Routes(r *mux.Router, h *Handler, requireUserV1 func(http.Handler) http.Handler) {
	r.HandleFunc("/api/v1/unsubscribe", h.Unsubscribe).Methods(http.MethodPost)
	v1 := func(path, method string, f http.HandlerFunc) { r.Handle(path, requireUserV1(f)).Methods(method) }
	v1("/api/v1/nurture-sequences", http.MethodGet, h.ListSequences)
	v1("/api/v1/nurture-sequences", http.MethodPost, h.CreateSequence)
	v1("/api/v1/nurture-sequences/{id}", http.MethodGet, h.GetSequence)
	v1("/api/v1/nurture-sequences/{id}", http.MethodPatch, h.UpdateSequence)
	v1("/api/v1/nurture-sequences/{id}", http.MethodDelete, h.DeleteSequence)
	v1("/api/v1/nurture-sequences/{id}/enrollments", http.MethodPost, h.Enroll)
	v1("/api/v1/nurture-enrollments", http.MethodGet, h.ListEnrollments)
	v1("/api/v1/nurture-enrollments/{id}/stop", http.MethodPost, h.StopEnrollment)
}

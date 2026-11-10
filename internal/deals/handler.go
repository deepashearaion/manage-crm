package deals

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

// queryInts parses optional non-negative integer query parameters.
func queryInts(r *http.Request, keys ...string) ([]int64, bool) {
	out := make([]int64, len(keys))
	for i, k := range keys {
		raw := r.URL.Query().Get(k)
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// List handles GET /api/v1/deals?status=&stage_id=&owner_id=&contact_id=&limit=&offset=.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	n, ok := queryInts(r, "stage_id", "owner_id", "contact_id", "limit", "offset")
	if !ok {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "Ids, limit and offset must be non-negative integers.")
		return
	}
	list, err := h.svc.List(r.Context(), p, Filter{
		Status: r.URL.Query().Get("status"), StageID: n[0], OwnerID: n[1], ContactID: n[2], Limit: int(n[3]), Offset: int(n[4]),
	})
	h.respond(w, r, "list deals", http.StatusOK, "deals", list, err)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		d, err := h.svc.Get(r.Context(), p, id)
		h.respond(w, r, "get deal", http.StatusOK, "deal", d, err)
	}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	d, err := h.svc.Create(r.Context(), p, in)
	h.respond(w, r, "create deal", http.StatusCreated, "deal", d, err)
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
	d, err := h.svc.Update(r.Context(), p, id, patch)
	h.respond(w, r, "update deal", http.StatusOK, "deal", d, err)
}

// Move handles POST /api/v1/deals/{id}/stage {stage_id, lost_reason}.
func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		StageID    int64  `json:"stage_id"`
		LostReason string `json:"lost_reason"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	d, err := h.svc.Move(r.Context(), p, id, req.StageID, req.LostReason)
	h.respond(w, r, "move deal", http.StatusOK, "deal", d, err)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		h.noContent(w, r, "delete deal", h.svc.Delete(r.Context(), p, id))
	}
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		list, err := h.svc.History(r.Context(), p, id)
		h.respond(w, r, "deal history", http.StatusOK, "history", list, err)
	}
}

// Forecast handles GET /api/v1/deals/forecast?from=YYYY-MM&months=&owner_id=.
func (h *Handler) Forecast(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	n, ok := queryInts(r, "months", "owner_id")
	if !ok {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "months and owner_id must be non-negative integers.")
		return
	}
	f, err := h.svc.Forecast(r.Context(), p, r.URL.Query().Get("from"), int(n[0]), n[1])
	h.respond(w, r, "deal forecast", http.StatusOK, "forecast", f, err)
}

func (h *Handler) ListStages(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	list, err := h.svc.Stages(r.Context(), p)
	h.respond(w, r, "list stages", http.StatusOK, "stages", list, err)
}

func (h *Handler) CreateStage(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in StageInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	st, err := h.svc.CreateStage(r.Context(), p, in)
	h.respond(w, r, "create stage", http.StatusCreated, "stage", st, err)
}

func (h *Handler) UpdateStage(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in StageInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	st, err := h.svc.UpdateStage(r.Context(), p, id, in)
	h.respond(w, r, "update stage", http.StatusOK, "stage", st, err)
}

func (h *Handler) DeleteStage(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		h.noContent(w, r, "delete stage", h.svc.DeleteStage(r.Context(), p, id))
	}
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, action string, status int, key string, v any, err error) {
	if err != nil {
		h.writeError(w, r, action, err)
		return
	}
	httpx.WriteJSON(w, status, map[string]any{key: v})
}

func (h *Handler) noContent(w http.ResponseWriter, r *http.Request, action string, err error) {
	if err != nil {
		h.writeError(w, r, action, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, action string, err error) {
	var fe *FieldError
	var mf *MissingFieldsError
	switch {
	case errors.As(err, &fe):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", fe.Message,
			map[string]string{fe.Field: fe.Problem})
	case errors.As(err, &mf):
		fields := map[string]string{}
		for _, f := range mf.Fields {
			fields[f] = "required"
		}
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "stage_requirements",
			"The "+mf.Stage+" stage needs these fields filled first.", fields)
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such deal.")
	case errors.Is(err, ErrStageNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such stage.")
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrContactNotFound):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "No such contact.",
			map[string]string{"contact_id": "invalid"})
	case errors.Is(err, ErrInvalidOwner):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "That user is not in your organization.",
			map[string]string{"owner_id": "invalid"})
	case errors.Is(err, ErrStageInUse):
		httpx.WriteProblem(w, r, http.StatusConflict, "stage_in_use", "Move this stage's deals elsewhere before deleting it.")
	case errors.Is(err, ErrLastOutcomeStage):
		httpx.WriteProblem(w, r, http.StatusConflict, "last_stage_of_kind", "Keep at least one open, one won and one lost stage.")
	case errors.Is(err, ErrStageNameTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "name_taken", "A stage with that name exists.",
			map[string]string{"name": "taken"})
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

// Routes registers the deal endpoints; all need a signed-in user. Stage
// changes go through POST /deals/{id}/stage so the guidance is enforced.
func Routes(r *mux.Router, h *Handler, requireUserV1 func(http.Handler) http.Handler) {
	v1 := func(path, method string, f http.HandlerFunc) {
		r.Handle(path, requireUserV1(f)).Methods(method)
	}
	v1("/api/v1/deal-stages", http.MethodGet, h.ListStages)
	v1("/api/v1/deal-stages", http.MethodPost, h.CreateStage)
	v1("/api/v1/deal-stages/{id}", http.MethodPatch, h.UpdateStage)
	v1("/api/v1/deal-stages/{id}", http.MethodDelete, h.DeleteStage)

	v1("/api/v1/deals/forecast", http.MethodGet, h.Forecast)
	v1("/api/v1/deals", http.MethodGet, h.List)
	v1("/api/v1/deals", http.MethodPost, h.Create)
	v1("/api/v1/deals/{id}", http.MethodGet, h.Get)
	v1("/api/v1/deals/{id}", http.MethodPatch, h.Update)
	v1("/api/v1/deals/{id}", http.MethodDelete, h.Delete)
	v1("/api/v1/deals/{id}/stage", http.MethodPost, h.Move)
	v1("/api/v1/deals/{id}/history", http.MethodGet, h.History)
}

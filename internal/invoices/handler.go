package invoices

import (
	"bytes"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"time"

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

func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	pr, err := h.svc.Profile(r.Context(), p)
	h.respond(w, r, "get billing profile", http.StatusOK, "billing_profile", pr, err)
}

func (h *Handler) SaveProfile(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in ProfileInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	pr, err := h.svc.SaveProfile(r.Context(), p, in)
	h.respond(w, r, "save billing profile", http.StatusOK, "billing_profile", pr, err)
}

// List handles GET /api/v1/invoices?status=&contact_id=&deal_id=&limit=&offset=.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	q := r.URL.Query()
	var n [4]int64
	for i, k := range []string{"contact_id", "deal_id", "limit", "offset"} {
		if raw := q.Get(k); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v < 0 {
				httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", k+" must be a non-negative integer.")
				return
			}
			n[i] = v
		}
	}
	list, err := h.svc.List(r.Context(), p, Filter{Status: q.Get("status"), ContactID: n[0], DealID: n[1], Limit: int(n[2]), Offset: int(n[3])})
	h.respond(w, r, "list invoices", http.StatusOK, "invoices", list, err)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in Input
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	inv, err := h.svc.Create(r.Context(), p, in)
	h.respond(w, r, "create invoice", http.StatusCreated, "invoice", inv, err)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		inv, err := h.svc.Get(r.Context(), p, id)
		h.respond(w, r, "get invoice", http.StatusOK, "invoice", inv, err)
	}
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
	inv, err := h.svc.Update(r.Context(), p, id, patch)
	h.respond(w, r, "update invoice", http.StatusOK, "invoice", inv, err)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		if err := h.svc.Delete(r.Context(), p, id); err != nil {
			h.writeError(w, r, "delete invoice", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) Issue(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		inv, err := h.svc.Issue(r.Context(), p, id)
		h.respond(w, r, "issue invoice", http.StatusOK, "invoice", inv, err)
	}
}

func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := pathID(w, r); ok {
		inv, err := h.svc.Send(r.Context(), p, id)
		h.respond(w, r, "send invoice", http.StatusOK, "invoice", inv, err)
	}
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	inv, err := h.svc.Cancel(r.Context(), p, id, req.Reason)
	h.respond(w, r, "cancel invoice", http.StatusOK, "invoice", inv, err)
}

// PDF handles GET /api/v1/invoices/{id}/pdf.
func (h *Handler) PDF(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	pdf, name, err := h.svc.PDF(r.Context(), p, id)
	if err != nil {
		h.writeError(w, r, "invoice pdf", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(pdf))
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
	var ie *IncompleteError
	switch {
	case errors.As(err, &fe):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", fe.Message,
			map[string]string{fe.Field: fe.Problem})
	case errors.As(err, &ie):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "invoice_incomplete",
			"Fill in these details before issuing (billing_profile.* are in the billing profile).", ie.Fields)
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such invoice.")
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrContactNotFound):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "No such contact.",
			map[string]string{"contact_id": "invalid"})
	case errors.Is(err, ErrDealNotFound):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "No such deal.",
			map[string]string{"deal_id": "invalid"})
	case errors.Is(err, ErrDealInvoiced):
		httpx.WriteProblem(w, r, http.StatusConflict, "deal_invoiced", "This deal already has an invoice; cancel it to make another.")
	case errors.Is(err, ErrNotDraft):
		httpx.WriteProblem(w, r, http.StatusConflict, "not_draft", "Issued invoices cannot be changed or deleted; cancel instead.")
	case errors.Is(err, ErrNotIssued):
		httpx.WriteProblem(w, r, http.StatusConflict, "not_issued", "Only issued invoices can be sent or cancelled.")
	case errors.Is(err, ErrNoEmail):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "The invoice has no customer email.",
			map[string]string{"bill_to_email": "required"})
	default:
		h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such invoice.")
		return 0, false
	}
	return id, true
}

// Routes registers the invoice endpoints; all need a signed-in user.
func Routes(r *mux.Router, h *Handler, requireUserV1 func(http.Handler) http.Handler) {
	v1 := func(path, method string, f http.HandlerFunc) { r.Handle(path, requireUserV1(f)).Methods(method) }
	v1("/api/v1/billing-profile", http.MethodGet, h.GetProfile)
	v1("/api/v1/billing-profile", http.MethodPut, h.SaveProfile)
	v1("/api/v1/invoices", http.MethodGet, h.List)
	v1("/api/v1/invoices", http.MethodPost, h.Create)
	v1("/api/v1/invoices/{id}", http.MethodGet, h.Get)
	v1("/api/v1/invoices/{id}", http.MethodPatch, h.Update)
	v1("/api/v1/invoices/{id}", http.MethodDelete, h.Delete)
	v1("/api/v1/invoices/{id}/issue", http.MethodPost, h.Issue)
	v1("/api/v1/invoices/{id}/send", http.MethodPost, h.Send)
	v1("/api/v1/invoices/{id}/cancel", http.MethodPost, h.Cancel)
	v1("/api/v1/invoices/{id}/pdf", http.MethodGet, h.PDF)
}

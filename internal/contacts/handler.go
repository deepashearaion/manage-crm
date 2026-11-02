package contacts

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// Handler serves the legacy /api/contacts routes, whose errors are plain
// text (http.Error) with the messages the frontend already shows.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, ok := caller(w, r)
	if !ok {
		return
	}

	var c Contact
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	c, err := h.svc.Create(r.Context(), p, c)
	if h.writeError(w, r, "create contact", "Failed to create contact", err) {
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"message": "Contact created successfully",
		"contact": c,
	})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, ok := caller(w, r)
	if !ok {
		return
	}

	list, err := h.svc.List(r.Context(), p)
	if h.writeError(w, r, "list contacts", "Failed to fetch contacts", err) {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"contacts": list,
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, ok := caller(w, r)
	if !ok {
		return
	}
	id, ok := contactID(w, r)
	if !ok {
		return
	}

	c, err := h.svc.Get(r.Context(), p, id)
	if h.writeError(w, r, "get contact", "Failed to fetch contact", err) {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"contact": c,
	})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	p, ok := caller(w, r)
	if !ok {
		return
	}
	id, ok := contactID(w, r)
	if !ok {
		return
	}

	var patch Patch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	c, err := h.svc.Update(r.Context(), p, id, patch)
	if h.writeError(w, r, "update contact", "Failed to update contact", err) {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Contact updated successfully",
		"contact": c,
	})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p, ok := caller(w, r)
	if !ok {
		return
	}
	id, ok := contactID(w, r)
	if !ok {
		return
	}

	err := h.svc.Delete(r.Context(), p, id)
	if h.writeError(w, r, "delete contact", "Failed to delete contact", err) {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Contact deleted successfully",
	})
}

// writeError answers err as a plain-text error and reports whether it did;
// unexpected errors are logged and answered with failure.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, action, failure string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrFirstNameRequired):
		http.Error(w, "First name is required", http.StatusBadRequest)
	case errors.Is(err, ErrInvalidOwner):
		http.Error(w, "Invalid lead owner", http.StatusBadRequest)
	case errors.Is(err, ErrForbidden):
		http.Error(w, "You do not have permission to perform this action", http.StatusForbidden)
	case errors.Is(err, ErrNotFound):
		http.Error(w, "Contact not found", http.StatusNotFound)
	default:
		h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
		http.Error(w, failure, http.StatusInternalServerError)
	}
	return true
}

func caller(w http.ResponseWriter, r *http.Request) (principal.Principal, bool) {
	p, ok := principal.From(r.Context())
	if !ok {
		http.Error(w, "User authentication information not found", http.StatusUnauthorized)
	}
	return p, ok
}

// contactID parses the {id} path variable, answering 400 when it is not a
// positive integer.
func contactID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid contact ID", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

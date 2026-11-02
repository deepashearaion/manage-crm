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
	p, ok := principal.From(r.Context())
	if !ok {
		http.Error(w, "User authentication information not found", http.StatusUnauthorized)
		return
	}

	var c Contact
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	c, err := h.svc.Create(r.Context(), p.UserID, c)
	switch {
	case errors.Is(err, ErrFirstNameRequired):
		http.Error(w, "First name is required", http.StatusBadRequest)
		return
	case err != nil:
		h.serverError(r, "create contact", err)
		http.Error(w, "Failed to create contact", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"message": "Contact created successfully",
		"contact": c,
	})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	if err != nil {
		h.serverError(r, "list contacts", err)
		http.Error(w, "Failed to fetch contacts", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"contacts": list,
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := contactID(w, r)
	if !ok {
		return
	}

	c, err := h.svc.Get(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "Contact not found", http.StatusNotFound)
		return
	case err != nil:
		h.serverError(r, "get contact", err)
		http.Error(w, "Failed to fetch contact", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"contact": c,
	})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := contactID(w, r)
	if !ok {
		return
	}

	var p Patch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	c, err := h.svc.Update(r.Context(), id, p)
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "Contact not found", http.StatusNotFound)
		return
	case err != nil:
		h.serverError(r, "update contact", err)
		http.Error(w, "Failed to update contact", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Contact updated successfully",
		"contact": c,
	})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := contactID(w, r)
	if !ok {
		return
	}

	err := h.svc.Delete(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "Contact not found", http.StatusNotFound)
		return
	case err != nil:
		h.serverError(r, "delete contact", err)
		http.Error(w, "Failed to delete contact", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Contact deleted successfully",
	})
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

func (h *Handler) serverError(r *http.Request, action string, err error) {
	h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
}

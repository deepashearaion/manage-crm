package auth

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// userView is a user as /api/v1 returns it (never the password hash).
type userView struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Email     string         `json:"email"`
	Role      principal.Role `json:"role"`
	CreatedAt time.Time      `json:"created_at"`
}

func viewOf(u User) userView {
	return userView{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt}
}

// ListUsers handles GET /api/v1/users.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	users, err := h.svc.ListUsers(r.Context(), p)
	if err != nil {
		h.v1Error(w, r, "list users", err)
		return
	}
	views := make([]userView, 0, len(users))
	for _, u := range users {
		views = append(views, viewOf(u))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": views})
}

// InviteUser handles POST /api/v1/users: create a user in the caller's org
// and email them a link to set their password.
func (h *Handler) InviteUser(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var req struct {
		Name  string         `json:"name"`
		Email string         `json:"email"`
		Role  principal.Role `json:"role"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}

	user, err := h.svc.InviteUser(r.Context(), p, req.Name, req.Email, req.Role)
	if errors.Is(err, ErrInviteNotSent) {
		// The user exists; the email can be resent, so report success.
		h.serverError(r, "invite user: send email", err)
		err = nil
	}
	if err != nil {
		h.v1Error(w, r, "invite user", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user": viewOf(user)})
}

// UpdateUser handles PATCH /api/v1/users/{id}; only the role can change.
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such user.")
		return
	}
	var req struct {
		Role principal.Role `json:"role"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}

	user, err := h.svc.ChangeRole(r.Context(), p, id, req.Role)
	if err != nil {
		h.v1Error(w, r, "change role", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": viewOf(user)})
}

// v1Error answers a service error as problem+json.
func (h *Handler) v1Error(w http.ResponseWriter, r *http.Request, action string, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrUserNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such user.")
	case errors.Is(err, ErrEmailTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "email_taken", "That email is already registered.",
			map[string]string{"email": "already registered"})
	case errors.Is(err, ErrLastOwner):
		httpx.WriteProblem(w, r, http.StatusConflict, "last_owner", "An organization must keep at least one owner.")
	case errors.Is(err, ErrMissingFields):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "Name and email are required.",
			map[string]string{"name": "required", "email": "required"})
	case errors.Is(err, ErrInvalidEmail):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "Invalid email address.",
			map[string]string{"email": "invalid"})
	case errors.Is(err, ErrInvalidRole):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "Role must be owner, admin, manager, rep or viewer.",
			map[string]string{"role": "invalid"})
	default:
		h.serverError(r, action, err)
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

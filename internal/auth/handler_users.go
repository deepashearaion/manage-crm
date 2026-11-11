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
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	RoleID    int64     `json:"role_id"`
	RoleName  string    `json:"role_name"`
	CreatedAt time.Time `json:"created_at"`
}

func viewOf(u User) userView {
	return userView{ID: u.ID, Name: u.Name, Email: u.Email, RoleID: u.RoleID, RoleName: u.RoleName, CreatedAt: u.CreatedAt}
}

// Me handles GET /api/v1/me: the caller, their role and every permission
// they have, so the frontend can show only what they may use.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	u, err := h.svc.Me(r.Context(), p)
	if err != nil {
		h.v1Error(w, r, "me", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":        viewOf(u),
		"role":        map[string]any{"id": u.RoleID, "name": u.RoleName, "locked": u.RoleOwner},
		"permissions": u.Principal().Effective(),
	})
}

// Catalog handles GET /api/v1/permissions: every module, action and scope a
// role can grant, for the role editor.
func (h *Handler) Catalog(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"modules": principal.Catalog})
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

// GetUser handles GET /api/v1/users/{id}.
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := userID(w, r)
	if !ok {
		return
	}
	u, err := h.svc.GetUser(r.Context(), p, id)
	h.userResult(w, r, "get user", http.StatusOK, u, err)
}

// CreateUser handles POST /api/v1/users {name, email, password, role_id}.
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in NewUser
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	u, err := h.svc.CreateUser(r.Context(), p, in)
	h.userResult(w, r, "create user", http.StatusCreated, u, err)
}

// UpdateUser handles PATCH /api/v1/users/{id} {name}.
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := userID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	u, err := h.svc.RenameUser(r.Context(), p, id, req.Name)
	h.userResult(w, r, "rename user", http.StatusOK, u, err)
}

// AssignRole handles PUT /api/v1/users/{id}/role {role_id}.
func (h *Handler) AssignRole(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := userID(w, r)
	if !ok {
		return
	}
	var req struct {
		RoleID int64 `json:"role_id"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	u, err := h.svc.AssignRole(r.Context(), p, id, req.RoleID)
	h.userResult(w, r, "assign role", http.StatusOK, u, err)
}

// SetPassword handles PUT /api/v1/users/{id}/password {password}.
func (h *Handler) SetPassword(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := userID(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.svc.SetUserPassword(r.Context(), p, id, req.Password); err != nil {
		h.v1Error(w, r, "set password", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) userResult(w http.ResponseWriter, r *http.Request, action string, status int, u User, err error) {
	if err != nil {
		h.v1Error(w, r, action, err)
		return
	}
	httpx.WriteJSON(w, status, map[string]any{"user": viewOf(u)})
}

// Roles.

func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	roles, err := h.svc.Roles(r.Context(), p)
	h.roleResult(w, r, "list roles", http.StatusOK, "roles", roles, err)
}

func (h *Handler) GetRole(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := roleID(w, r); ok {
		role, err := h.svc.Role(r.Context(), p, id)
		h.roleResult(w, r, "get role", http.StatusOK, "role", role, err)
	}
}

func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in RoleInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	role, err := h.svc.CreateRole(r.Context(), p, in)
	h.roleResult(w, r, "create role", http.StatusCreated, "role", role, err)
}

func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := roleID(w, r)
	if !ok {
		return
	}
	var in RoleInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	role, err := h.svc.UpdateRole(r.Context(), p, id, in)
	h.roleResult(w, r, "update role", http.StatusOK, "role", role, err)
}

func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if id, ok := roleID(w, r); ok {
		if err := h.svc.DeleteRole(r.Context(), p, id); err != nil {
			h.v1Error(w, r, "delete role", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) roleResult(w http.ResponseWriter, r *http.Request, action string, status int, key string, v any, err error) {
	if err != nil {
		h.v1Error(w, r, action, err)
		return
	}
	httpx.WriteJSON(w, status, map[string]any{key: v})
}

func pathID(w http.ResponseWriter, r *http.Request, detail string) (int64, bool) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", detail)
		return 0, false
	}
	return id, true
}

func userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return pathID(w, r, "No such user.")
}
func roleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return pathID(w, r, "No such role.")
}

// v1Error answers a service error as problem+json.
func (h *Handler) v1Error(w http.ResponseWriter, r *http.Request, action string, err error) {
	var fe *FieldError
	var ee *ExceedsError
	switch {
	case errors.As(err, &fe):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", fe.Message,
			map[string]string{fe.Field: fe.Problem})
	case errors.As(err, &ee):
		httpx.WriteProblem(w, r, http.StatusForbidden, "exceeds_own_access",
			"You cannot grant or change access beyond your own ("+ee.Permission+").")
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrUserNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such user.")
	case errors.Is(err, ErrRoleNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such role.")
	case errors.Is(err, ErrEmailTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "email_taken", "That email is already registered.",
			map[string]string{"email": "taken"})
	case errors.Is(err, ErrLastOwner):
		httpx.WriteProblem(w, r, http.StatusConflict, "last_owner", "An organization must keep at least one owner.")
	case errors.Is(err, ErrOwnRole):
		httpx.WriteProblem(w, r, http.StatusConflict, "own_role", "You cannot change your own role.")
	case errors.Is(err, ErrRoleLocked):
		httpx.WriteProblem(w, r, http.StatusConflict, "role_locked", "The Owner role cannot be changed or deleted.")
	case errors.Is(err, ErrRoleInUse):
		httpx.WriteProblem(w, r, http.StatusConflict, "role_in_use", "Move this role's users to another role before deleting it.")
	case errors.Is(err, ErrRoleNameTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "name_taken", "A role with that name exists.",
			map[string]string{"name": "taken"})
	case errors.Is(err, ErrInvalidEmail):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "Invalid email address.",
			map[string]string{"email": "invalid"})
	case errors.Is(err, ErrPasswordLength):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", "Password must be 8 to 72 characters long.",
			map[string]string{"password": "invalid"})
	default:
		h.serverError(r, action, err)
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

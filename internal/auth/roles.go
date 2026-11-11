package auth

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// Role is a named set of permissions in an org. The owner role is locked:
// it always has full access and cannot be changed or deleted.
type Role struct {
	ID          int64                 `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Locked      bool                  `json:"locked"` // the owner role
	Permissions principal.Permissions `json:"permissions"`
	UserCount   int                   `json:"user_count"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

// RoleInput creates or changes a role; nil fields are left as they are.
// Permissions replace the whole set; modules or actions left out are none.
type RoleInput struct {
	Name        *string               `json:"name"`
	Description *string               `json:"description"`
	Permissions principal.Permissions `json:"permissions"`
}

func scopes(view, create, edit, del principal.Scope) map[principal.Action]principal.Scope {
	return map[principal.Action]principal.Scope{
		principal.View: view, principal.Create: create, principal.Edit: edit, principal.Delete: del,
	}
}

// DefaultRoles are every new org's starting roles: the five fixed roles the
// CRM used to have, with the same access. Migration 00015 gives existing
// orgs the same roles; keep the two in step.
var DefaultRoles = func() []Role {
	const a, o, n = principal.All, principal.Own, principal.None
	manager := principal.FullAccess()
	manager[principal.Users] = map[principal.Action]principal.Scope{principal.View: a, principal.Create: n, principal.Edit: n}
	manager[principal.Roles] = scopes(a, n, n, n)
	return []Role{
		{Name: "Owner", Description: "Full access to everything. Cannot be changed or deleted.", Locked: true,
			Permissions: principal.FullAccess()},
		{Name: "Admin", Description: "Everything, including users and roles.", Permissions: principal.FullAccess()},
		{Name: "Manager", Description: "All records and settings; cannot manage users or roles.", Permissions: manager},
		{Name: "Rep", Description: "Works only on the records they own or are assigned.", Permissions: principal.Permissions{
			principal.Contacts: scopes(o, o, o, o),
			principal.Deals:    scopes(o, o, o, o),
			principal.Tasks:    scopes(o, o, o, o),
			principal.Nurture:  scopes(o, o, o, n),
			principal.Invoices: scopes(o, o, o, o),
			principal.Reports:  {principal.View: o},
			principal.Settings: {principal.View: n, principal.Edit: n},
			principal.Users:    {principal.View: a, principal.Create: n, principal.Edit: n},
			principal.Roles:    scopes(n, n, n, n),
		}},
		{Name: "Viewer", Description: "Read-only access to every record.", Permissions: principal.Permissions{
			principal.Contacts: scopes(a, n, n, n),
			principal.Deals:    scopes(a, n, n, n),
			principal.Tasks:    scopes(a, n, n, n),
			principal.Nurture:  scopes(a, n, n, n),
			principal.Invoices: scopes(a, n, n, n),
			principal.Reports:  {principal.View: a},
			principal.Settings: {principal.View: n, principal.Edit: n},
			principal.Users:    {principal.View: a, principal.Create: n, principal.Edit: n},
			principal.Roles:    scopes(n, n, n, n),
		}},
	}
}()

const (
	maxRoleName        = 60
	maxRoleDescription = 300
)

func (in RoleInput) apply(r *Role) error {
	if in.Name != nil {
		r.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		r.Description = strings.TrimSpace(*in.Description)
	}
	if in.Permissions != nil {
		if err := in.Permissions.Validate(); err != nil {
			var pe *principal.PermissionError
			if errors.As(err, &pe) {
				return &FieldError{pe.Field, "invalid", pe.Message}
			}
			return err
		}
		r.Permissions = in.Permissions.Normalized()
	}
	switch {
	case r.Name == "":
		return &FieldError{"name", "required", "Name is required."}
	case utf8.RuneCountInString(r.Name) > maxRoleName:
		return &FieldError{"name", "invalid", "Name must be at most 60 characters."}
	case utf8.RuneCountInString(r.Description) > maxRoleDescription:
		return &FieldError{"description", "invalid", "Description must be at most 300 characters."}
	}
	return nil
}

// grantable checks that p may hand out perms: nobody can create, change or
// assign a role with more access than their own (owners have it all).
func grantable(p principal.Principal, perms principal.Permissions) error {
	if ok, what := perms.Within(p.Effective()); !ok {
		return &ExceedsError{Permission: what}
	}
	return nil
}

// Roles lists the caller's org's roles.
func (s *Service) Roles(ctx context.Context, p principal.Principal) ([]Role, error) {
	if !p.Can(principal.Roles, principal.View) {
		return nil, ErrForbidden
	}
	return s.repo.Roles(ctx, p.OrgID)
}

func (s *Service) Role(ctx context.Context, p principal.Principal, id int64) (Role, error) {
	if !p.Can(principal.Roles, principal.View) {
		return Role{}, ErrForbidden
	}
	return s.repo.Role(ctx, p.OrgID, id)
}

func (s *Service) CreateRole(ctx context.Context, p principal.Principal, in RoleInput) (Role, error) {
	if !p.Can(principal.Roles, principal.Create) {
		return Role{}, ErrForbidden
	}
	r := Role{Permissions: principal.Permissions{}.Normalized()}
	if err := in.apply(&r); err != nil {
		return Role{}, err
	}
	if err := grantable(p, r.Permissions); err != nil {
		return Role{}, err
	}
	return s.repo.CreateRole(ctx, p.OrgID, r)
}

// UpdateRole changes a role; its users get the new permissions on their
// next request.
func (s *Service) UpdateRole(ctx context.Context, p principal.Principal, id int64, in RoleInput) (Role, error) {
	if !p.Can(principal.Roles, principal.Edit) {
		return Role{}, ErrForbidden
	}
	r, err := s.repo.Role(ctx, p.OrgID, id)
	if err != nil {
		return Role{}, err
	}
	if r.Locked {
		return Role{}, ErrRoleLocked
	}
	// Changing a role that already exceeds the caller would let them shape
	// access they do not have.
	if err := grantable(p, r.Permissions); err != nil {
		return Role{}, err
	}
	if err := in.apply(&r); err != nil {
		return Role{}, err
	}
	if err := grantable(p, r.Permissions); err != nil {
		return Role{}, err
	}
	return s.repo.UpdateRole(ctx, p.OrgID, r)
}

// DeleteRole removes a role nobody has.
func (s *Service) DeleteRole(ctx context.Context, p principal.Principal, id int64) error {
	if !p.Can(principal.Roles, principal.Delete) {
		return ErrForbidden
	}
	r, err := s.repo.Role(ctx, p.OrgID, id)
	if err != nil {
		return err
	}
	if r.Locked {
		return ErrRoleLocked
	}
	if err := grantable(p, r.Permissions); err != nil {
		return err
	}
	return s.repo.DeleteRole(ctx, p.OrgID, id)
}

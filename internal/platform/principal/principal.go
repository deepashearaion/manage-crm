// Package principal carries the authenticated caller through a request's
// context. The auth module sets it, with the permissions of the caller's
// role loaded fresh for each request; other modules only read it, so they
// never depend on how authentication or roles work. The org always comes
// from here (the signed token), never from client input.
package principal

import "context"

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID int64
	OrgID  int64
	// RoleID and RoleName name the caller's role.
	RoleID   int64
	RoleName string
	// Owner marks the org's locked owner role: full access to everything.
	Owner bool
	// Perms is what the role grants; Owner overrides it.
	Perms Permissions
}

// Scope is how far p may perform action on module.
func (p Principal) Scope(m Module, a Action) Scope {
	if p.Owner {
		return All
	}
	return p.Perms.Scope(m, a)
}

// Can reports whether p may perform action on module at all (on its own
// records at least).
func (p Principal) Can(m Module, a Action) bool {
	return p.Scope(m, a) != None
}

// SeesAll reports whether p may view every record of module, not just the
// ones they own.
func (p Principal) SeesAll(m Module) bool {
	return p.Scope(m, View) == All
}

// CanOn reports whether p may perform action on a record of module owned
// by ownerID (nil when it has no owner). With scope "own" only records
// owned by p qualify. For Create, ownerID is who the new record will
// belong to.
func (p Principal) CanOn(m Module, a Action, ownerID *int64) bool {
	switch p.Scope(m, a) {
	case All:
		return true
	case Own:
		return ownerID != nil && *ownerID == p.UserID
	}
	return false
}

// CanReassign reports whether p may change who owns a record of module:
// that needs edit access to every record.
func (p Principal) CanReassign(m Module) bool {
	return p.Scope(m, Edit) == All
}

// Effective is the caller's full permission set (every catalog entry),
// as the frontend needs it to show or hide controls.
func (p Principal) Effective() Permissions {
	if p.Owner {
		return FullAccess()
	}
	return p.Perms.Normalized()
}

type ctxKey struct{}

// With returns a copy of ctx carrying p.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal in ctx and whether one is present.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

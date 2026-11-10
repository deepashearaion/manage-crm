// Package principal carries the authenticated caller through a request's
// context. The auth module sets it; other modules read it, so they never
// depend on how authentication works. The org always comes from here (the
// signed token), never from client input.
package principal

import "context"

// Role is a user's role within their org.
type Role string

const (
	RoleOwner   Role = "owner"
	RoleAdmin   Role = "admin"
	RoleManager Role = "manager"
	RoleRep     Role = "rep"
	RoleViewer  Role = "viewer"
)

// Valid reports whether r is one of the defined roles.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleManager, RoleRep, RoleViewer:
		return true
	}
	return false
}

// Principal is the authenticated caller of a request.
type Principal struct {
	UserID int64
	OrgID  int64
	Role   Role
}

// Permissions. Owners and admins can do everything; managers everything
// except managing users; reps work only on records they own; viewers only
// read.

// ManagesUsers reports whether p may create users and change roles.
func (p Principal) ManagesUsers() bool {
	return p.Role == RoleOwner || p.Role == RoleAdmin
}

// SeesAllRecords reports whether p may read every record in the org (reps
// see only records they own).
func (p Principal) SeesAllRecords() bool {
	return p.Role != RoleRep
}

// CanWrite reports whether p may create, change or delete records at all.
func (p Principal) CanWrite() bool {
	return p.Role != RoleViewer
}

// CanReassign reports whether p may change who owns a record.
func (p Principal) CanReassign() bool {
	return p.Role == RoleOwner || p.Role == RoleAdmin || p.Role == RoleManager
}

// ManagesSettings reports whether p may change org-wide configuration such
// as lead sources, scoring rules and routing rules.
func (p Principal) ManagesSettings() bool {
	return p.Role == RoleOwner || p.Role == RoleAdmin || p.Role == RoleManager
}

// CanWriteRecord reports whether p may change or delete a record owned by
// ownerID (nil when the record has no owner).
func (p Principal) CanWriteRecord(ownerID *int64) bool {
	if !p.CanWrite() {
		return false
	}
	return p.SeesAllRecords() || (ownerID != nil && *ownerID == p.UserID)
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

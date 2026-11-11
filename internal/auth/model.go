package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// User is an account that can sign in, with its role.
type User struct {
	ID           int64
	OrgID        int64
	RoleID       int64
	RoleName     string
	RoleOwner    bool // the org's locked owner role
	Perms        principal.Permissions
	Name         string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// Principal is the user as a request's caller, with their role's
// permissions.
func (u User) Principal() principal.Principal {
	return principal.Principal{UserID: u.ID, OrgID: u.OrgID, RoleID: u.RoleID, RoleName: u.RoleName,
		Owner: u.RoleOwner, Perms: u.Perms}
}

// OrgCreatedHook runs inside the transaction that creates an org, so other
// modules can set up the org's data (e.g. default deal stages) atomically.
type OrgCreatedHook func(ctx context.Context, tx pgx.Tx, orgID int64) error

// Session is the outcome of signing in or refreshing: a short-lived access
// token (JWT) and the rotating refresh token that renews it.
type Session struct {
	UserID       int64
	AccessToken  string
	RefreshToken RefreshToken
}

// RefreshToken is a newly issued refresh token and when it expires.
type RefreshToken struct {
	Token     string
	ExpiresAt time.Time
}

var (
	ErrMissingFields      = errors.New("name, email and password are required")
	ErrInvalidEmail       = errors.New("invalid email address")
	ErrPasswordLength     = errors.New("password must be 8 to 72 characters long")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidResetToken  = errors.New("invalid or expired reset token")

	// ErrInvalidRefreshToken covers unknown, expired and revoked tokens.
	ErrInvalidRefreshToken = errors.New("invalid or expired refresh token")
	// ErrRefreshTokenReused means an already-rotated token was presented; the
	// session has been revoked because the token was probably stolen.
	ErrRefreshTokenReused = errors.New("refresh token reused")

	ErrUserNotFound = errors.New("user not found")
	ErrForbidden    = errors.New("not allowed")
	ErrLastOwner    = errors.New("an organization must keep at least one owner")

	ErrRoleNotFound  = errors.New("role not found")
	ErrRoleLocked    = errors.New("the owner role cannot be changed or deleted")
	ErrRoleInUse     = errors.New("role is assigned to users")
	ErrRoleNameTaken = errors.New("role name already used")
	ErrOwnRole       = errors.New("you cannot change your own role")
)

// ExceedsError means a role would grant more than the caller has, which
// would let them raise their own access.
type ExceedsError struct {
	Permission string // e.g. "deals.edit"
}

func (e *ExceedsError) Error() string {
	return "the role grants " + e.Permission + " beyond your own access"
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field, Problem, Message string
}

func (e *FieldError) Error() string { return e.Message }

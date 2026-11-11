package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
	"github.com/deepashearaion/manage-crm/internal/platform/validate"
)

const resetTokenTTL = 30 * time.Minute

// Settings configures the auth service.
type Settings struct {
	RefreshTokenTTL time.Duration
	// FrontendURL is the base of the password-reset link sent by email.
	FrontendURL string
	// OnOrgCreated sets up other modules' data for a new org.
	OnOrgCreated OrgCreatedHook
}

type Service struct {
	repo     *Repository
	tokens   *JWT
	mailer   mail.Mailer
	settings Settings
}

func NewService(repo *Repository, tokens *JWT, mailer mail.Mailer, settings Settings) *Service {
	return &Service{repo: repo, tokens: tokens, mailer: mailer, settings: settings}
}

// Register creates an account and returns its id.
// Register signs up a new org with the user as its owner. orgName defaults
// to "<name>'s organization".
func (s *Service) Register(ctx context.Context, name, email, password, orgName string) (int64, error) {
	name = strings.TrimSpace(name)
	email = validate.NormalizeEmail(email)
	orgName = strings.TrimSpace(orgName)

	switch {
	case name == "" || email == "" || password == "":
		return 0, ErrMissingFields
	case !validate.Email(email):
		return 0, ErrInvalidEmail
	case !ValidPassword(password):
		return 0, ErrPasswordLength
	}

	// Checked before hashing so a taken email fails fast; CreateUser also
	// catches the race where two registrations pass this check together.
	_, err := s.repo.UserByEmail(ctx, email)
	if err == nil {
		return 0, ErrEmailTaken
	}
	if !errors.Is(err, ErrUserNotFound) {
		return 0, fmt.Errorf("look up email: %w", err)
	}

	hash, err := hashPassword(password)
	if err != nil {
		return 0, fmt.Errorf("hash password: %w", err)
	}
	if orgName == "" {
		orgName = name + "'s organization"
	}
	owner, err := s.repo.CreateOrgWithOwner(ctx, orgName, User{Name: name, Email: email, PasswordHash: hash}, s.settings.OnOrgCreated)
	return owner.ID, err
}

// Login checks credentials and starts a session.
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	user, err := s.repo.UserByEmail(ctx, validate.NormalizeEmail(email))
	if errors.Is(err, ErrUserNotFound) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("look up user: %w", err)
	}
	if !checkPassword(password, user.PasswordHash) {
		return Session{}, ErrInvalidCredentials
	}

	refresh, hash, err := NewOpaqueToken()
	if err != nil {
		return Session{}, err
	}
	expiresAt, err := s.repo.CreateRefreshToken(ctx, user.ID, hash, s.settings.RefreshTokenTTL)
	if err != nil {
		return Session{}, fmt.Errorf("start session: %w", err)
	}
	return s.session(user, RefreshToken{Token: refresh, ExpiresAt: expiresAt})
}

// Refresh rotates a refresh token and issues a new access token. Reusing an
// already-rotated token ends the session with ErrRefreshTokenReused (the
// returned Session still carries the user id, for logging).
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	if refreshToken == "" {
		return Session{}, ErrInvalidRefreshToken
	}
	next, hash, err := NewOpaqueToken()
	if err != nil {
		return Session{}, err
	}
	userID, expiresAt, err := s.repo.RotateRefreshToken(ctx, HashToken(refreshToken), hash, s.settings.RefreshTokenTTL)
	if err != nil {
		return Session{UserID: userID}, err
	}
	// Reload the user so a changed role takes effect at the next refresh.
	user, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return Session{}, fmt.Errorf("load user: %w", err)
	}
	return s.session(user, RefreshToken{Token: next, ExpiresAt: expiresAt})
}

// Logout ends the session a refresh token belongs to; unknown or empty
// tokens are ignored, so it is idempotent.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.repo.RevokeRefreshSession(ctx, HashToken(refreshToken))
}

// ForgotPassword emails a reset link if an account exists for email. It
// returns the token ("" when there is no account) so callers may expose it
// in development; errors only occur for existing accounts.
func (s *Service) ForgotPassword(ctx context.Context, email string) (string, error) {
	user, err := s.repo.UserByEmail(ctx, validate.NormalizeEmail(email))
	if errors.Is(err, ErrUserNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up user: %w", err)
	}

	token, hash, err := NewOpaqueToken()
	if err != nil {
		return "", err
	}
	if err := s.repo.ReplaceResetToken(ctx, user.ID, hash, resetTokenTTL); err != nil {
		return "", fmt.Errorf("store reset token: %w", err)
	}

	link := s.settings.FrontendURL + "/reset-password?token=" + url.QueryEscape(token)
	err = s.mailer.Send(ctx, mail.Message{
		To:      user.Email,
		Subject: "Reset your password",
		Text: fmt.Sprintf(
			"Hi %s,\n\nWe received a request to reset your password. Open this link within %d minutes to choose a new one:\n\n%s\n\nIf you did not ask for this, you can ignore this email; your password will not change.\n",
			user.Name, int(resetTokenTTL.Minutes()), link,
		),
	})
	if err != nil {
		return token, fmt.Errorf("send reset email: %w", err)
	}
	return token, nil
}

// ResetPassword sets a new password using a reset token and signs the user
// out everywhere.
func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	if !ValidPassword(password) {
		return ErrPasswordLength
	}
	hash, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.repo.ResetPassword(ctx, HashToken(token), hash)
}

// AccessTokenTTL is how long issued access tokens stay valid.
func (s *Service) AccessTokenTTL() time.Duration { return s.tokens.TTL() }

func (s *Service) session(user User, refresh RefreshToken) (Session, error) {
	access, err := s.tokens.Generate(user.Principal())
	if err != nil {
		return Session{}, fmt.Errorf("generate access token: %w", err)
	}
	return Session{UserID: user.ID, AccessToken: access, RefreshToken: refresh}, nil
}

// Users. Every request reloads the caller's role, so role and permission
// changes apply from the user's next request.

const maxUserName = 100

// ListUsers returns the caller's org's users.
func (s *Service) ListUsers(ctx context.Context, p principal.Principal) ([]User, error) {
	if !p.Can(principal.Users, principal.View) {
		return nil, ErrForbidden
	}
	return s.repo.UsersInOrg(ctx, p.OrgID)
}

func (s *Service) GetUser(ctx context.Context, p principal.Principal, id int64) (User, error) {
	if !p.Can(principal.Users, principal.View) && id != p.UserID {
		return User{}, ErrForbidden
	}
	return s.repo.UserInOrg(ctx, p.OrgID, id)
}

// Me is the caller with their role and permissions.
func (s *Service) Me(ctx context.Context, p principal.Principal) (User, error) {
	return s.repo.UserInOrg(ctx, p.OrgID, p.UserID)
}

// NewUser is a user an admin creates, with the password they set.
type NewUser struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	RoleID   int64  `json:"role_id"`
}

// assignable checks that p may give someone role: only owners hand out the
// owner role, and nobody hands out more access than their own.
func (s *Service) assignable(ctx context.Context, p principal.Principal, roleID int64) error {
	role, err := s.repo.Role(ctx, p.OrgID, roleID)
	if errors.Is(err, ErrRoleNotFound) {
		return &FieldError{"role_id", "invalid", "No such role."}
	}
	if err != nil {
		return err
	}
	if role.Locked && !p.Owner {
		return ErrForbidden
	}
	return grantable(p, role.Permissions)
}

// CreateUser adds a user to the caller's org with the password the caller
// chose; the user can sign in with it straight away.
func (s *Service) CreateUser(ctx context.Context, p principal.Principal, in NewUser) (User, error) {
	if !p.Can(principal.Users, principal.Create) {
		return User{}, ErrForbidden
	}
	name := strings.TrimSpace(in.Name)
	email := validate.NormalizeEmail(in.Email)
	switch {
	case name == "":
		return User{}, &FieldError{"name", "required", "Name is required."}
	case len([]rune(name)) > maxUserName:
		return User{}, &FieldError{"name", "invalid", "Name must be at most 100 characters."}
	case email == "":
		return User{}, &FieldError{"email", "required", "Email is required."}
	case !validate.Email(email):
		return User{}, ErrInvalidEmail
	case !ValidPassword(in.Password):
		return User{}, ErrPasswordLength
	case in.RoleID == 0:
		return User{}, &FieldError{"role_id", "required", "Choose a role."}
	}
	if err := s.assignable(ctx, p, in.RoleID); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	u, err := s.repo.CreateUser(ctx, User{OrgID: p.OrgID, RoleID: in.RoleID, Name: name, Email: email, PasswordHash: hash})
	if err != nil {
		return User{}, err
	}
	return s.repo.UserInOrg(ctx, p.OrgID, u.ID)
}

// editable loads a user p may change: owners can be changed only by owners.
func (s *Service) editable(ctx context.Context, p principal.Principal, id int64) (User, error) {
	if !p.Can(principal.Users, principal.Edit) {
		return User{}, ErrForbidden
	}
	u, err := s.repo.UserInOrg(ctx, p.OrgID, id)
	if err != nil {
		return User{}, err
	}
	if u.RoleOwner && !p.Owner {
		return User{}, ErrForbidden
	}
	return u, nil
}

// RenameUser changes a user's name.
func (s *Service) RenameUser(ctx context.Context, p principal.Principal, id int64, name string) (User, error) {
	if _, err := s.editable(ctx, p, id); err != nil {
		return User{}, err
	}
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return User{}, &FieldError{"name", "required", "Name is required."}
	case len([]rune(name)) > maxUserName:
		return User{}, &FieldError{"name", "invalid", "Name must be at most 100 characters."}
	}
	return s.repo.RenameUser(ctx, p.OrgID, id, name)
}

// AssignRole gives a user another role. Nobody changes their own role
// except an owner stepping down (the org keeps at least one owner).
func (s *Service) AssignRole(ctx context.Context, p principal.Principal, userID, roleID int64) (User, error) {
	target, err := s.editable(ctx, p, userID)
	if err != nil {
		return User{}, err
	}
	if userID == p.UserID && !p.Owner {
		return User{}, ErrOwnRole
	}
	if roleID == 0 {
		return User{}, &FieldError{"role_id", "required", "Choose a role."}
	}
	if err := s.assignable(ctx, p, roleID); err != nil {
		return User{}, err
	}
	// Taking a user's role away needs as much access as giving it.
	if err := grantable(p, target.Perms); err != nil && !target.RoleOwner {
		return User{}, err
	}
	return s.repo.SetRole(ctx, p.OrgID, userID, roleID)
}

// SetUserPassword replaces another user's password (an admin reset) and
// signs them out everywhere.
func (s *Service) SetUserPassword(ctx context.Context, p principal.Principal, userID int64, password string) error {
	if _, err := s.editable(ctx, p, userID); err != nil {
		return err
	}
	if !ValidPassword(password) {
		return ErrPasswordLength
	}
	hash, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.repo.SetPassword(ctx, p.OrgID, userID, hash)
}

// Access is the principal for a signed-in user, loaded for each request so
// role changes apply at once. A user who no longer exists (or moved org)
// gets ErrUserNotFound.
func (s *Service) Access(ctx context.Context, userID, orgID int64) (principal.Principal, error) {
	u, err := s.repo.UserInOrg(ctx, orgID, userID)
	if err != nil {
		return principal.Principal{}, err
	}
	return u.Principal(), nil
}

// System lookups for other modules (wired in internal/app), e.g. to find
// whom to notify. They never expose password hashes outside this module's
// User type, which callers map to their own types.

// OrgUser returns a user of orgID, or ErrUserNotFound.
func (s *Service) OrgUser(ctx context.Context, orgID, userID int64) (User, error) {
	return s.repo.UserInOrg(ctx, orgID, userID)
}

// OrgUsers lists the users of orgID.
func (s *Service) OrgUsers(ctx context.Context, orgID int64) ([]User, error) {
	return s.repo.UsersInOrg(ctx, orgID)
}

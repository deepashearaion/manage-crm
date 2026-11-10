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

// inviteTTL is how long an invited user's set-password link works.
const inviteTTL = 72 * time.Hour

// unusablePasswordHash is stored for invited users until they set a
// password; no password matches it.
const unusablePasswordHash = "!"

// ErrInviteNotSent means the user was created but the invite email failed;
// an admin can resend it later (or the user can use "forgot password").
var ErrInviteNotSent = errors.New("invite email not sent")

// ListUsers returns the caller's org's users. Every role may list them, so
// owner pickers can show names.
func (s *Service) ListUsers(ctx context.Context, p principal.Principal) ([]User, error) {
	return s.repo.UsersInOrg(ctx, p.OrgID)
}

// InviteUser adds a user to the caller's org with no usable password and
// emails a link to set one. Only owners and admins may invite, and only
// owners may create owners.
func (s *Service) InviteUser(ctx context.Context, p principal.Principal, name, email string, role principal.Role) (User, error) {
	if !p.ManagesUsers() {
		return User{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	email = validate.NormalizeEmail(email)
	switch {
	case name == "" || email == "":
		return User{}, ErrMissingFields
	case !validate.Email(email):
		return User{}, ErrInvalidEmail
	case !role.Valid():
		return User{}, ErrInvalidRole
	case role == principal.RoleOwner && p.Role != principal.RoleOwner:
		return User{}, ErrForbidden
	}

	user, err := s.repo.CreateUser(ctx, User{OrgID: p.OrgID, Role: role, Name: name, Email: email, PasswordHash: unusablePasswordHash})
	if err != nil {
		return User{}, err
	}

	token, hash, err := NewOpaqueToken()
	if err != nil {
		return user, fmt.Errorf("%w: %w", ErrInviteNotSent, err)
	}
	if err := s.repo.ReplaceResetToken(ctx, user.ID, hash, inviteTTL); err != nil {
		return user, fmt.Errorf("%w: store token: %w", ErrInviteNotSent, err)
	}
	link := s.settings.FrontendURL + "/reset-password?token=" + url.QueryEscape(token)
	err = s.mailer.Send(ctx, mail.Message{
		To:      user.Email,
		Subject: "You have been invited to the CRM",
		Text: fmt.Sprintf(
			"Hi %s,\n\nAn account has been created for you. Open this link within %d hours to set your password:\n\n%s\n",
			user.Name, int(inviteTTL.Hours()), link,
		),
	})
	if err != nil {
		return user, fmt.Errorf("%w: %w", ErrInviteNotSent, err)
	}
	return user, nil
}

// ChangeRole sets the role of a user in the caller's org. Only owners and
// admins may change roles, only owners may grant or remove the owner role,
// and the last owner cannot be demoted. The new role applies from the
// user's next token refresh.
func (s *Service) ChangeRole(ctx context.Context, p principal.Principal, userID int64, role principal.Role) (User, error) {
	if !p.ManagesUsers() {
		return User{}, ErrForbidden
	}
	if !role.Valid() {
		return User{}, ErrInvalidRole
	}
	target, err := s.repo.UserInOrg(ctx, p.OrgID, userID)
	if err != nil {
		return User{}, err
	}
	if (target.Role == principal.RoleOwner || role == principal.RoleOwner) && p.Role != principal.RoleOwner {
		return User{}, ErrForbidden
	}
	return s.repo.SetRole(ctx, p.OrgID, userID, role)
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

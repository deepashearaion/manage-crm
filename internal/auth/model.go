package auth

import (
	"errors"
	"time"
)

// User is an account that can sign in.
type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
}

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

	errUserNotFound = errors.New("user not found")
)

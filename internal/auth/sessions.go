package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrInvalidRefreshToken covers unknown, expired and revoked tokens.
	ErrInvalidRefreshToken = errors.New("invalid or expired refresh token")
	// ErrRefreshTokenReused means an already-rotated token was presented; the
	// session has been revoked because the token was probably stolen.
	ErrRefreshTokenReused = errors.New("refresh token reused")
)

// Execer runs a statement; *pgxpool.Pool and pgx.Tx satisfy it.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Sessions manages rotating refresh tokens. Each login starts a session (a
// token family); each refresh replaces the presented token with a new one.
type Sessions struct {
	DB  *pgxpool.Pool
	TTL time.Duration
}

// RefreshToken is a newly issued token and when it expires.
type RefreshToken struct {
	Token     string
	ExpiresAt time.Time
}

// Start begins a new session for userID.
func (s *Sessions) Start(ctx context.Context, userID int64) (RefreshToken, error) {
	return s.issue(ctx, s.DB, userID, pgtype.UUID{})
}

// Rotate exchanges a refresh token for a new one in the same session and
// returns the session's user. Presenting a token that was already rotated
// revokes the whole session and returns ErrRefreshTokenReused.
func (s *Sessions) Rotate(ctx context.Context, token string) (int64, RefreshToken, error) {
	var (
		userID int64
		next   RefreshToken
		reused bool
	)

	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var (
			id                 int64
			family             pgtype.UUID
			expiresAt          time.Time
			rotatedAt, revoked pgtype.Timestamptz
		)
		// FOR UPDATE makes concurrent refreshes of one token take turns, so
		// only the first succeeds and the second is seen as reuse.
		err := tx.QueryRow(ctx,
			`SELECT id, user_id, family_id, expires_at, rotated_at, revoked_at
			FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
			HashToken(token),
		).Scan(&id, &userID, &family, &expiresAt, &rotatedAt, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidRefreshToken
		}
		if err != nil {
			return err
		}

		switch {
		case revoked.Valid:
			return ErrInvalidRefreshToken
		case rotatedAt.Valid:
			if _, err := tx.Exec(ctx,
				`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`,
				family,
			); err != nil {
				return err
			}
			reused = true // commit the revocation, then report the reuse
			return nil
		case !expiresAt.After(time.Now()):
			return ErrInvalidRefreshToken
		}

		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET rotated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		next, err = s.issue(ctx, tx, userID, family)
		return err
	})

	if err != nil {
		return 0, RefreshToken{}, err
	}
	if reused {
		return userID, RefreshToken{}, ErrRefreshTokenReused
	}
	return userID, next, nil
}

// Revoke ends the session the token belongs to. Unknown tokens are ignored,
// so logout is idempotent.
func (s *Sessions) Revoke(ctx context.Context, token string) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		WHERE family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1) AND revoked_at IS NULL`,
		HashToken(token),
	)
	return err
}

// RevokeAll ends every session of a user, e.g. after a password change. Pass
// a transaction to make it part of a larger change.
func RevokeAll(ctx context.Context, db Execer, userID int64) error {
	_, err := db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID,
	)
	return err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// issue stores a new token in family, or in a new family when family is unset.
func (s *Sessions) issue(ctx context.Context, db querier, userID int64, family pgtype.UUID) (RefreshToken, error) {
	token, hash, err := NewOpaqueToken()
	if err != nil {
		return RefreshToken{}, err
	}
	var expiresAt time.Time
	err = db.QueryRow(ctx,
		`INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES ($1, COALESCE($2, gen_random_uuid()), $3, now() + make_interval(secs => $4))
		RETURNING expires_at`,
		userID, family, hash, s.TTL.Seconds(),
	).Scan(&expiresAt)
	if err != nil {
		return RefreshToken{}, fmt.Errorf("store refresh token: %w", err)
	}
	return RefreshToken{Token: token, ExpiresAt: expiresAt}, nil
}

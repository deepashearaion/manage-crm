package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// Repository holds all of the auth module's SQL: users, password reset
// tokens and refresh tokens. Multi-step changes run in one transaction.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const userColumns = `id, org_id, role, name, email, password_hash`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.OrgID, &u.Role, &u.Name, &u.Email, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, errUserNotFound
	}
	return u, err
}

func (r *Repository) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

func (r *Repository) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// CreateOrgWithOwner creates an org and its first user (role owner) in one
// transaction, running onCreated inside it. A duplicate email returns
// ErrEmailTaken.
func (r *Repository) CreateOrgWithOwner(ctx context.Context, orgName string, owner User, onCreated OrgCreatedHook) (User, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ($1) RETURNING id`, orgName).Scan(&owner.OrgID); err != nil {
			return err
		}
		owner.Role = principal.RoleOwner
		var err error
		if owner.ID, err = insertUser(ctx, tx, owner); err != nil {
			return err
		}
		if onCreated != nil {
			return onCreated(ctx, tx, owner.OrgID)
		}
		return nil
	})
	return owner, err
}

// CreateUser adds a user to an existing org; a duplicate email returns
// ErrEmailTaken.
func (r *Repository) CreateUser(ctx context.Context, u User) (User, error) {
	var err error
	u.ID, err = insertUser(ctx, r.db, u)
	return u, err
}

func insertUser(ctx context.Context, db queryRower, u User) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO users (org_id, role, name, email, password_hash) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		u.OrgID, u.Role, u.Name, u.Email, u.PasswordHash,
	).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrEmailTaken
	}
	return id, err
}

// ReplaceResetToken stores a new reset token for the user and invalidates
// any earlier unused ones, so only the newest link works.
func (r *Repository) ReplaceResetToken(ctx context.Context, userID int64, tokenHash []byte, ttl time.Duration) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE password_reset_tokens SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`,
			userID,
		); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
			VALUES ($1, $2, now() + make_interval(secs => $3))`,
			userID, tokenHash, ttl.Seconds(),
		)
		return err
	})
}

// ResetPassword consumes a valid reset token and, in the same transaction,
// sets the password, invalidates the user's other reset tokens and revokes
// all of their sessions. It returns ErrInvalidResetToken for unknown, used
// or expired tokens.
func (r *Repository) ResetPassword(ctx context.Context, tokenHash []byte, passwordHash string) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var userID int64
		err := tx.QueryRow(ctx,
			`UPDATE password_reset_tokens SET used_at = now()
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
			RETURNING user_id`,
			tokenHash,
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidResetToken
		}
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE users SET password_hash = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`,
			passwordHash, userID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE password_reset_tokens SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`,
			userID,
		); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
			userID,
		)
		return err
	})
}

// CreateRefreshToken starts a new session (token family) for the user.
func (r *Repository) CreateRefreshToken(ctx context.Context, userID int64, tokenHash []byte, ttl time.Duration) (time.Time, error) {
	return insertRefreshToken(ctx, r.db, userID, pgtype.UUID{}, tokenHash, ttl)
}

// RotateRefreshToken replaces the token with oldHash by one with newHash in
// the same session and returns the session's user. An already-rotated token
// revokes the whole session (committed) and returns ErrRefreshTokenReused;
// unknown, revoked or expired tokens return ErrInvalidRefreshToken.
func (r *Repository) RotateRefreshToken(ctx context.Context, oldHash, newHash []byte, ttl time.Duration) (int64, time.Time, error) {
	var (
		userID    int64
		expiresAt time.Time
		reused    bool
	)
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var (
			id                 int64
			family             pgtype.UUID
			oldExpiry          time.Time
			rotatedAt, revoked pgtype.Timestamptz
		)
		// FOR UPDATE makes concurrent refreshes of one token take turns, so
		// only the first succeeds and the second is seen as reuse.
		err := tx.QueryRow(ctx,
			`SELECT id, user_id, family_id, expires_at, rotated_at, revoked_at
			FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`,
			oldHash,
		).Scan(&id, &userID, &family, &oldExpiry, &rotatedAt, &revoked)
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
			reused = true
			_, err := tx.Exec(ctx,
				`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`,
				family,
			)
			return err // commit the revocation; the reuse is reported below
		case !oldExpiry.After(time.Now()):
			return ErrInvalidRefreshToken
		}

		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET rotated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		expiresAt, err = insertRefreshToken(ctx, tx, userID, family, newHash, ttl)
		return err
	})
	if err != nil {
		return 0, time.Time{}, err
	}
	if reused {
		return userID, time.Time{}, ErrRefreshTokenReused
	}
	return userID, expiresAt, nil
}

// RevokeRefreshSession revokes the session the token belongs to. Unknown
// tokens are ignored.
func (r *Repository) RevokeRefreshSession(ctx context.Context, tokenHash []byte) error {
	_, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		WHERE family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1) AND revoked_at IS NULL`,
		tokenHash,
	)
	return err
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertRefreshToken stores a token in family, or in a new family when
// family is unset, and returns its expiry.
func insertRefreshToken(ctx context.Context, db queryRower, userID int64, family pgtype.UUID, tokenHash []byte, ttl time.Duration) (time.Time, error) {
	var expiresAt time.Time
	err := db.QueryRow(ctx,
		`INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES ($1, COALESCE($2, gen_random_uuid()), $3, now() + make_interval(secs => $4))
		RETURNING expires_at`,
		userID, family, tokenHash, ttl.Seconds(),
	).Scan(&expiresAt)
	return expiresAt, err
}

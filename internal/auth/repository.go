package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository holds all of the auth module's SQL: users, password reset
// tokens and refresh tokens. Multi-step changes run in one transaction.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// selectUser reads users with their role; append a WHERE clause.
const selectUser = `SELECT u.id, u.org_id, u.role_id, r.name, r.is_owner, r.permissions,
	u.name, u.email, u.password_hash, u.created_at
	FROM users u JOIN roles r ON r.id = u.role_id `

func scanUser(row pgx.Row) (User, error) {
	var u User
	var perms []byte
	err := row.Scan(&u.ID, &u.OrgID, &u.RoleID, &u.RoleName, &u.RoleOwner, &perms,
		&u.Name, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	if err := json.Unmarshal(perms, &u.Perms); err != nil {
		return User{}, fmt.Errorf("role %d permissions: %w", u.RoleID, err)
	}
	return u, nil
}

func (r *Repository) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(r.db.QueryRow(ctx, selectUser+`WHERE u.email = $1`, email))
}

func (r *Repository) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(r.db.QueryRow(ctx, selectUser+`WHERE u.id = $1`, id))
}

// CreateOrgWithOwner creates an org, its default roles and its first user
// (with the owner role) in one transaction, running onCreated inside it. A
// duplicate email returns ErrEmailTaken.
func (r *Repository) CreateOrgWithOwner(ctx context.Context, orgName string, owner User, onCreated OrgCreatedHook) (User, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ($1) RETURNING id`, orgName).Scan(&owner.OrgID); err != nil {
			return err
		}
		var err error
		if owner.RoleID, err = seedRoles(ctx, tx, owner.OrgID); err != nil {
			return err
		}
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
		`INSERT INTO users (org_id, role_id, name, email, password_hash) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		u.OrgID, u.RoleID, u.Name, u.Email, u.PasswordHash,
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

// UsersInOrg lists an org's users, oldest first.
func (r *Repository) UsersInOrg(ctx context.Context, orgID int64) ([]User, error) {
	rows, err := r.db.Query(ctx, selectUser+`WHERE u.org_id = $1 ORDER BY u.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UserInOrg returns a user of orgID, or ErrUserNotFound.
func (r *Repository) UserInOrg(ctx context.Context, orgID, userID int64) (User, error) {
	return scanUser(r.db.QueryRow(ctx, selectUser+`WHERE u.org_id = $1 AND u.id = $2`, orgID, userID))
}

// RenameUser changes a user's name.
func (r *Repository) RenameUser(ctx context.Context, orgID, userID int64, name string) (User, error) {
	tag, err := r.db.Exec(ctx, `UPDATE users SET name = $3, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, userID, name)
	if err != nil {
		return User{}, err
	}
	if tag.RowsAffected() == 0 {
		return User{}, ErrUserNotFound
	}
	return r.UserInOrg(ctx, orgID, userID)
}

// SetPassword replaces a user's password and ends all of their sessions.
func (r *Repository) SetPassword(ctx context.Context, orgID, userID int64, passwordHash string) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $3, updated_at = now() WHERE org_id = $1 AND id = $2`,
			orgID, userID, passwordHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrUserNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
		return err
	})
}

// SetRole gives a user of orgID the role roleID (which must be one of the
// org's roles). Moving the org's only owner to another role returns
// ErrLastOwner; the org's owners are locked so two changes cannot race
// past the check.
func (r *Repository) SetRole(ctx context.Context, orgID, userID, roleID int64) (User, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT u.id FROM users u JOIN roles r ON r.id = u.role_id
			WHERE u.org_id = $1 AND r.is_owner FOR UPDATE OF u`, orgID)
		if err != nil {
			return err
		}
		owners := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			owners[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var toOwner bool
		err = tx.QueryRow(ctx, `SELECT is_owner FROM roles WHERE org_id = $1 AND id = $2`, orgID, roleID).Scan(&toOwner)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoleNotFound
		}
		if err != nil {
			return err
		}
		if owners[userID] && !toOwner && len(owners) == 1 {
			return ErrLastOwner
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET role_id = $3, updated_at = now() WHERE org_id = $1 AND id = $2`,
			orgID, userID, roleID)
		if err == nil && tag.RowsAffected() == 0 {
			err = ErrUserNotFound
		}
		return err
	})
	if err != nil {
		return User{}, err
	}
	return r.UserInOrg(ctx, orgID, userID)
}

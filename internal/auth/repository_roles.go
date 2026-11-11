package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

const selectRole = `SELECT r.id, r.name, r.description, r.is_owner, r.permissions,
	(SELECT count(*) FROM users u WHERE u.role_id = r.id), r.created_at, r.updated_at
	FROM roles r `

func scanRole(row pgx.Row) (Role, error) {
	var r Role
	var perms []byte
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.Locked, &perms, &r.UserCount, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, ErrRoleNotFound
	}
	if err != nil {
		return Role{}, err
	}
	if err := json.Unmarshal(perms, &r.Permissions); err != nil {
		return Role{}, fmt.Errorf("role %d permissions: %w", r.ID, err)
	}
	// The owner role has everything, whatever is stored.
	if r.Locked {
		r.Permissions = principal.FullAccess()
	} else {
		r.Permissions = r.Permissions.Normalized()
	}
	return r, nil
}

// seedRoles creates DefaultRoles for a new org inside tx and returns the
// owner role's id.
func seedRoles(ctx context.Context, tx pgx.Tx, orgID int64) (int64, error) {
	var ownerID int64
	for _, d := range DefaultRoles {
		perms, err := json.Marshal(d.Permissions.Normalized())
		if err != nil {
			return 0, err
		}
		var id int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO roles (org_id, name, description, is_owner, permissions) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			orgID, d.Name, d.Description, d.Locked, perms,
		).Scan(&id); err != nil {
			return 0, err
		}
		if d.Locked {
			ownerID = id
		}
	}
	return ownerID, nil
}

func (r *Repository) Roles(ctx context.Context, orgID int64) ([]Role, error) {
	rows, err := r.db.Query(ctx, selectRole+`WHERE r.org_id = $1 ORDER BY r.is_owner DESC, r.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Role, 0)
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, rows.Err()
}

func (r *Repository) Role(ctx context.Context, orgID, id int64) (Role, error) {
	return scanRole(r.db.QueryRow(ctx, selectRole+`WHERE r.org_id = $1 AND r.id = $2`, orgID, id))
}

func nameTaken(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrRoleNameTaken
	}
	return err
}

func (r *Repository) CreateRole(ctx context.Context, orgID int64, role Role) (Role, error) {
	perms, err := json.Marshal(role.Permissions)
	if err != nil {
		return Role{}, err
	}
	var id int64
	err = r.db.QueryRow(ctx,
		`INSERT INTO roles (org_id, name, description, permissions) VALUES ($1, $2, $3, $4) RETURNING id`,
		orgID, role.Name, role.Description, perms,
	).Scan(&id)
	if err != nil {
		return Role{}, nameTaken(err)
	}
	return r.Role(ctx, orgID, id)
}

// UpdateRole saves a role's name, description and permissions. The owner
// role is never changed.
func (r *Repository) UpdateRole(ctx context.Context, orgID int64, role Role) (Role, error) {
	perms, err := json.Marshal(role.Permissions)
	if err != nil {
		return Role{}, err
	}
	tag, err := r.db.Exec(ctx,
		`UPDATE roles SET name = $3, description = $4, permissions = $5, updated_at = now()
		WHERE org_id = $1 AND id = $2 AND NOT is_owner`,
		orgID, role.ID, role.Name, role.Description, perms,
	)
	if err != nil {
		return Role{}, nameTaken(err)
	}
	if tag.RowsAffected() == 0 {
		return Role{}, ErrRoleNotFound
	}
	return r.Role(ctx, orgID, role.ID)
}

// DeleteRole deletes a role no user has; otherwise ErrRoleInUse.
func (r *Repository) DeleteRole(ctx context.Context, orgID, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM roles WHERE org_id = $1 AND id = $2 AND NOT is_owner`, orgID, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrRoleInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRoleNotFound
	}
	return nil
}

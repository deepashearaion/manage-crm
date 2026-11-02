package contacts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Scope limits queries to one org and, when OwnerID is set, to the contacts
// that user owns. Every query takes a Scope, so no query can cross orgs.
type Scope struct {
	OrgID   int64
	OwnerID *int64
}

// columns lists Contact's fields in scan order; NULL text reads as "".
const columns = `
	id,
	first_name,
	COALESCE(last_name, ''),
	COALESCE(email, ''),
	COALESCE(mobile, ''),
	COALESCE(alternate_mobile, ''),
	company_id,
	lead_status_id,
	lead_owner_id,
	COALESCE(destination, ''),
	COALESCE(source, ''),
	COALESCE(notes, ''),
	created_at,
	updated_at`

// scopeFilter is the WHERE condition for a Scope whose org and owner are
// bound to parameters $1 and $2.
const scopeFilter = `org_id = $1 AND ($2::bigint IS NULL OR lead_owner_id = $2)`

func scan(row pgx.Row) (Contact, error) {
	var c Contact
	err := row.Scan(
		&c.ID,
		&c.FirstName,
		&c.LastName,
		&c.Email,
		&c.Mobile,
		&c.AlternateMobile,
		&c.CompanyID,
		&c.LeadStatusID,
		&c.LeadOwnerID,
		&c.Destination,
		&c.Source,
		&c.Notes,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	return c, err
}

// List returns the contacts in scope, newest first.
func (r *Repository) List(ctx context.Context, s Scope) ([]Contact, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+columns+` FROM contacts WHERE `+scopeFilter+` ORDER BY id DESC`,
		s.OrgID, s.OwnerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]Contact, 0)
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// Get returns a contact in scope, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, s Scope, id int64) (Contact, error) {
	return scan(r.db.QueryRow(ctx,
		`SELECT `+columns+` FROM contacts WHERE `+scopeFilter+` AND id = $3`,
		s.OrgID, s.OwnerID, id,
	))
}

// Create inserts c into orgID and fills in its ID and timestamps.
func (r *Repository) Create(ctx context.Context, orgID int64, c *Contact) error {
	return r.db.QueryRow(ctx,
		`INSERT INTO contacts (
			org_id, first_name, last_name, email, mobile, alternate_mobile, company_id,
			lead_status_id, lead_owner_id, destination, source, notes
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at, updated_at`,
		orgID, c.FirstName, c.LastName, c.Email, c.Mobile, c.AlternateMobile, c.CompanyID,
		c.LeadStatusID, c.LeadOwnerID, c.Destination, c.Source, c.Notes,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

// Update applies p to a contact in scope and returns the result. An owner
// outside the org returns ErrInvalidOwner.
func (r *Repository) Update(ctx context.Context, s Scope, id int64, p Patch) (Contact, error) {
	c, err := scan(r.db.QueryRow(ctx,
		`UPDATE contacts
		SET
			first_name = COALESCE($4, first_name),
			last_name = COALESCE($5, last_name),
			email = COALESCE($6, email),
			mobile = COALESCE($7, mobile),
			alternate_mobile = COALESCE($8, alternate_mobile),
			company_id = COALESCE($9, company_id),
			lead_status_id = COALESCE($10, lead_status_id),
			lead_owner_id = COALESCE($11, lead_owner_id),
			destination = COALESCE($12, destination),
			source = COALESCE($13, source),
			notes = COALESCE($14, notes),
			updated_at = CURRENT_TIMESTAMP
		WHERE `+scopeFilter+` AND id = $3
		RETURNING `+columns,
		s.OrgID, s.OwnerID, id,
		p.FirstName, p.LastName, p.Email, p.Mobile, p.AlternateMobile, p.CompanyID,
		p.LeadStatusID, p.LeadOwnerID, p.Destination, p.Source, p.Notes,
	))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "contacts_lead_owner_same_org_fkey" {
		return Contact{}, ErrInvalidOwner
	}
	return c, err
}

// Delete removes a contact in scope, or returns ErrNotFound.
func (r *Repository) Delete(ctx context.Context, s Scope, id int64) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM contacts WHERE `+scopeFilter+` AND id = $3`,
		s.OrgID, s.OwnerID, id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

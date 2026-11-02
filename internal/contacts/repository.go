package contacts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
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
	return c, err
}

// List returns every contact, newest first.
func (r *Repository) List(ctx context.Context) ([]Contact, error) {
	rows, err := r.db.Query(ctx, `SELECT `+columns+` FROM contacts ORDER BY id DESC`)
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

func (r *Repository) Get(ctx context.Context, id int64) (Contact, error) {
	c, err := scan(r.db.QueryRow(ctx, `SELECT `+columns+` FROM contacts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	return c, err
}

// Create inserts c and fills in its ID and timestamps.
func (r *Repository) Create(ctx context.Context, c *Contact) error {
	return r.db.QueryRow(ctx,
		`INSERT INTO contacts (
			first_name, last_name, email, mobile, alternate_mobile, company_id,
			lead_status_id, lead_owner_id, destination, source, notes
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`,
		c.FirstName, c.LastName, c.Email, c.Mobile, c.AlternateMobile, c.CompanyID,
		c.LeadStatusID, c.LeadOwnerID, c.Destination, c.Source, c.Notes,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

// Update applies p to the contact and returns the result.
func (r *Repository) Update(ctx context.Context, id int64, p Patch) (Contact, error) {
	c, err := scan(r.db.QueryRow(ctx,
		`UPDATE contacts
		SET
			first_name = COALESCE($1, first_name),
			last_name = COALESCE($2, last_name),
			email = COALESCE($3, email),
			mobile = COALESCE($4, mobile),
			alternate_mobile = COALESCE($5, alternate_mobile),
			company_id = COALESCE($6, company_id),
			lead_status_id = COALESCE($7, lead_status_id),
			lead_owner_id = COALESCE($8, lead_owner_id),
			destination = COALESCE($9, destination),
			source = COALESCE($10, source),
			notes = COALESCE($11, notes),
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $12
		RETURNING `+columns,
		p.FirstName, p.LastName, p.Email, p.Mobile, p.AlternateMobile, p.CompanyID,
		p.LeadStatusID, p.LeadOwnerID, p.Destination, p.Source, p.Notes,
		id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	return c, err
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM contacts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

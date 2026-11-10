package tasks

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the only code that touches the tasks table.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Scope limits queries to an org and, when AssigneeID is set, to tasks
// assigned to that user (reps see only their own tasks).
type Scope struct {
	OrgID      int64
	AssigneeID *int64
}

const columns = `id, contact_id, assignee_id, created_by_id, title, description, type, priority,
	status, origin, due_at, completed_at, created_at, updated_at`

const scopeFilter = `org_id = $1 AND ($2::bigint IS NULL OR assignee_id = $2)`

func scan(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ContactID, &t.AssigneeID, &t.CreatedByID, &t.Title, &t.Description,
		&t.Type, &t.Priority, &t.Status, &t.Origin, &t.DueAt, &t.CompletedAt, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, mapFK(err)
}

// mapFK turns same-org foreign-key violations into module errors.
func mapFK(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "tasks_assignee_same_org_fkey":
			return ErrInvalidAssignee
		case "tasks_contact_same_org_fkey":
			return ErrContactNotFound
		}
	}
	return err
}

func (r *Repository) List(ctx context.Context, s Scope, f Filter) ([]Task, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+columns+` FROM tasks WHERE `+scopeFilter+`
			AND ($3 = '' OR status = $3)
			AND ($4::bigint = 0 OR assignee_id = $4)
			AND ($5::bigint = 0 OR contact_id = $5)
			AND (NOT $6 OR (status = 'open' AND due_at < now()))
		ORDER BY (status = 'open') DESC, due_at ASC NULLS LAST, id DESC
		LIMIT $7 OFFSET $8`,
		s.OrgID, s.AssigneeID, f.Status, f.AssigneeID, f.ContactID, f.Overdue, f.Limit, f.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]Task, 0)
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *Repository) Get(ctx context.Context, s Scope, id int64) (Task, error) {
	return scan(r.db.QueryRow(ctx,
		`SELECT `+columns+` FROM tasks WHERE `+scopeFilter+` AND id = $3`, s.OrgID, s.AssigneeID, id))
}

func (r *Repository) Create(ctx context.Context, orgID int64, t Task) (Task, error) {
	return scan(r.db.QueryRow(ctx,
		`INSERT INTO tasks (org_id, contact_id, assignee_id, created_by_id, title, description, type, priority, origin, due_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+columns,
		orgID, t.ContactID, t.AssigneeID, t.CreatedByID, t.Title, t.Description, t.Type, t.Priority, t.Origin, t.DueAt,
	))
}

// CreateWorkflow inserts a workflow task unless the contact already has an
// open one from the same origin, which it returns instead (created false).
func (r *Repository) CreateWorkflow(ctx context.Context, orgID int64, t Task) (Task, bool, error) {
	created, err := scan(r.db.QueryRow(ctx,
		`INSERT INTO tasks (org_id, contact_id, assignee_id, title, description, type, priority, origin, due_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (contact_id, origin) WHERE status = 'open' AND origin <> 'manual' DO NOTHING
		RETURNING `+columns,
		orgID, t.ContactID, t.AssigneeID, t.Title, t.Description, t.Type, t.Priority, t.Origin, t.DueAt,
	))
	if !errors.Is(err, ErrNotFound) {
		return created, err == nil, err
	}
	existing, err := scan(r.db.QueryRow(ctx,
		`SELECT `+columns+` FROM tasks WHERE org_id = $1 AND contact_id = $2 AND origin = $3 AND status = 'open'`,
		orgID, t.ContactID, t.Origin,
	))
	return existing, false, err
}

// Save writes t's editable fields; completed_at follows the status.
func (r *Repository) Save(ctx context.Context, s Scope, t Task) (Task, error) {
	return scan(r.db.QueryRow(ctx,
		`UPDATE tasks SET
			assignee_id = $4, title = $5, description = $6, type = $7, priority = $8, status = $9, due_at = $10,
			completed_at = CASE WHEN $9 = 'done' THEN COALESCE(completed_at, now()) END,
			-- a new due date or a reopened task earns a new overdue reminder
			reminded_at = CASE WHEN due_at IS DISTINCT FROM $10 OR (status <> 'open' AND $9 = 'open') THEN NULL ELSE reminded_at END,
			updated_at = now()
		WHERE `+scopeFilter+` AND id = $3
		RETURNING `+columns,
		s.OrgID, s.AssigneeID, t.ID, t.AssigneeID, t.Title, t.Description, t.Type, t.Priority, t.Status, t.DueAt,
	))
}

func (r *Repository) Delete(ctx context.Context, s Scope, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM tasks WHERE `+scopeFilter+` AND id = $3`, s.OrgID, s.AssigneeID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ClaimOverdue marks up to limit open tasks that became overdue before now
// as reminded and returns them with their org. Claiming and marking in one
// statement means each overdue task is reminded once, even with several
// workers.
func (r *Repository) ClaimOverdue(ctx context.Context, now time.Time, limit int) ([]Task, []int64, error) {
	rows, err := r.db.Query(ctx,
		`WITH due AS (
			SELECT id FROM tasks
			WHERE status = 'open' AND reminded_at IS NULL AND due_at < $1
			ORDER BY due_at LIMIT $2 FOR UPDATE SKIP LOCKED
		)
		UPDATE tasks t SET reminded_at = $1 FROM due WHERE t.id = due.id
		RETURNING t.org_id, `+prefixed("t.", columns), now, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var list []Task
	var orgs []int64
	for rows.Next() {
		var org int64
		var t Task
		if err := rows.Scan(&org, &t.ID, &t.ContactID, &t.AssigneeID, &t.CreatedByID, &t.Title, &t.Description,
			&t.Type, &t.Priority, &t.Status, &t.Origin, &t.DueAt, &t.CompletedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, nil, err
		}
		list, orgs = append(list, t), append(orgs, org)
	}
	return list, orgs, rows.Err()
}

// prefixed qualifies each comma-separated column with p.
func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

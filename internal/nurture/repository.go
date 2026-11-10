package nurture

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the only code that touches the nurture_* tables.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func mapErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "nurture_sequences_org_id_name_key":
			return ErrNameTaken
		case "nurture_sequences_active_trigger_key":
			return ErrTriggerTaken
		case "nurture_enrollments_contact_id_org_id_fkey":
			return ErrContactNotFound
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

const sequenceColumns = `s.id, s.name, s.trigger, s.active, s.stop_on_reply, s.created_at, s.updated_at,
	(SELECT count(*) FROM nurture_enrollments e WHERE e.sequence_id = s.id AND e.status = 'active')`

func (r *Repository) scanSequence(ctx context.Context, row pgx.Row) (Sequence, error) {
	var s Sequence
	if err := row.Scan(&s.ID, &s.Name, &s.Trigger, &s.Active, &s.StopOnReply, &s.CreatedAt, &s.UpdatedAt, &s.ActiveEnrollments); err != nil {
		return Sequence{}, mapErr(err)
	}
	rows, err := r.db.Query(ctx,
		`SELECT delay_minutes, channel, subject, body, whatsapp_template, whatsapp_language, whatsapp_template_params
		FROM nurture_steps WHERE sequence_id = $1 ORDER BY position`, s.ID)
	if err != nil {
		return Sequence{}, err
	}
	s.Steps, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Step, error) {
		var st Step
		err := row.Scan(&st.DelayMinutes, &st.Channel, &st.Subject, &st.Body, &st.WhatsAppTemplate,
			&st.WhatsAppLanguage, &st.WhatsAppTemplateParams)
		return st, err
	})
	return s, err
}

func (r *Repository) Sequences(ctx context.Context, orgID int64) ([]Sequence, error) {
	rows, err := r.db.Query(ctx, `SELECT id FROM nurture_sequences WHERE org_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	list := make([]Sequence, 0, len(ids))
	for _, id := range ids {
		s, err := r.Sequence(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, nil
}

func (r *Repository) Sequence(ctx context.Context, orgID, id int64) (Sequence, error) {
	return r.scanSequence(ctx, r.db.QueryRow(ctx,
		`SELECT `+sequenceColumns+` FROM nurture_sequences s WHERE s.org_id = $1 AND s.id = $2`, orgID, id))
}

// ActiveFor returns the org's active sequence for an automatic trigger.
func (r *Repository) ActiveFor(ctx context.Context, orgID int64, trigger string) (Sequence, error) {
	return r.scanSequence(ctx, r.db.QueryRow(ctx,
		`SELECT `+sequenceColumns+` FROM nurture_sequences s WHERE s.org_id = $1 AND s.trigger = $2 AND s.active`,
		orgID, trigger))
}

// Save inserts (ID 0) or updates a sequence and replaces its steps.
func (r *Repository) Save(ctx context.Context, orgID int64, s Sequence) (Sequence, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if s.ID == 0 {
			if err := tx.QueryRow(ctx,
				`INSERT INTO nurture_sequences (org_id, name, trigger, active, stop_on_reply) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
				orgID, s.Name, s.Trigger, s.Active, s.StopOnReply,
			).Scan(&s.ID); err != nil {
				return mapErr(err)
			}
		} else {
			tag, err := tx.Exec(ctx,
				`UPDATE nurture_sequences SET name = $3, trigger = $4, active = $5, stop_on_reply = $6, updated_at = now()
				WHERE org_id = $1 AND id = $2`,
				orgID, s.ID, s.Name, s.Trigger, s.Active, s.StopOnReply)
			if err != nil {
				return mapErr(err)
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
			if _, err := tx.Exec(ctx, `DELETE FROM nurture_steps WHERE sequence_id = $1`, s.ID); err != nil {
				return err
			}
		}
		for i, st := range s.Steps {
			if _, err := tx.Exec(ctx,
				`INSERT INTO nurture_steps (org_id, sequence_id, position, delay_minutes, channel, subject, body,
					whatsapp_template, whatsapp_language, whatsapp_template_params)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				orgID, s.ID, i, st.DelayMinutes, st.Channel, st.Subject, st.Body,
				st.WhatsAppTemplate, st.WhatsAppLanguage, st.WhatsAppTemplateParams,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Sequence{}, err
	}
	return r.Sequence(ctx, orgID, s.ID)
}

func (r *Repository) Delete(ctx context.Context, orgID, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM nurture_sequences WHERE org_id = $1 AND id = $2`, orgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Enrollments

const enrollmentColumns = `e.id, e.org_id, e.sequence_id, s.name, e.contact_id, e.status, e.stop_reason, e.next_step,
	e.next_run_at, e.attempts, e.last_error, e.enrolled_by_id, e.enrolled_at, e.finished_at`

func scanEnrollment(row pgx.Row) (Enrollment, error) {
	var e Enrollment
	err := row.Scan(&e.ID, &e.orgID, &e.SequenceID, &e.SequenceName, &e.ContactID, &e.Status, &e.StopReason, &e.NextStep,
		&e.NextRunAt, &e.Attempts, &e.LastError, &e.EnrolledByID, &e.EnrolledAt, &e.FinishedAt)
	return e, mapErr(err)
}

// Enroll adds an active enrollment unless the contact already has one in
// the sequence (ErrAlreadyEnrolled).
func (r *Repository) Enroll(ctx context.Context, orgID, sequenceID, contactID int64, by *int64, firstRun time.Time) (Enrollment, error) {
	var id int64
	err := r.db.QueryRow(ctx,
		`INSERT INTO nurture_enrollments (org_id, sequence_id, contact_id, enrolled_by_id, next_run_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (contact_id, sequence_id) WHERE status = 'active' DO NOTHING
		RETURNING id`, orgID, sequenceID, contactID, by, firstRun,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Enrollment{}, ErrAlreadyEnrolled
	}
	if err != nil {
		return Enrollment{}, mapErr(err)
	}
	return r.Enrollment(ctx, orgID, id)
}

func (r *Repository) Enrollment(ctx context.Context, orgID, id int64) (Enrollment, error) {
	return scanEnrollment(r.db.QueryRow(ctx,
		`SELECT `+enrollmentColumns+` FROM nurture_enrollments e JOIN nurture_sequences s ON s.id = e.sequence_id
		WHERE e.org_id = $1 AND e.id = $2`, orgID, id))
}

// Enrollments lists enrollments, newest first; zero filters match all.
func (r *Repository) Enrollments(ctx context.Context, orgID, contactID, sequenceID int64, status string, limit, offset int) ([]Enrollment, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+enrollmentColumns+` FROM nurture_enrollments e JOIN nurture_sequences s ON s.id = e.sequence_id
		WHERE e.org_id = $1 AND ($2::bigint = 0 OR e.contact_id = $2) AND ($3::bigint = 0 OR e.sequence_id = $3)
			AND ($4 = '' OR e.status = $4)
		ORDER BY e.enrolled_at DESC, e.id DESC LIMIT $5 OFFSET $6`,
		orgID, contactID, sequenceID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Enrollment, error) { return scanEnrollment(row) })
}

// StopForContact stops a contact's active enrollments (only those whose
// sequence stops on reply, when onlyStopOnReply) and returns them.
func (r *Repository) StopForContact(ctx context.Context, orgID, contactID int64, reason string, onlyStopOnReply bool) ([]Enrollment, error) {
	rows, err := r.db.Query(ctx,
		`UPDATE nurture_enrollments e SET status = 'stopped', stop_reason = $3, finished_at = now()
		FROM nurture_sequences s
		WHERE s.id = e.sequence_id AND e.org_id = $1 AND e.contact_id = $2 AND e.status = 'active'
			AND (NOT $4 OR s.stop_on_reply)
		RETURNING `+enrollmentColumns,
		orgID, contactID, reason, onlyStopOnReply)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Enrollment, error) { return scanEnrollment(row) })
}

// Finish ends an active enrollment.
func (r *Repository) Finish(ctx context.Context, orgID, id int64, status, reason string) (Enrollment, error) {
	return scanEnrollment(r.db.QueryRow(ctx,
		`UPDATE nurture_enrollments e SET status = $3, stop_reason = $4, finished_at = now()
		FROM nurture_sequences s
		WHERE s.id = e.sequence_id AND e.org_id = $1 AND e.id = $2 AND e.status = 'active'
		RETURNING `+enrollmentColumns, orgID, id, status, reason))
}

// leaseFor is how long a claimed enrollment stays invisible to other
// workers; if the process dies mid-send it is retried after this.
const leaseFor = 10 * time.Minute

// ClaimDue takes up to limit active enrollments due at now, leasing them
// so no other worker sends the same step concurrently.
func (r *Repository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]Enrollment, error) {
	rows, err := r.db.Query(ctx,
		`WITH due AS (
			SELECT id FROM nurture_enrollments
			WHERE status = 'active' AND next_run_at <= $1
			ORDER BY next_run_at LIMIT $2 FOR UPDATE SKIP LOCKED
		)
		UPDATE nurture_enrollments e SET next_run_at = $3
		FROM due, nurture_sequences s
		WHERE e.id = due.id AND s.id = e.sequence_id
		RETURNING `+enrollmentColumns,
		now, limit, now.Add(leaseFor))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Enrollment, error) { return scanEnrollment(row) })
}

// Advance moves to step next at runAt, clearing attempts; lastError notes
// a skipped step.
func (r *Repository) Advance(ctx context.Context, id int64, next int, runAt time.Time, lastError string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE nurture_enrollments SET next_step = $2, next_run_at = $3, attempts = 0, last_error = $4
		WHERE id = $1 AND status = 'active'`, id, next, runAt, lastError)
	return err
}

// Complete finishes an enrollment whose steps are all done.
func (r *Repository) Complete(ctx context.Context, id int64, next int, lastError string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE nurture_enrollments SET next_step = $2, status = 'completed', finished_at = now(), last_error = $3
		WHERE id = $1 AND status = 'active'`, id, next, lastError)
	return err
}

// Retry schedules another attempt at the same step.
func (r *Repository) Retry(ctx context.Context, id int64, attempts int, runAt time.Time, lastError string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE nurture_enrollments SET attempts = $2, next_run_at = $3, last_error = $4 WHERE id = $1 AND status = 'active'`,
		id, attempts, runAt, lastError)
	return err
}

// Reschedule moves the next run without counting an attempt (quiet hours).
func (r *Repository) Reschedule(ctx context.Context, id int64, runAt time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE nurture_enrollments SET next_run_at = $2 WHERE id = $1 AND status = 'active'`, id, runAt)
	return err
}

package contacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// queryer is satisfied by *pgxpool.Pool and pgx.Tx.
type queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// newActivity is an activity to insert.
type newActivity struct {
	OrgID       int64
	ContactID   int64
	Kind        string
	Channel     string // "" for none
	Direction   string // "" for none
	Summary     string
	Body        string
	Metadata    map[string]any
	ExternalID  string
	ActorUserID *int64
	OccurredAt  *time.Time // nil: now
}

// insertActivity adds an activity; with an ExternalID already recorded for
// the channel it does nothing and reports false.
func insertActivity(ctx context.Context, q queryer, a newActivity) (int64, bool, error) {
	meta := a.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return 0, false, err
	}
	var id int64
	err = q.QueryRow(ctx,
		`INSERT INTO contact_activities (
			org_id, contact_id, kind, channel, direction, summary, body, metadata,
			external_id, actor_user_id, occurred_at
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8, NULLIF($9, ''), $10, COALESCE($11, now()))
		ON CONFLICT (org_id, channel, external_id) WHERE external_id IS NOT NULL DO NOTHING
		RETURNING id`,
		a.OrgID, a.ContactID, a.Kind, a.Channel, a.Direction, a.Summary, a.Body, raw,
		a.ExternalID, a.ActorUserID, a.OccurredAt,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// Capture finds the org's contact with in's email or phone, filling in any
// details it lacks, or creates one; and logs the capture and message on the
// timeline. Captures of the same email or phone are serialized, so two
// simultaneous submissions never create two contacts.
func (r *Repository) Capture(ctx context.Context, orgID int64, in CaptureInput) (CaptureResult, error) {
	var res CaptureResult
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var emailKey, phoneKey *string
		if err := tx.QueryRow(ctx,
			`SELECT NULLIF(lower(btrim($1)), ''), crm_phone_key($2)`, in.Email, in.Mobile,
		).Scan(&emailKey, &phoneKey); err != nil {
			return err
		}
		locks := make([]string, 0, 2)
		if emailKey != nil {
			locks = append(locks, "e:"+*emailKey)
		}
		if phoneKey != nil {
			locks = append(locks, "p:"+*phoneKey)
		}
		sort.Strings(locks) // a fixed order cannot deadlock
		for _, key := range locks {
			if _, err := tx.Exec(ctx,
				`SELECT pg_advisory_xact_lock(hashtextextended($1::bigint::text || ':' || $2, 0))`, orgID, key,
			); err != nil {
				return err
			}
		}

		if in.ExternalID != "" {
			err := tx.QueryRow(ctx,
				`SELECT contact_id FROM contact_activities WHERE org_id = $1 AND channel = $2 AND external_id = $3`,
				orgID, in.Channel, in.ExternalID,
			).Scan(&res.ContactID)
			if err == nil {
				res.AlreadyProcessed = true
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}

		err := tx.QueryRow(ctx,
			`SELECT id, lead_owner_id FROM contacts
			WHERE org_id = $1 AND (email_key = $2 OR phone_key = $3)
			ORDER BY (email_key = $2) IS TRUE DESC, id
			LIMIT 1 FOR UPDATE`,
			orgID, emailKey, phoneKey,
		).Scan(&res.ContactID, &res.OwnerID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			res.Created = true
			res.OwnerID = in.OwnerID
			err = tx.QueryRow(ctx,
				`INSERT INTO contacts (org_id, first_name, last_name, email, mobile, destination, source, notes, lead_owner_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				RETURNING id`,
				orgID, in.FirstName, in.LastName, in.Email, in.Mobile, in.Destination, in.Source, in.Notes, in.OwnerID,
			).Scan(&res.ContactID)
			if isOwnerFKError(err) {
				return ErrInvalidOwner
			}
		case err == nil:
			// Fill in only what the contact lacks; never overwrite.
			_, err = tx.Exec(ctx,
				`UPDATE contacts SET
					last_name = COALESCE(NULLIF(last_name, ''), NULLIF($3, '')),
					email = COALESCE(NULLIF(email, ''), NULLIF($4, '')),
					mobile = COALESCE(NULLIF(mobile, ''), NULLIF($5, '')),
					destination = COALESCE(NULLIF(destination, ''), NULLIF($6, '')),
					source = COALESCE(NULLIF(source, ''), NULLIF($7, '')),
					updated_at = now()
				WHERE org_id = $1 AND id = $2`,
				orgID, res.ContactID, in.LastName, in.Email, in.Mobile, in.Destination, in.Source,
			)
		}
		if err != nil {
			return err
		}

		var at *time.Time
		if !in.OccurredAt.IsZero() {
			at = &in.OccurredAt
		}
		meta := map[string]any{"source": in.Source}
		switch {
		case res.Created:
			_, _, err = insertActivity(ctx, tx, newActivity{
				OrgID: orgID, ContactID: res.ContactID, Kind: KindCaptured, Channel: in.Channel,
				Summary: captureSummary("Lead captured", in), Metadata: meta,
				ActorUserID: in.ActorUserID, OccurredAt: at,
			})
		case in.ExternalID == "":
			// A repeat form submission or manual entry (messages from a
			// known contact are just messages).
			_, _, err = insertActivity(ctx, tx, newActivity{
				OrgID: orgID, ContactID: res.ContactID, Kind: KindDuplicateCaptured, Channel: in.Channel,
				Summary: captureSummary("Captured again", in), Metadata: meta,
				ActorUserID: in.ActorUserID, OccurredAt: at,
			})
		}
		if err != nil {
			return err
		}

		if strings.TrimSpace(in.Message) != "" {
			_, _, err = insertActivity(ctx, tx, newActivity{
				OrgID: orgID, ContactID: res.ContactID, Kind: KindMessage, Channel: in.Channel,
				Direction: "inbound", Summary: "Message received", Body: in.Message,
				ExternalID: in.ExternalID, ActorUserID: in.ActorUserID, OccurredAt: at,
			})
		}
		return err
	})
	return res, err
}

func captureSummary(prefix string, in CaptureInput) string {
	s := prefix + " via " + strings.ReplaceAll(in.Channel, "_", " ")
	if in.Source != "" {
		s += " (" + in.Source + ")"
	}
	return s
}

func isOwnerFKError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == "contacts_lead_owner_same_org_fkey"
}

// Facts returns what scoring needs to know about a contact in orgID.
func (r *Repository) Facts(ctx context.Context, orgID, contactID int64) (Facts, error) {
	var f Facts
	err := r.db.QueryRow(ctx,
		`SELECT
			COALESCE(c.source, ''),
			COALESCE(c.destination, ''),
			c.email_key IS NOT NULL,
			c.phone_key IS NOT NULL,
			c.company_id IS NOT NULL,
			c.lead_owner_id,
			ARRAY(SELECT DISTINCT a.channel FROM contact_activities a
			      WHERE a.contact_id = c.id AND a.channel IS NOT NULL AND a.channel <> 'system'
			      ORDER BY 1),
			(SELECT count(*) FROM contact_activities a
			 WHERE a.contact_id = c.id AND a.kind = 'message' AND a.direction = 'inbound'),
			concat_ws(' ', c.notes, (
				SELECT string_agg(m.body, ' ') FROM (
					SELECT a.body FROM contact_activities a
					WHERE a.contact_id = c.id AND a.kind = 'message' AND a.direction = 'inbound'
					ORDER BY a.occurred_at DESC, a.id DESC LIMIT 20
				) m
			)),
			c.first_name,
			COALESCE(c.last_name, ''),
			COALESCE(c.email, ''),
			COALESCE(c.mobile, ''),
			COALESCE(last.body, ''),
			COALESCE(last.channel, ''),
			last.occurred_at
		FROM contacts c
		LEFT JOIN LATERAL (
			SELECT a.body, a.channel, a.occurred_at FROM contact_activities a
			WHERE a.contact_id = c.id AND a.kind = 'message' AND a.direction = 'inbound'
			ORDER BY a.occurred_at DESC, a.id DESC LIMIT 1
		) last ON true
		WHERE c.org_id = $1 AND c.id = $2`,
		orgID, contactID,
	).Scan(&f.Source, &f.Destination, &f.HasEmail, &f.HasMobile, &f.HasCompany, &f.OwnerID,
		&f.Channels, &f.InboundMessages, &f.Text,
		&f.FirstName, &f.LastName, &f.Email, &f.Mobile, &f.LastMessage, &f.LastMessageChannel, &f.LastMessageAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Facts{}, ErrNotFound
	}
	return f, err
}

// AddSystemActivity records something another module did with a contact
// (a task created or completed). It returns ErrNotFound if the contact is
// not in orgID.
func (r *Repository) AddSystemActivity(ctx context.Context, a newActivity) error {
	_, _, err := insertActivity(ctx, r.db, a)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrNotFound
	}
	return err
}

// SaveScore stores a contact's score and, when its temperature changes (or
// it is scored for the first time), logs that on the timeline. It reports
// the previous temperature ("" if none).
func (r *Repository) SaveScore(ctx context.Context, orgID, contactID int64, s Score) (string, error) {
	var previous string
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT temperature FROM lead_scores WHERE org_id = $1 AND contact_id = $2 FOR UPDATE`,
			orgID, contactID,
		).Scan(&previous)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		reasons, err := json.Marshal(nonNil(s.Reasons))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO lead_scores (contact_id, org_id, score, temperature, reasons, method, scored_at)
			VALUES ($2, $1, $3, $4, $5, $6, now())
			ON CONFLICT (contact_id) DO UPDATE SET
				score = EXCLUDED.score, temperature = EXCLUDED.temperature,
				reasons = EXCLUDED.reasons, method = EXCLUDED.method, scored_at = EXCLUDED.scored_at`,
			orgID, contactID, s.Score, s.Temperature, reasons, s.Method,
		)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // contact gone or in another org
			return ErrNotFound
		}
		if err != nil || previous == s.Temperature {
			return err
		}
		summary := fmt.Sprintf("Scored %d (%s)", s.Score, s.Temperature)
		if previous != "" {
			summary = fmt.Sprintf("Score %d: %s → %s", s.Score, previous, s.Temperature)
		}
		_, _, err = insertActivity(ctx, tx, newActivity{
			OrgID: orgID, ContactID: contactID, Kind: KindScoreChanged, Channel: ChannelSystem,
			Summary: summary,
			Metadata: map[string]any{
				"score": s.Score, "from": previous, "to": s.Temperature, "reasons": nonNil(s.Reasons),
			},
		})
		return err
	})
	return previous, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// AssignOwner gives an unowned contact to userID and logs it. It reports
// false, changing nothing, when the contact already has an owner.
func (r *Repository) AssignOwner(ctx context.Context, orgID, contactID, userID int64, reason string) (bool, error) {
	assigned := false
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE contacts SET lead_owner_id = $3, updated_at = now()
			WHERE org_id = $1 AND id = $2 AND lead_owner_id IS NULL`,
			orgID, contactID, userID,
		)
		if isOwnerFKError(err) {
			return ErrInvalidOwner
		}
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		assigned = true
		_, _, err = insertActivity(ctx, tx, newActivity{
			OrgID: orgID, ContactID: contactID, Kind: KindOwnerAssigned, Channel: ChannelSystem,
			Summary:  "Assigned to owner",
			Metadata: map[string]any{"owner_id": userID, "reason": reason},
		})
		return err
	})
	return assigned, err
}

// viewColumns lists View's fields in scan order (contacts c LEFT JOIN
// lead_scores s).
const viewColumns = `
	c.id,
	c.first_name,
	COALESCE(c.last_name, ''),
	COALESCE(c.email, ''),
	COALESCE(c.mobile, ''),
	COALESCE(c.alternate_mobile, ''),
	c.company_id,
	c.lead_status_id,
	c.lead_owner_id,
	COALESCE(c.destination, ''),
	COALESCE(c.source, ''),
	COALESCE(c.notes, ''),
	c.lifecycle_stage,
	c.created_at,
	c.updated_at,
	s.score,
	s.temperature,
	s.reasons,
	s.method,
	s.scored_at,
	c.marketing_opt_out_at`

const viewFrom = ` FROM contacts c LEFT JOIN lead_scores s ON s.contact_id = c.id`

// viewScope is scopeFilter for the c alias.
const viewScope = `c.org_id = $1 AND ($2::bigint IS NULL OR c.lead_owner_id = $2)`

func scanView(row pgx.Row) (View, error) {
	var v View
	var score *int
	var temperature, method *string
	var reasons []byte
	var scoredAt *time.Time
	err := row.Scan(
		&v.ID, &v.FirstName, &v.LastName, &v.Email, &v.Mobile, &v.AlternateMobile,
		&v.CompanyID, &v.LeadStatusID, &v.LeadOwnerID, &v.Destination, &v.Source, &v.Notes,
		&v.LifecycleStage, &v.CreatedAt, &v.UpdatedAt,
		&score, &temperature, &reasons, &method, &scoredAt, &v.MarketingOptOutAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	if score != nil {
		s := &Score{Score: *score, Temperature: *temperature, Method: *method, ScoredAt: scoredAt.UTC()}
		if err := json.Unmarshal(reasons, &s.Reasons); err != nil {
			return View{}, err
		}
		v.Score = s
	}
	return v, nil
}

// GetView returns a contact in scope with its score.
func (r *Repository) GetView(ctx context.Context, s Scope, id int64) (View, error) {
	return scanView(r.db.QueryRow(ctx,
		`SELECT `+viewColumns+viewFrom+` WHERE `+viewScope+` AND c.id = $3`,
		s.OrgID, s.OwnerID, id,
	))
}

// ListViews returns contacts in scope matching f, newest first.
func (r *Repository) ListViews(ctx context.Context, s Scope, f ListFilter) ([]View, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+viewColumns+viewFrom+` WHERE `+viewScope+`
			AND ($3 = '' OR s.temperature = $3)
			AND ($4 = '' OR c.lifecycle_stage = $4)
			AND ($5::bigint = 0 OR c.id < $5)
		ORDER BY c.id DESC
		LIMIT $6`,
		s.OrgID, s.OwnerID, f.Temperature, f.Stage, f.BeforeID, f.Limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]View, 0)
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

const activityColumns = `id, contact_id, kind, channel, direction, summary, body, metadata, actor_user_id, occurred_at`

func scanActivity(row pgx.Row) (Activity, error) {
	var a Activity
	var meta []byte
	err := row.Scan(&a.ID, &a.ContactID, &a.Kind, &a.Channel, &a.Direction, &a.Summary, &a.Body,
		&meta, &a.ActorUserID, &a.OccurredAt)
	a.Metadata = meta
	return a, err
}

// Timeline returns a contact's activities, newest first, before beforeID
// when it is not 0. The caller has already checked the contact is in scope.
func (r *Repository) Timeline(ctx context.Context, orgID, contactID int64, limit int, beforeID int64) ([]Activity, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+activityColumns+` FROM contact_activities
		WHERE org_id = $1 AND contact_id = $2 AND ($3::bigint = 0 OR id < $3)
		ORDER BY occurred_at DESC, id DESC
		LIMIT $4`,
		orgID, contactID, beforeID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]Activity, 0)
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// AddActivity inserts a and returns it as stored.
func (r *Repository) AddActivity(ctx context.Context, a newActivity) (Activity, error) {
	id, _, err := insertActivity(ctx, r.db, a)
	if err != nil {
		return Activity{}, err
	}
	return scanActivity(r.db.QueryRow(ctx,
		`SELECT `+activityColumns+` FROM contact_activities WHERE org_id = $1 AND id = $2`, a.OrgID, id))
}

// SetStage moves a contact in scope to stage, logging the change, and
// returns the contact.
func (r *Repository) SetStage(ctx context.Context, s Scope, id int64, stage string, actor int64) (View, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var from string
		err := tx.QueryRow(ctx,
			`SELECT lifecycle_stage FROM contacts WHERE `+scopeFilter+` AND id = $3 FOR UPDATE`,
			s.OrgID, s.OwnerID, id,
		).Scan(&from)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || from == stage {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE contacts SET lifecycle_stage = $3, updated_at = now() WHERE org_id = $1 AND id = $2`,
			s.OrgID, id, stage,
		); err != nil {
			return err
		}
		_, _, err = insertActivity(ctx, tx, newActivity{
			OrgID: s.OrgID, ContactID: id, Kind: KindLifecycleChanged, Channel: ChannelManual,
			Summary:     fmt.Sprintf("Stage %s → %s", from, stage),
			Metadata:    map[string]any{"from": from, "to": stage},
			ActorUserID: &actor,
		})
		return err
	})
	if err != nil {
		return View{}, err
	}
	return r.GetView(ctx, s, id)
}

// SetStageFrom moves a contact in orgID to stage `to` if its current stage
// is one of `from` (any when empty), logging the change. It reports whether
// the stage changed.
func (r *Repository) SetStageFrom(ctx context.Context, orgID, id int64, to string, from []string, actor *int64) (bool, error) {
	changed := false
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var cur string
		err := tx.QueryRow(ctx,
			`SELECT lifecycle_stage FROM contacts WHERE org_id = $1 AND id = $2 FOR UPDATE`, orgID, id,
		).Scan(&cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || cur == to || (len(from) > 0 && !slices.Contains(from, cur)) {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE contacts SET lifecycle_stage = $3, updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id, to,
		); err != nil {
			return err
		}
		changed = true
		_, _, err = insertActivity(ctx, tx, newActivity{
			OrgID: orgID, ContactID: id, Kind: KindLifecycleChanged, Channel: ChannelSystem,
			Summary:     fmt.Sprintf("Stage %s → %s", cur, to),
			Metadata:    map[string]any{"from": cur, "to": to},
			ActorUserID: actor,
		})
		return err
	})
	return changed, err
}

// Recipient returns what nurture needs to message a contact in orgID.
func (r *Repository) Recipient(ctx context.Context, orgID, id int64) (Recipient, error) {
	var rc Recipient
	err := r.db.QueryRow(ctx,
		`SELECT c.first_name, COALESCE(c.last_name, ''), COALESCE(c.email_key, ''), COALESCE(c.phone_key, ''),
			c.lifecycle_stage, c.marketing_opt_out_at IS NOT NULL, c.lead_owner_id,
			(SELECT max(a.occurred_at) FROM contact_activities a
			 WHERE a.contact_id = c.id AND a.kind = 'message' AND a.direction = 'inbound' AND a.channel = 'whatsapp')
		FROM contacts c WHERE c.org_id = $1 AND c.id = $2`, orgID, id,
	).Scan(&rc.FirstName, &rc.LastName, &rc.Email, &rc.PhoneKey, &rc.LifecycleStage, &rc.OptedOut, &rc.OwnerID,
		&rc.LastWhatsAppInboundAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recipient{}, ErrNotFound
	}
	return rc, err
}

// OptOut records that a contact opted out of marketing, once.
func (r *Repository) OptOut(ctx context.Context, orgID, id int64, via string) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE contacts SET marketing_opt_out_at = now(), updated_at = now()
			WHERE org_id = $1 AND id = $2 AND marketing_opt_out_at IS NULL`, orgID, id)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, _, err = insertActivity(ctx, tx, newActivity{
			OrgID: orgID, ContactID: id, Kind: KindOptedOut, Channel: ChannelSystem,
			Summary: "Opted out of marketing via " + via, Metadata: map[string]any{"via": via},
		})
		return err
	})
}

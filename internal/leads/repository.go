package leads

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the only code that touches the lead_* and whatsapp_accounts
// tables. Every query is scoped to an org.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func constraintError(err error, name string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == name
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// SeedDefaults creates the default scoring rules for a new org inside tx.
func SeedDefaults(ctx context.Context, tx pgx.Tx, orgID int64) error {
	for _, r := range defaultScoringRules {
		if _, err := tx.Exec(ctx,
			`INSERT INTO lead_scoring_rules (org_id, name, criterion, value, points) VALUES ($1, $2, $3, $4, $5)`,
			orgID, r.Name, r.Criterion, r.Value, r.Points,
		); err != nil {
			return err
		}
	}
	return nil
}

// Settings returns the org's lead settings, or the defaults.
func (r *Repository) Settings(ctx context.Context, orgID int64) (Settings, error) {
	var s Settings
	err := r.db.QueryRow(ctx,
		`SELECT hot_threshold, warm_threshold, default_owner_id, hot_followup_minutes, updated_at FROM lead_settings WHERE org_id = $1`,
		orgID,
	).Scan(&s.HotThreshold, &s.WarmThreshold, &s.DefaultOwnerID, &s.HotFollowupMinutes, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSettings, nil
	}
	return s, err
}

// SaveSettings stores the org's lead settings.
func (r *Repository) SaveSettings(ctx context.Context, orgID int64, s Settings) (Settings, error) {
	err := r.db.QueryRow(ctx,
		`INSERT INTO lead_settings (org_id, hot_threshold, warm_threshold, default_owner_id, hot_followup_minutes, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (org_id) DO UPDATE SET
			hot_threshold = EXCLUDED.hot_threshold, warm_threshold = EXCLUDED.warm_threshold,
			default_owner_id = EXCLUDED.default_owner_id, hot_followup_minutes = EXCLUDED.hot_followup_minutes,
			updated_at = EXCLUDED.updated_at
		RETURNING hot_threshold, warm_threshold, default_owner_id, hot_followup_minutes, updated_at`,
		orgID, s.HotThreshold, s.WarmThreshold, s.DefaultOwnerID, s.HotFollowupMinutes,
	).Scan(&s.HotThreshold, &s.WarmThreshold, &s.DefaultOwnerID, &s.HotFollowupMinutes, &s.UpdatedAt)
	if constraintError(err, "lead_settings_default_owner_same_org_fkey") {
		return Settings{}, ErrInvalidOwner
	}
	return s, err
}

// Sources

const sourceColumns = `id, name, channel, public_key, active, created_at, updated_at`

func scanSource(row pgx.Row) (Source, error) {
	var s Source
	err := row.Scan(&s.ID, &s.Name, &s.Channel, &s.PublicKey, &s.Active, &s.CreatedAt, &s.UpdatedAt)
	return s, notFound(err)
}

// newPublicKey returns a random, URL-safe source key.
func newPublicKey() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "lsk_" + base64.RawURLEncoding.EncodeToString(b), nil
}

func (r *Repository) Sources(ctx context.Context, orgID int64) ([]Source, error) {
	return list(ctx, r.db, scanSource,
		`SELECT `+sourceColumns+` FROM lead_sources WHERE org_id = $1 ORDER BY name`, orgID)
}

func (r *Repository) CreateSource(ctx context.Context, orgID int64, name, channel string) (Source, error) {
	key, err := newPublicKey()
	if err != nil {
		return Source{}, err
	}
	s, err := scanSource(r.db.QueryRow(ctx,
		`INSERT INTO lead_sources (org_id, name, channel, public_key) VALUES ($1, $2, $3, $4)
		RETURNING `+sourceColumns,
		orgID, name, channel, key,
	))
	if constraintError(err, "lead_sources_org_id_name_key") {
		return Source{}, ErrNameTaken
	}
	return s, err
}

// UpdateSource changes a source's name and/or active flag (nil: unchanged)
// and, when rotate is true, issues a new public key.
func (r *Repository) UpdateSource(ctx context.Context, orgID, id int64, name *string, active *bool, rotate bool) (Source, error) {
	key := ""
	if rotate {
		var err error
		if key, err = newPublicKey(); err != nil {
			return Source{}, err
		}
	}
	s, err := scanSource(r.db.QueryRow(ctx,
		`UPDATE lead_sources SET
			name = COALESCE($3, name),
			active = COALESCE($4, active),
			public_key = COALESCE(NULLIF($5, ''), public_key),
			updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING `+sourceColumns,
		orgID, id, name, active, key,
	))
	if constraintError(err, "lead_sources_org_id_name_key") {
		return Source{}, ErrNameTaken
	}
	return s, err
}

// SourceByKey finds an active source by its public key, in any org; the
// key is what identifies the org for public form submissions.
func (r *Repository) SourceByKey(ctx context.Context, key string) (Source, int64, error) {
	var orgID int64
	var s Source
	err := r.db.QueryRow(ctx,
		`SELECT org_id, `+sourceColumns+` FROM lead_sources WHERE public_key = $1 AND active`, key,
	).Scan(&orgID, &s.ID, &s.Name, &s.Channel, &s.PublicKey, &s.Active, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, 0, ErrSourceInactive
	}
	return s, orgID, err
}

// WhatsApp accounts

const waColumns = `id, org_id, phone_number_id, display_phone, source_name, created_at`

func scanWA(row pgx.Row) (WhatsAppAccount, error) {
	var a WhatsAppAccount
	err := row.Scan(&a.ID, &a.OrgID, &a.PhoneNumberID, &a.DisplayPhone, &a.SourceName, &a.CreatedAt)
	return a, notFound(err)
}

func (r *Repository) WhatsAppAccounts(ctx context.Context, orgID int64) ([]WhatsAppAccount, error) {
	return list(ctx, r.db, scanWA,
		`SELECT `+waColumns+` FROM whatsapp_accounts WHERE org_id = $1 ORDER BY id`, orgID)
}

func (r *Repository) CreateWhatsAppAccount(ctx context.Context, orgID int64, a WhatsAppAccount) (WhatsAppAccount, error) {
	a, err := scanWA(r.db.QueryRow(ctx,
		`INSERT INTO whatsapp_accounts (org_id, phone_number_id, display_phone, source_name)
		VALUES ($1, $2, $3, $4) RETURNING `+waColumns,
		orgID, a.PhoneNumberID, a.DisplayPhone, a.SourceName,
	))
	if constraintError(err, "whatsapp_accounts_phone_number_id_key") {
		return WhatsAppAccount{}, ErrPhoneNumberTaken
	}
	return a, err
}

func (r *Repository) DeleteWhatsAppAccount(ctx context.Context, orgID, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM whatsapp_accounts WHERE org_id = $1 AND id = $2`, orgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// WhatsAppAccountByPhoneNumberID finds the account (and so the org) an
// inbound webhook message was sent to.
func (r *Repository) WhatsAppAccountByPhoneNumberID(ctx context.Context, phoneNumberID string) (WhatsAppAccount, error) {
	return scanWA(r.db.QueryRow(ctx,
		`SELECT `+waColumns+` FROM whatsapp_accounts WHERE phone_number_id = $1`, phoneNumberID))
}

// Scoring rules

const scoringColumns = `id, name, criterion, value, points, active, created_at, updated_at`

func scanScoring(row pgx.Row) (ScoringRule, error) {
	var s ScoringRule
	err := row.Scan(&s.ID, &s.Name, &s.Criterion, &s.Value, &s.Points, &s.Active, &s.CreatedAt, &s.UpdatedAt)
	return s, notFound(err)
}

func (r *Repository) ScoringRules(ctx context.Context, orgID int64) ([]ScoringRule, error) {
	return list(ctx, r.db, scanScoring,
		`SELECT `+scoringColumns+` FROM lead_scoring_rules WHERE org_id = $1 ORDER BY id`, orgID)
}

func (r *Repository) ScoringRule(ctx context.Context, orgID, id int64) (ScoringRule, error) {
	return scanScoring(r.db.QueryRow(ctx,
		`SELECT `+scoringColumns+` FROM lead_scoring_rules WHERE org_id = $1 AND id = $2`, orgID, id))
}

func (r *Repository) CreateScoringRule(ctx context.Context, orgID int64, s ScoringRule) (ScoringRule, error) {
	return scanScoring(r.db.QueryRow(ctx,
		`INSERT INTO lead_scoring_rules (org_id, name, criterion, value, points, active)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+scoringColumns,
		orgID, s.Name, s.Criterion, s.Value, s.Points, s.Active,
	))
}

func (r *Repository) UpdateScoringRule(ctx context.Context, orgID int64, s ScoringRule) (ScoringRule, error) {
	return scanScoring(r.db.QueryRow(ctx,
		`UPDATE lead_scoring_rules
		SET name = $3, criterion = $4, value = $5, points = $6, active = $7, updated_at = now()
		WHERE org_id = $1 AND id = $2 RETURNING `+scoringColumns,
		orgID, s.ID, s.Name, s.Criterion, s.Value, s.Points, s.Active,
	))
}

func (r *Repository) DeleteScoringRule(ctx context.Context, orgID, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM lead_scoring_rules WHERE org_id = $1 AND id = $2`, orgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Assignment rules

const assignmentColumns = `id, name, position, criterion, value, active, created_at, updated_at,
	ARRAY(SELECT m.user_id FROM lead_assignment_rule_members m WHERE m.rule_id = lead_assignment_rules.id ORDER BY m.user_id)`

func scanAssignment(row pgx.Row) (AssignmentRule, error) {
	var a AssignmentRule
	err := row.Scan(&a.ID, &a.Name, &a.Position, &a.Criterion, &a.Value, &a.Active, &a.CreatedAt, &a.UpdatedAt, &a.UserIDs)
	return a, notFound(err)
}

// AssignmentRules returns the org's rules in evaluation order.
func (r *Repository) AssignmentRules(ctx context.Context, orgID int64) ([]AssignmentRule, error) {
	return list(ctx, r.db, scanAssignment,
		`SELECT `+assignmentColumns+` FROM lead_assignment_rules WHERE org_id = $1 ORDER BY position, id`, orgID)
}

func (r *Repository) AssignmentRule(ctx context.Context, orgID, id int64) (AssignmentRule, error) {
	return scanAssignment(r.db.QueryRow(ctx,
		`SELECT `+assignmentColumns+` FROM lead_assignment_rules WHERE org_id = $1 AND id = $2`, orgID, id))
}

// SaveAssignmentRule inserts a (ID 0) or updates it, replacing its members.
// A member outside the org returns ErrInvalidOwner.
func (r *Repository) SaveAssignmentRule(ctx context.Context, orgID int64, a AssignmentRule) (AssignmentRule, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if a.ID == 0 {
			if err := tx.QueryRow(ctx,
				`INSERT INTO lead_assignment_rules (org_id, name, position, criterion, value, active)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
				orgID, a.Name, a.Position, a.Criterion, a.Value, a.Active,
			).Scan(&a.ID); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx,
				`UPDATE lead_assignment_rules
				SET name = $3, position = $4, criterion = $5, value = $6, active = $7, updated_at = now()
				WHERE org_id = $1 AND id = $2`,
				orgID, a.ID, a.Name, a.Position, a.Criterion, a.Value, a.Active,
			)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
			if _, err := tx.Exec(ctx, `DELETE FROM lead_assignment_rule_members WHERE rule_id = $1`, a.ID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO lead_assignment_rule_members (rule_id, org_id, user_id)
			SELECT $1, $2, unnest($3::bigint[])`,
			a.ID, orgID, a.UserIDs,
		)
		if constraintError(err, "lead_assignment_rule_members_user_id_org_id_fkey") {
			return ErrInvalidOwner
		}
		return err
	})
	if err != nil {
		return AssignmentRule{}, err
	}
	return r.AssignmentRule(ctx, orgID, a.ID)
}

func (r *Repository) DeleteAssignmentRule(ctx context.Context, orgID, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM lead_assignment_rules WHERE org_id = $1 AND id = $2`, orgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// NextMember advances a rule's round-robin cursor and returns the member
// whose turn it is (members in user id order).
func (r *Repository) NextMember(ctx context.Context, orgID int64, rule AssignmentRule) (int64, error) {
	var turn int64
	err := r.db.QueryRow(ctx,
		`UPDATE lead_assignment_rules SET rr_cursor = rr_cursor + 1
		WHERE org_id = $1 AND id = $2 RETURNING rr_cursor - 1`,
		orgID, rule.ID,
	).Scan(&turn)
	if err != nil {
		return 0, notFound(err)
	}
	return rule.UserIDs[turn%int64(len(rule.UserIDs))], nil
}

// list runs a query and scans every row with scan.
func list[T any](ctx context.Context, db *pgxpool.Pool, scan func(pgx.Row) (T, error), sql string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]T, 0)
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

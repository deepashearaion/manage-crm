package deals

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the only code that writes deals, deal_stages and
// deal_stage_history (reports reads them).
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Scope limits queries to an org and, when OwnerID is set, to that user's deals.
type Scope struct {
	OrgID   int64
	OwnerID *int64
}

func constraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// mapErr turns constraint violations into module errors.
func mapErr(err error) error {
	switch constraint(err) {
	case "":
		return err
	case "deals_owner_same_org_fkey":
		return ErrInvalidOwner
	case "deals_contact_same_org_fkey":
		return ErrContactNotFound
	case "deals_stage_same_org_fkey":
		return ErrStageInUse // deleting a stage that deals use
	case "deal_stages_org_id_name_key":
		return ErrStageNameTaken
	}
	return err
}

// Stages

const stageColumns = `s.id, s.name, s.position, s.kind, s.probability, s.guidance, s.required_fields, s.created_at, s.updated_at`

func scanStage(row pgx.Row, extra ...any) (Stage, error) {
	var s Stage
	err := row.Scan(append([]any{&s.ID, &s.Name, &s.Position, &s.Kind, &s.Probability, &s.Guidance,
		&s.RequiredFields, &s.CreatedAt, &s.UpdatedAt}, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stage{}, ErrStageNotFound
	}
	return s, mapErr(err)
}

// Stages lists the org's stages in pipeline order with the open deals in
// scope in each.
func (r *Repository) Stages(ctx context.Context, sc Scope) ([]Stage, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+stageColumns+`, count(d.id), COALESCE(sum(crm_deal_value_paise(d.amount_paise, d.billing_type, d.billing_period)), 0)
		FROM deal_stages s
		LEFT JOIN deals d ON d.stage_id = s.id AND d.status = 'open'
			AND ($2::bigint IS NULL OR d.owner_id = $2)
		WHERE s.org_id = $1
		GROUP BY s.id
		ORDER BY s.position, s.id`,
		sc.OrgID, sc.OwnerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]Stage, 0)
	for rows.Next() {
		s, err := scanStageCounts(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// scanStageCounts scans a stage followed by its open deal count and value.
func scanStageCounts(row pgx.Row) (Stage, error) {
	var s Stage
	var count int
	var value int64
	s, err := scanStage(row, &count, &value)
	s.OpenDeals, s.OpenValuePaise = count, value
	return s, err
}

func (r *Repository) Stage(ctx context.Context, orgID, id int64) (Stage, error) {
	return scanStage(r.db.QueryRow(ctx, `SELECT `+stageColumns+` FROM deal_stages s WHERE s.org_id = $1 AND s.id = $2`, orgID, id))
}

// FirstOpenStage is where new deals start by default.
func (r *Repository) FirstOpenStage(ctx context.Context, orgID int64) (Stage, error) {
	return scanStage(r.db.QueryRow(ctx,
		`SELECT `+stageColumns+` FROM deal_stages s WHERE s.org_id = $1 AND s.kind = 'open' ORDER BY s.position, s.id LIMIT 1`, orgID))
}

func (r *Repository) CreateStage(ctx context.Context, orgID int64, s Stage) (Stage, error) {
	return scanStage(r.db.QueryRow(ctx,
		`INSERT INTO deal_stages (org_id, name, position, kind, probability, guidance, required_fields)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, name, position, kind, probability, guidance, required_fields, created_at, updated_at`,
		orgID, s.Name, s.Position, s.Kind, s.Probability, s.Guidance, s.RequiredFields,
	))
}

func (r *Repository) UpdateStage(ctx context.Context, orgID int64, s Stage) (Stage, error) {
	return scanStage(r.db.QueryRow(ctx,
		`UPDATE deal_stages SET name = $3, position = $4, probability = $5, guidance = $6, required_fields = $7, updated_at = now()
		WHERE org_id = $1 AND id = $2
		RETURNING id, name, position, kind, probability, guidance, required_fields, created_at, updated_at`,
		orgID, s.ID, s.Name, s.Position, s.Probability, s.Guidance, s.RequiredFields,
	))
}

// DeleteStage removes a stage no deal uses, keeping at least one stage of
// each kind. The org's stages are locked so two deletions cannot race.
func (r *Repository) DeleteStage(ctx context.Context, orgID, id int64) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, kind FROM deal_stages WHERE org_id = $1 ORDER BY id FOR UPDATE`, orgID)
		if err != nil {
			return err
		}
		kinds := map[string]int{}
		kind := ""
		for rows.Next() {
			var sid int64
			var k string
			if err := rows.Scan(&sid, &k); err != nil {
				rows.Close()
				return err
			}
			kinds[k]++
			if sid == id {
				kind = k
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if kind == "" {
			return ErrStageNotFound
		}
		if kinds[kind] == 1 {
			return ErrLastOutcomeStage
		}
		_, err = tx.Exec(ctx, `DELETE FROM deal_stages WHERE org_id = $1 AND id = $2`, orgID, id)
		return mapErr(err)
	})
}

// Deals

const dealColumns = `d.id, d.title, d.description, d.contact_id, d.owner_id, d.stage_id, s.name, d.status,
	d.amount_paise, d.billing_type, d.billing_period, crm_deal_value_paise(d.amount_paise, d.billing_type, d.billing_period),
	to_char(d.expected_close_date, 'YYYY-MM-DD'), d.closed_at, d.lost_reason, d.stage_entered_at,
	d.created_at, d.updated_at, s.probability`

const dealFrom = ` FROM deals d JOIN deal_stages s ON s.id = d.stage_id`

const dealScope = `d.org_id = $1 AND ($2::bigint IS NULL OR d.owner_id = $2)`

func scanDeal(row pgx.Row) (Deal, error) {
	var d Deal
	err := row.Scan(&d.ID, &d.Title, &d.Description, &d.ContactID, &d.OwnerID, &d.StageID, &d.StageName, &d.Status,
		&d.AmountPaise, &d.BillingType, &d.BillingPeriod, &d.ValuePaise,
		&d.ExpectedCloseDate, &d.ClosedAt, &d.LostReason, &d.StageEnteredAt,
		&d.CreatedAt, &d.UpdatedAt, &d.stageProbability)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deal{}, ErrNotFound
	}
	return d, mapErr(err)
}

func (r *Repository) List(ctx context.Context, sc Scope, f Filter) ([]Deal, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+dealColumns+dealFrom+` WHERE `+dealScope+`
			AND ($3 = '' OR d.status = $3)
			AND ($4::bigint = 0 OR d.stage_id = $4)
			AND ($5::bigint = 0 OR d.owner_id = $5)
			AND ($6::bigint = 0 OR d.contact_id = $6)
		ORDER BY s.position, d.expected_close_date ASC NULLS LAST, d.id DESC
		LIMIT $7 OFFSET $8`,
		sc.OrgID, sc.OwnerID, f.Status, f.StageID, f.OwnerID, f.ContactID, f.Limit, f.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]Deal, 0)
	for rows.Next() {
		d, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func (r *Repository) Get(ctx context.Context, sc Scope, id int64) (Deal, error) {
	return scanDeal(r.db.QueryRow(ctx, `SELECT `+dealColumns+dealFrom+` WHERE `+dealScope+` AND d.id = $3`,
		sc.OrgID, sc.OwnerID, id))
}

// Create inserts a deal in an open stage and records its first stage.
func (r *Repository) Create(ctx context.Context, orgID int64, d Deal, by int64) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO deals (org_id, title, description, contact_id, owner_id, stage_id, status,
				amount_paise, billing_type, billing_period, expected_close_date)
			VALUES ($1, $2, $3, $4, $5, $6, 'open', $7, $8, $9, $10::date)
			RETURNING id`,
			orgID, d.Title, d.Description, d.ContactID, d.OwnerID, d.StageID,
			d.AmountPaise, d.BillingType, d.BillingPeriod, d.ExpectedCloseDate,
		).Scan(&id); err != nil {
			return mapErr(err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO deal_stage_history (org_id, deal_id, to_stage_id, changed_by_id) VALUES ($1, $2, $3, $4)`,
			orgID, id, d.StageID, by)
		return err
	})
	return id, err
}

// Save writes a deal's editable fields (not its stage).
func (r *Repository) Save(ctx context.Context, sc Scope, d Deal) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE deals d SET title = $4, description = $5, contact_id = $6, owner_id = $7, amount_paise = $8,
			billing_type = $9, billing_period = $10, expected_close_date = $11::date, updated_at = now()
		WHERE `+dealScope+` AND d.id = $3`,
		sc.OrgID, sc.OwnerID, d.ID, d.Title, d.Description, d.ContactID, d.OwnerID, d.AmountPaise,
		d.BillingType, d.BillingPeriod, d.ExpectedCloseDate,
	)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return mapErr(err)
}

// Move puts a deal in another stage: status follows the stage kind,
// closed_at is set when the outcome changes and cleared when it reopens,
// and the change is recorded in the history.
func (r *Repository) Move(ctx context.Context, sc Scope, id int64, to Stage, lostReason string, by int64) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		var from int64
		err := tx.QueryRow(ctx, `SELECT d.stage_id FROM deals d WHERE `+dealScope+` AND d.id = $3 FOR UPDATE`,
			sc.OrgID, sc.OwnerID, id).Scan(&from)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE deals SET stage_id = $3, stage_entered_at = now(), updated_at = now(),
				closed_at = CASE WHEN $4 = 'open' THEN NULL WHEN status = $4 THEN closed_at ELSE now() END,
				status = $4, lost_reason = $5
			WHERE org_id = $1 AND id = $2`,
			sc.OrgID, id, to.ID, to.Kind, lostReason,
		); err != nil {
			return mapErr(err)
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO deal_stage_history (org_id, deal_id, from_stage_id, to_stage_id, changed_by_id)
			VALUES ($1, $2, $3, $4, $5)`, sc.OrgID, id, from, to.ID, by)
		return err
	})
}

func (r *Repository) Delete(ctx context.Context, sc Scope, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM deals d WHERE `+dealScope+` AND d.id = $3`, sc.OrgID, sc.OwnerID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (r *Repository) History(ctx context.Context, orgID, dealID int64) ([]HistoryEntry, error) {
	rows, err := r.db.Query(ctx,
		`SELECT h.from_stage_id, f.name, h.to_stage_id, t.name, h.changed_by_id, h.changed_at
		FROM deal_stage_history h
		LEFT JOIN deal_stages f ON f.id = h.from_stage_id
		JOIN deal_stages t ON t.id = h.to_stage_id
		WHERE h.org_id = $1 AND h.deal_id = $2
		ORDER BY h.changed_at, h.id`, orgID, dealID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (HistoryEntry, error) {
		var h HistoryEntry
		err := row.Scan(&h.FromStageID, &h.FromStage, &h.ToStageID, &h.ToStage, &h.ChangedByID, &h.ChangedAt)
		return h, err
	})
}

// OtherLiveDeals counts the contact's open or won deals other than dealID.
func (r *Repository) OtherLiveDeals(ctx context.Context, orgID, contactID, dealID int64) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM deals WHERE org_id = $1 AND contact_id = $2 AND id <> $3 AND status IN ('open', 'won')`,
		orgID, contactID, dealID).Scan(&n)
	return n, err
}

// ForecastRows returns the open deals in scope and the deals won in
// [from, to).
func (r *Repository) ForecastRows(ctx context.Context, sc Scope, ownerID int64, from, to time.Time) ([]openDeal, []wonDeal, error) {
	rows, err := r.db.Query(ctx,
		`SELECT crm_deal_value_paise(d.amount_paise, d.billing_type, d.billing_period), s.probability, d.stage_entered_at,
			to_char(d.expected_close_date, 'YYYY-MM-DD')`+dealFrom+`
		WHERE `+dealScope+` AND d.status = 'open' AND ($3::bigint = 0 OR d.owner_id = $3)`,
		sc.OrgID, sc.OwnerID, ownerID)
	if err != nil {
		return nil, nil, err
	}
	open, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (openDeal, error) {
		var o openDeal
		err := row.Scan(&o.ValuePaise, &o.StageProbability, &o.StageEnteredAt, &o.ExpectedClose)
		return o, err
	})
	if err != nil {
		return nil, nil, err
	}
	rows, err = r.db.Query(ctx,
		`SELECT crm_deal_value_paise(d.amount_paise, d.billing_type, d.billing_period), d.closed_at FROM deals d
		WHERE `+dealScope+` AND d.status = 'won' AND ($3::bigint = 0 OR d.owner_id = $3)
			AND d.closed_at >= $4 AND d.closed_at < $5`,
		sc.OrgID, sc.OwnerID, ownerID, from, to)
	if err != nil {
		return nil, nil, err
	}
	won, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (wonDeal, error) {
		var w wonDeal
		err := row.Scan(&w.ValuePaise, &w.ClosedAt)
		return w, err
	})
	return open, won, err
}

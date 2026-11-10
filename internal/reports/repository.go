package reports

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Every query takes the org ($1) and an optional owner ($2: a rep sees only
// the deals, contacts and invoices they own).

const dealValue = `crm_deal_value_paise(d.amount_paise, d.billing_type, d.billing_period)`

// newCustomersSQL counts contacts whose first-ever won deal closed in
// [$3, $4); with an owner, only when that deal is theirs.
const newCustomersSQL = `
	SELECT count(*) FROM (
		SELECT DISTINCT ON (d.contact_id) d.contact_id, d.closed_at, d.owner_id
		FROM deals d
		WHERE d.org_id = $1 AND d.status = 'won' AND d.contact_id IS NOT NULL
		ORDER BY d.contact_id, d.closed_at, d.id
	) first_win
	WHERE first_win.closed_at >= $3 AND first_win.closed_at < $4
	  AND ($2::bigint IS NULL OR first_win.owner_id = $2)`

// startOfTodayIST is midnight IST today, as an instant.
func startOfTodayIST(now time.Time) time.Time {
	t := now.In(ist)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, ist)
}

// SalesDashboard computes the legacy dashboard: open, won and lost totals
// over all time, ARR and MRR, and today's new customers.
func (r *Repository) SalesDashboard(ctx context.Context, orgID int64, ownerID *int64, now time.Time) (SalesDashboard, error) {
	var d SalesDashboard
	var openValue, won, lost, arr int64
	today := startOfTodayIST(now)
	err := r.db.QueryRow(ctx, `
		WITH v AS (
			SELECT d.status, d.billing_type, `+dealValue+` AS value
			FROM deals d
			WHERE d.org_id = $1 AND ($2::bigint IS NULL OR d.owner_id = $2)
		)
		SELECT
			COUNT(*) FILTER (WHERE status = 'open'),
			COALESCE(SUM(value) FILTER (WHERE status = 'open'), 0),
			COALESCE(SUM(value) FILTER (WHERE status = 'won'), 0),
			COALESCE(SUM(value) FILTER (WHERE status = 'lost'), 0),
			(`+newCustomersSQL+`),
			COALESCE(SUM(value) FILTER (WHERE status = 'won' AND billing_type = 'recurring'), 0)
		FROM v`,
		orgID, ownerID, today, today.AddDate(0, 0, 1),
	).Scan(&d.TotalOpenDeals, &openValue, &won, &lost, &d.TotalNewCustomers, &arr)
	d.TotalOpenDealValue = rupees(openValue)
	d.TotalWon = rupees(won)
	d.TotalLoss = rupees(lost)
	d.TotalARR = rupees(arr)
	d.TotalMRR = rupees(arr) / 12
	return d, err
}

// rupees converts paise to the rupee amounts the legacy dashboard returns.
func rupees(paise int64) float64 { return float64(paise) / 100 }

// Dashboard computes the v1 dashboard for period p.
func (r *Repository) Dashboard(ctx context.Context, orgID int64, ownerID *int64, p Period) (Dashboard, error) {
	d := Dashboard{Period: p}
	var cycle *float64
	err := r.db.QueryRow(ctx, `
		WITH v AS (
			SELECT d.status, d.billing_type, d.closed_at, d.created_at, `+dealValue+` AS value,
				d.closed_at >= $3 AND d.closed_at < $4 AS in_period
			FROM deals d
			WHERE d.org_id = $1 AND ($2::bigint IS NULL OR d.owner_id = $2)
		)
		SELECT
			COUNT(*) FILTER (WHERE status = 'open'),
			COALESCE(SUM(value) FILTER (WHERE status = 'open'), 0),
			COUNT(*) FILTER (WHERE status = 'won' AND in_period),
			COALESCE(SUM(value) FILTER (WHERE status = 'won' AND in_period), 0),
			COUNT(*) FILTER (WHERE status = 'lost' AND in_period),
			COALESCE(SUM(value) FILTER (WHERE status = 'lost' AND in_period), 0),
			AVG(EXTRACT(EPOCH FROM closed_at - created_at) / 86400) FILTER (WHERE status = 'won' AND in_period),
			COALESCE(SUM(value) FILTER (WHERE status = 'won' AND billing_type = 'recurring'), 0),
			(`+newCustomersSQL+`),
			(SELECT count(*) FROM contacts c
			 WHERE c.org_id = $1 AND ($2::bigint IS NULL OR c.lead_owner_id = $2)
			   AND c.created_at >= $3 AND c.created_at < $4),
			(SELECT count(*) FROM contacts c JOIN lead_scores s ON s.contact_id = c.id
			 WHERE c.org_id = $1 AND ($2::bigint IS NULL OR c.lead_owner_id = $2)
			   AND s.temperature = 'hot' AND c.lifecycle_stage NOT IN ('customer', 'lost')),
			(SELECT COALESCE(sum(i.total_paise), 0) FROM invoices i
			 WHERE i.org_id = $1 AND ($2::bigint IS NULL OR i.owner_id = $2) AND i.status = 'issued'
			   AND i.issue_date >= $5::date AND i.issue_date <= $6::date)
		FROM v`,
		orgID, ownerID, p.start, p.end, p.From, p.To,
	).Scan(&d.OpenDeals.Count, &d.OpenDeals.ValuePaise, &d.Won.Count, &d.Won.ValuePaise, &d.Lost.Count, &d.Lost.ValuePaise,
		&cycle, &d.ARRPaise, &d.NewCustomers, &d.NewLeads, &d.HotLeads, &d.InvoicedPaise)
	if err != nil {
		return Dashboard{}, err
	}
	d.MRRPaise = d.ARRPaise / 12
	d.WinRatePercent = percent(d.Won.Count, d.Won.Count+d.Lost.Count)
	if d.Won.Count > 0 {
		d.AvgDealSizePaise = d.Won.ValuePaise / int64(d.Won.Count)
	}
	if cycle != nil {
		d.AvgSalesCycleDays = float64(int(*cycle*10+0.5)) / 10
	}
	return d, nil
}

// funnelStages are the lifecycle stages in order (lost is reported apart).
var funnelStages = []string{"lead", "mql", "sql", "opportunity", "customer"}

// Funnel follows the contacts created in p through the lifecycle stages.
// A contact counts as having reached every stage up to the furthest one it
// has ever been in (its current stage or any stage on its timeline).
func (r *Repository) Funnel(ctx context.Context, orgID int64, ownerID *int64, p Period) (Funnel, error) {
	f := Funnel{Period: p}
	rows, err := r.db.Query(ctx, `
		WITH cohort AS (
			SELECT c.id, c.lifecycle_stage FROM contacts c
			WHERE c.org_id = $1 AND ($2::bigint IS NULL OR c.lead_owner_id = $2)
			  AND c.created_at >= $3 AND c.created_at < $4
		),
		furthest AS (
			SELECT c.id, max(array_position($5::text[], s.stage)) AS rank
			FROM cohort c
			CROSS JOIN LATERAL (
				SELECT c.lifecycle_stage AS stage
				UNION ALL
				SELECT a.metadata->>'to' FROM contact_activities a
				WHERE a.contact_id = c.id AND a.kind = 'lifecycle_changed'
			) s
			GROUP BY c.id
		)
		SELECT COALESCE(rank, 1), count(*) FROM furthest GROUP BY 1`,
		orgID, ownerID, p.start, p.end, funnelStages)
	if err != nil {
		return Funnel{}, err
	}
	atRank := make([]int, len(funnelStages)+1)
	err = forEach(rows, func(row pgx.Row) error {
		var rank, n int
		if err := row.Scan(&rank, &n); err != nil {
			return err
		}
		if rank >= 1 && rank <= len(funnelStages) {
			atRank[rank] += n
		}
		return nil
	})
	if err != nil {
		return Funnel{}, err
	}
	// reached[i] = contacts whose furthest stage is i or beyond.
	reached := make([]int, len(funnelStages)+2)
	for i := len(funnelStages); i >= 1; i-- {
		reached[i] = reached[i+1] + atRank[i]
	}
	for i, stage := range funnelStages {
		fs := FunnelStage{Stage: stage, Reached: reached[i+1], FromFirstStagePercent: percent(reached[i+1], reached[1])}
		if i > 0 {
			fs.FromPreviousPercent = percent(reached[i+1], reached[i])
		} else if reached[1] > 0 {
			fs.FromPreviousPercent = 100
		}
		f.Stages = append(f.Stages, fs)
	}
	err = r.db.QueryRow(ctx, `SELECT count(*) FROM contacts c
		WHERE c.org_id = $1 AND ($2::bigint IS NULL OR c.lead_owner_id = $2)
		  AND c.created_at >= $3 AND c.created_at < $4 AND c.lifecycle_stage = 'lost'`,
		orgID, ownerID, p.start, p.end).Scan(&f.Lost)
	return f, err
}

// Pipeline is the open deals by stage now.
func (r *Repository) Pipeline(ctx context.Context, orgID int64, ownerID *int64, now time.Time) ([]PipelineStage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.name, s.probability, count(d.id),
			COALESCE(sum(`+dealValue+`), 0),
			COALESCE(sum(`+dealValue+` * s.probability / 100), 0),
			COALESCE(avg(EXTRACT(EPOCH FROM $3 - d.stage_entered_at) / 86400), 0)
		FROM deal_stages s
		LEFT JOIN deals d ON d.stage_id = s.id AND d.status = 'open' AND ($2::bigint IS NULL OR d.owner_id = $2)
		WHERE s.org_id = $1 AND s.kind = 'open'
		GROUP BY s.id ORDER BY s.position, s.id`, orgID, ownerID, now)
	if err != nil {
		return nil, err
	}
	out := make([]PipelineStage, 0)
	err = forEach(rows, func(row pgx.Row) error {
		var ps PipelineStage
		if err := row.Scan(&ps.StageID, &ps.Name, &ps.Probability, &ps.Deals, &ps.ValuePaise, &ps.WeightedPaise, &ps.AvgDaysInStage); err != nil {
			return err
		}
		ps.AvgDaysInStage = float64(int(ps.AvgDaysInStage*10+0.5)) / 10
		out = append(out, ps)
		return nil
	})
	return out, err
}

// Sources groups the contacts created in p by lead source.
func (r *Repository) Sources(ctx context.Context, orgID int64, ownerID *int64, p Period) ([]SourceRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(NULLIF(btrim(c.source), ''), '(none)') AS source,
			count(*),
			count(*) FILTER (WHERE s.temperature = 'hot'),
			count(*) FILTER (WHERE c.lifecycle_stage = 'customer'),
			COALESCE(sum((SELECT sum(`+dealValue+`) FROM deals d WHERE d.contact_id = c.id AND d.status = 'won')), 0)
		FROM contacts c LEFT JOIN lead_scores s ON s.contact_id = c.id
		WHERE c.org_id = $1 AND ($2::bigint IS NULL OR c.lead_owner_id = $2)
		  AND c.created_at >= $3 AND c.created_at < $4
		GROUP BY 1 ORDER BY 2 DESC, 1`, orgID, ownerID, p.start, p.end)
	if err != nil {
		return nil, err
	}
	out := make([]SourceRow, 0)
	err = forEach(rows, func(row pgx.Row) error {
		var s SourceRow
		if err := row.Scan(&s.Source, &s.Leads, &s.HotLeads, &s.Customers, &s.WonPaise); err != nil {
			return err
		}
		s.ConversionPercent = percent(s.Customers, s.Leads)
		out = append(out, s)
		return nil
	})
	return out, err
}

// Reps reports each user of the org for p.
func (r *Repository) Reps(ctx context.Context, orgID int64, p Period, now time.Time) ([]RepRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.name, u.role,
			(SELECT count(*) FROM contacts c WHERE c.org_id = $1 AND c.lead_owner_id = u.id
			   AND c.created_at >= $2 AND c.created_at < $3),
			count(d.id) FILTER (WHERE d.status = 'open'),
			COALESCE(sum(`+dealValue+`) FILTER (WHERE d.status = 'open'), 0),
			count(d.id) FILTER (WHERE d.status = 'won' AND d.closed_at >= $2 AND d.closed_at < $3),
			COALESCE(sum(`+dealValue+`) FILTER (WHERE d.status = 'won' AND d.closed_at >= $2 AND d.closed_at < $3), 0),
			count(d.id) FILTER (WHERE d.status = 'lost' AND d.closed_at >= $2 AND d.closed_at < $3),
			COALESCE(sum(`+dealValue+`) FILTER (WHERE d.status = 'lost' AND d.closed_at >= $2 AND d.closed_at < $3), 0),
			(SELECT count(*) FROM tasks t WHERE t.org_id = $1 AND t.assignee_id = u.id AND t.status = 'open' AND t.due_at < $4)
		FROM users u
		LEFT JOIN deals d ON d.org_id = $1 AND d.owner_id = u.id
		WHERE u.org_id = $1
		GROUP BY u.id ORDER BY 8 DESC, u.name`, orgID, p.start, p.end, now)
	if err != nil {
		return nil, err
	}
	out := make([]RepRow, 0)
	err = forEach(rows, func(row pgx.Row) error {
		var rr RepRow
		if err := row.Scan(&rr.UserID, &rr.Name, &rr.Role, &rr.LeadsAssigned, &rr.OpenDeals.Count, &rr.OpenDeals.ValuePaise,
			&rr.Won.Count, &rr.Won.ValuePaise, &rr.Lost.Count, &rr.Lost.ValuePaise, &rr.OverdueTasks); err != nil {
			return err
		}
		rr.WinRatePercent = percent(rr.Won.Count, rr.Won.Count+rr.Lost.Count)
		out = append(out, rr)
		return nil
	})
	return out, err
}

// Revenue fills months (IST, "YYYY-MM") from start (a month's first
// instant) to end (exclusive).
func (r *Repository) Revenue(ctx context.Context, orgID int64, ownerID *int64, start, end time.Time, months []RevenueMonth) error {
	index := map[string]int{}
	for i, m := range months {
		index[m.Month] = i
	}
	add := func(sql string, apply func(m *RevenueMonth, n int, paise int64)) error {
		rows, err := r.db.Query(ctx, sql, orgID, ownerID, start, end)
		if err != nil {
			return err
		}
		return forEach(rows, func(row pgx.Row) error {
			var month string
			var n int
			var paise int64
			if err := row.Scan(&month, &n, &paise); err != nil {
				return err
			}
			if i, ok := index[month]; ok {
				apply(&months[i], n, paise)
			}
			return nil
		})
	}
	month := func(col string) string { return `to_char(` + col + ` AT TIME ZONE 'Asia/Kolkata', 'YYYY-MM')` }
	// Won deals: count, value, and the recurring share (new ARR).
	rows, err := r.db.Query(ctx, `SELECT `+month("d.closed_at")+`, count(*), COALESCE(sum(`+dealValue+`), 0),
			COALESCE(sum(`+dealValue+`) FILTER (WHERE d.billing_type = 'recurring'), 0)
		FROM deals d WHERE d.org_id = $1 AND ($2::bigint IS NULL OR d.owner_id = $2) AND d.status = 'won'
		  AND d.closed_at >= $3 AND d.closed_at < $4 GROUP BY 1`, orgID, ownerID, start, end)
	if err != nil {
		return err
	}
	err = forEach(rows, func(row pgx.Row) error {
		var m string
		var n int
		var won, arr int64
		if err := row.Scan(&m, &n, &won, &arr); err != nil {
			return err
		}
		if i, ok := index[m]; ok {
			months[i].WonDeals, months[i].WonPaise, months[i].NewARRPaise = n, won, arr
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := add(`SELECT `+month("f.closed_at")+`, count(*), 0::bigint FROM (
			SELECT DISTINCT ON (d.contact_id) d.contact_id, d.closed_at, d.owner_id FROM deals d
			WHERE d.org_id = $1 AND d.status = 'won' AND d.contact_id IS NOT NULL
			ORDER BY d.contact_id, d.closed_at, d.id
		) f WHERE f.closed_at >= $3 AND f.closed_at < $4 AND ($2::bigint IS NULL OR f.owner_id = $2) GROUP BY 1`,
		func(m *RevenueMonth, n int, _ int64) { m.NewCustomers = n }); err != nil {
		return err
	}
	return add(`SELECT to_char(i.issue_date, 'YYYY-MM'), count(*), COALESCE(sum(i.total_paise), 0) FROM invoices i
		WHERE i.org_id = $1 AND ($2::bigint IS NULL OR i.owner_id = $2) AND i.status = 'issued'
		  AND i.issue_date >= ($3::timestamptz AT TIME ZONE 'Asia/Kolkata')::date
		  AND i.issue_date < ($4::timestamptz AT TIME ZONE 'Asia/Kolkata')::date GROUP BY 1`,
		func(m *RevenueMonth, _ int, paise int64) { m.InvoicedPaise = paise })
}

// Settings returns the org's report settings (weekly email on by default).
func (r *Repository) Settings(ctx context.Context, orgID int64) (Settings, error) {
	s := Settings{WeeklyEmail: true}
	err := r.db.QueryRow(ctx, `SELECT weekly_email, updated_at FROM report_settings WHERE org_id = $1`, orgID).
		Scan(&s.WeeklyEmail, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

func (r *Repository) SaveSettings(ctx context.Context, orgID int64, weekly bool) (Settings, error) {
	var s Settings
	err := r.db.QueryRow(ctx,
		`INSERT INTO report_settings (org_id, weekly_email, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (org_id) DO UPDATE SET weekly_email = EXCLUDED.weekly_email, updated_at = EXCLUDED.updated_at
		RETURNING weekly_email, updated_at`, orgID, weekly).Scan(&s.WeeklyEmail, &s.UpdatedAt)
	return s, err
}

// Recipient is someone who receives the weekly report.
type Recipient struct {
	ID    int64
	Name  string
	Email string
}

// ClaimWeekly records that orgs' weekly report for periodStart is being
// sent, returning the orgs (with the email enabled) that had not had it
// yet. Claiming first makes each report go out once, even with several
// instances.
func (r *Repository) ClaimWeekly(ctx context.Context, periodStart string) ([]int64, error) {
	rows, err := r.db.Query(ctx, `
		INSERT INTO report_runs (org_id, kind, period_start)
		SELECT o.id, 'weekly', $1::date FROM orgs o
		LEFT JOIN report_settings rs ON rs.org_id = o.id
		WHERE COALESCE(rs.weekly_email, true)
		ON CONFLICT DO NOTHING
		RETURNING org_id`, periodStart)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// Managers are the org's owners, admins and managers.
func (r *Repository) Managers(ctx context.Context, orgID int64) ([]Recipient, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, name, email FROM users WHERE org_id = $1 AND role IN ('owner', 'admin', 'manager') ORDER BY id`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Recipient, error) {
		var rc Recipient
		err := row.Scan(&rc.ID, &rc.Name, &rc.Email)
		return rc, err
	})
}

func (r *Repository) OrgName(ctx context.Context, orgID int64) (string, error) {
	var name string
	err := r.db.QueryRow(ctx, `SELECT name FROM orgs WHERE id = $1`, orgID).Scan(&name)
	return name, err
}

func (r *Repository) SetRecipients(ctx context.Context, orgID int64, periodStart string, n int) error {
	_, err := r.db.Exec(ctx,
		`UPDATE report_runs SET recipients = $3 WHERE org_id = $1 AND kind = 'weekly' AND period_start = $2::date`,
		orgID, periodStart, n)
	return err
}

func forEach(rows pgx.Rows, fn func(pgx.Row) error) error {
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

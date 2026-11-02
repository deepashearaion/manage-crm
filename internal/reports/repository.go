package reports

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// SalesDashboard computes the dashboard figures for one org, limited to the
// deals and contacts ownerID owns when it is set. Current definitions (to
// be revised with the deals module): open deals are those not in Closed
// Won/Lost (deals without a stage count nowhere); new customers are contacts
// created since midnight IST; ARR and MRR sum every deal.
func (r *Repository) SalesDashboard(ctx context.Context, orgID int64, ownerID *int64) (SalesDashboard, error) {
	var d SalesDashboard
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE ds.name NOT IN ('Closed Won', 'Closed Lost')),
			COALESCE(SUM(d.amount) FILTER (WHERE ds.name NOT IN ('Closed Won', 'Closed Lost')), 0),
			COALESCE(SUM(d.amount) FILTER (WHERE ds.name = 'Closed Won'), 0),
			COALESCE(SUM(d.amount) FILTER (WHERE ds.name = 'Closed Lost'), 0),
			(
				SELECT COUNT(*)
				FROM contacts c
				WHERE c.org_id = $1
				  AND ($2::bigint IS NULL OR c.lead_owner_id = $2)
				  -- "today" is the current day in IST, whatever the session time zone
				  AND c.created_at >= date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata'
			),
			COALESCE(SUM(d.annual_recurring_revenue), 0),
			COALESCE(SUM(d.monthly_recurring_revenue), 0)
		FROM deals d
		LEFT JOIN deal_stages ds ON d.stage_id = ds.id
		WHERE d.org_id = $1
		  AND ($2::bigint IS NULL OR d.owner_id = $2)`,
		orgID, ownerID,
	).Scan(
		&d.TotalOpenDeals,
		&d.TotalOpenDealValue,
		&d.TotalWon,
		&d.TotalLoss,
		&d.TotalNewCustomers,
		&d.TotalARR,
		&d.TotalMRR,
	)
	return d, err
}

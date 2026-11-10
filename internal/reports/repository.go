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
// deals and contacts ownerID owns when it is set. Deal values come from
// crm_deal_value_paise (a one-time amount, or a recurring deal's first-year
// value). ARR counts only won recurring deals, and MRR is ARR / 12. New
// customers are still contacts created since midnight IST; phase 7f
// redefines them as first-ever won deals in the period.
func (r *Repository) SalesDashboard(ctx context.Context, orgID int64, ownerID *int64) (SalesDashboard, error) {
	var d SalesDashboard
	var openValue, won, lost, arr int64
	err := r.db.QueryRow(ctx, `
		WITH v AS (
			SELECT d.status, d.billing_type,
				crm_deal_value_paise(d.amount_paise, d.billing_type, d.billing_period) AS value
			FROM deals d
			WHERE d.org_id = $1 AND ($2::bigint IS NULL OR d.owner_id = $2)
		)
		SELECT
			COUNT(*) FILTER (WHERE status = 'open'),
			COALESCE(SUM(value) FILTER (WHERE status = 'open'), 0),
			COALESCE(SUM(value) FILTER (WHERE status = 'won'), 0),
			COALESCE(SUM(value) FILTER (WHERE status = 'lost'), 0),
			(
				SELECT COUNT(*)
				FROM contacts c
				WHERE c.org_id = $1
				  AND ($2::bigint IS NULL OR c.lead_owner_id = $2)
				  -- "today" is the current day in IST, whatever the session time zone
				  AND c.created_at >= date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata'
			),
			COALESCE(SUM(value) FILTER (WHERE status = 'won' AND billing_type = 'recurring'), 0)
		FROM v`,
		orgID, ownerID,
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

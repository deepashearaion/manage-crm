package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
)

type SalesDashboardHandler struct {
	DB *pgx.Conn
}

type SalesDashboardResponse struct {
	TotalOpenDeals     int     `json:"total_open_deals"`
	TotalOpenDealValue float64 `json:"total_open_deal_value"`
	TotalWon           float64 `json:"total_won"`
	TotalLoss          float64 `json:"total_loss"`
	TotalNewCustomers  int     `json:"total_new_customers"`
	TotalARR           float64 `json:"total_arr"`
	TotalMRR           float64 `json:"total_mrr"`
}

func (h *SalesDashboardHandler) GetSalesDashboard(w http.ResponseWriter, r *http.Request) {

	ctx := context.Background()

	var response SalesDashboardResponse

	query := `
		SELECT
			COUNT(*) FILTER (
				WHERE ds.name NOT IN ('Closed Won', 'Closed Lost')
			),
			COALESCE(
				SUM(d.amount) FILTER (
					WHERE ds.name NOT IN ('Closed Won', 'Closed Lost')
				),
				0
			),
			COALESCE(
				SUM(d.amount) FILTER (
					WHERE ds.name = 'Closed Won'
				),
				0
			),
			COALESCE(
				SUM(d.amount) FILTER (
					WHERE ds.name = 'Closed Lost'
				),
				0
			),
			(
				SELECT COUNT(*)
				FROM contacts
				WHERE created_at >= CURRENT_DATE
			),
			COALESCE(SUM(d.annual_recurring_revenue), 0),
			COALESCE(SUM(d.monthly_recurring_revenue), 0)
		FROM deals d
		LEFT JOIN deal_stages ds
			ON d.stage_id = ds.id
	`

	err := h.DB.QueryRow(ctx, query).Scan(
		&response.TotalOpenDeals,
		&response.TotalOpenDealValue,
		&response.TotalWon,
		&response.TotalLoss,
		&response.TotalNewCustomers,
		&response.TotalARR,
		&response.TotalMRR,
	)

	if err != nil {
		http.Error(
			w,
			"Failed to load sales dashboard",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(response)
}

// Package reports serves dashboards and reports. Unlike other modules it
// reads across other modules' tables, but only with read-only aggregate
// queries; it never writes to them.
package reports

// SalesDashboard is the summary shown on the home page. Amounts are rupees.
type SalesDashboard struct {
	TotalOpenDeals     int     `json:"total_open_deals"`
	TotalOpenDealValue float64 `json:"total_open_deal_value"`
	TotalWon           float64 `json:"total_won"`
	TotalLoss          float64 `json:"total_loss"`
	TotalNewCustomers  int     `json:"total_new_customers"`
	TotalARR           float64 `json:"total_arr"`
	TotalMRR           float64 `json:"total_mrr"`
}

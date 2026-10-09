package models

type Deal struct {
	ID                      int64   `json:"id"`
	Title                   string  `json:"title"`
	Amount                  float64 `json:"amount"`
	StageID                 int64   `json:"stage_id"`
	OwnerID                 *int64  `json:"owner_id,omitempty"`
	ContactID               *int64  `json:"contact_id,omitempty"`
	MonthlyRecurringRevenue float64 `json:"monthly_recurring_revenue"`
	AnnualRecurringRevenue  float64 `json:"annual_recurring_revenue"`
}

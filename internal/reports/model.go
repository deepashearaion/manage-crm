// Package reports serves dashboards and reports, and emails a weekly
// summary. Unlike other modules it reads across other modules' tables, but
// only with read-only queries; it writes only its own tables
// (report_settings, report_runs).
//
// Definitions (the user's): money is paise; a deal's value is
// crm_deal_value_paise (one-time amount, or a recurring deal's first-year
// value); won and lost deals count in the period their closed_at falls in;
// a new customer is a contact whose first-ever won deal closed in the
// period; ARR is the value of all won recurring deals and MRR is ARR / 12.
// Periods are IST calendar days.
package reports

import (
	"errors"
	"fmt"
	"time"
)

// SalesDashboard is the legacy home-page summary (/api/dashboard/sales).
// Amounts are rupees; "new customers" covers today (IST).
type SalesDashboard struct {
	TotalOpenDeals     int     `json:"total_open_deals"`
	TotalOpenDealValue float64 `json:"total_open_deal_value"`
	TotalWon           float64 `json:"total_won"`
	TotalLoss          float64 `json:"total_loss"`
	TotalNewCustomers  int     `json:"total_new_customers"`
	TotalARR           float64 `json:"total_arr"`
	TotalMRR           float64 `json:"total_mrr"`
}

// ist is India Standard Time (UTC+5:30, no daylight saving).
var ist = time.FixedZone("IST", 5*3600+1800)

const dayLayout = "2006-01-02"

// Period is a range of IST days, From and To inclusive.
type Period struct {
	From  string    `json:"from"`
	To    string    `json:"to"`
	start time.Time // From 00:00 IST
	end   time.Time // the day after To, 00:00 IST
}

const maxPeriodDays = 731

// parsePeriod reads from/to (YYYY-MM-DD, IST). The default is the current
// month up to today.
func parsePeriod(from, to string, now time.Time) (Period, error) {
	today := now.In(ist)
	if from == "" {
		from = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, ist).Format(dayLayout)
	}
	if to == "" {
		to = today.Format(dayLayout)
	}
	start, err1 := time.ParseInLocation(dayLayout, from, ist)
	last, err2 := time.ParseInLocation(dayLayout, to, ist)
	switch {
	case err1 != nil || err2 != nil:
		return Period{}, &FieldError{"from", "invalid", "from and to must be dates as YYYY-MM-DD."}
	case last.Before(start):
		return Period{}, &FieldError{"to", "invalid", "to must not be before from."}
	case last.Sub(start) > maxPeriodDays*24*time.Hour:
		return Period{}, &FieldError{"to", "invalid", fmt.Sprintf("A period can span at most %d days.", maxPeriodDays)}
	}
	return Period{From: from, To: to, start: start, end: last.AddDate(0, 0, 1)}, nil
}

// Count is a number of deals and their value.
type Count struct {
	Count      int   `json:"count"`
	ValuePaise int64 `json:"value_paise"`
}

type Dashboard struct {
	Period            Period  `json:"period"`
	OpenDeals         Count   `json:"open_deals"` // now
	Won               Count   `json:"won"`        // closed in the period
	Lost              Count   `json:"lost"`
	WinRatePercent    float64 `json:"win_rate_percent"` // won / (won + lost), by count
	AvgDealSizePaise  int64   `json:"avg_deal_size_paise"`
	AvgSalesCycleDays float64 `json:"avg_sales_cycle_days"` // deal created → won
	NewCustomers      int     `json:"new_customers"`
	NewLeads          int     `json:"new_leads"`
	HotLeads          int     `json:"hot_leads"` // now: hot and not yet a customer
	InvoicedPaise     int64   `json:"invoiced_paise"`
	ARRPaise          int64   `json:"arr_paise"` // now
	MRRPaise          int64   `json:"mrr_paise"`
}

// FunnelStage is how many contacts in the cohort ever reached a stage.
type FunnelStage struct {
	Stage                 string  `json:"stage"`
	Reached               int     `json:"reached"`
	FromPreviousPercent   float64 `json:"from_previous_percent"`
	FromFirstStagePercent float64 `json:"from_first_stage_percent"`
}

// Funnel follows the contacts created in the period through the
// lifecycle stages, using the stages each has ever been in.
type Funnel struct {
	Period Period        `json:"period"`
	Stages []FunnelStage `json:"stages"`
	Lost   int           `json:"lost"` // currently lost
}

type PipelineStage struct {
	StageID        int64   `json:"stage_id"`
	Name           string  `json:"name"`
	Probability    int     `json:"probability"`
	Deals          int     `json:"deals"`
	ValuePaise     int64   `json:"value_paise"`
	WeightedPaise  int64   `json:"weighted_paise"`
	AvgDaysInStage float64 `json:"avg_days_in_stage"`
}

type SourceRow struct {
	Source            string  `json:"source"`
	Leads             int     `json:"leads"`
	HotLeads          int     `json:"hot_leads"`
	Customers         int     `json:"customers"`
	ConversionPercent float64 `json:"conversion_percent"`
	WonPaise          int64   `json:"won_paise"`
}

type RepRow struct {
	UserID         int64   `json:"user_id"`
	Name           string  `json:"name"`
	Role           string  `json:"role"`
	LeadsAssigned  int     `json:"leads_assigned"` // created in the period
	OpenDeals      Count   `json:"open_deals"`
	Won            Count   `json:"won"`
	Lost           Count   `json:"lost"`
	WinRatePercent float64 `json:"win_rate_percent"`
	OverdueTasks   int     `json:"overdue_tasks"` // now
}

type RevenueMonth struct {
	Month         string `json:"month"` // YYYY-MM (IST)
	WonDeals      int    `json:"won_deals"`
	WonPaise      int64  `json:"won_paise"`
	NewARRPaise   int64  `json:"new_arr_paise"` // recurring deals won
	NewCustomers  int    `json:"new_customers"`
	InvoicedPaise int64  `json:"invoiced_paise"`
}

type Settings struct {
	WeeklyEmail bool      `json:"weekly_email"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field, Problem, Message string
}

func (e *FieldError) Error() string { return e.Message }

var ErrForbidden = errors.New("not allowed")

func percent(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(int(float64(part)*1000/float64(whole)+0.5)) / 10
}

package deals

import (
	"fmt"
	"time"
)

// ist is India Standard Time (UTC+5:30, no daylight saving): forecast
// months and "today" are IST, as the org works; storage stays UTC.
var ist = time.FixedZone("IST", 5*3600+1800)

// Rule-based win probability: the stage's probability, discounted for
// signs a deal is slipping. An AI forecast can replace this later.
const (
	staleAfter     = 30 * 24 * time.Hour
	staleFactor    = 0.5
	overdueFactor  = 0.5
	undatedFactor  = 0.8
	commitAtLeast  = 70 // adjusted probability that counts as "commit"
	dateLayout     = "2006-01-02"
	monthLayout    = "2006-01"
	maxMonths      = 12
	defaultMonths  = 3
	forecastMethod = "rules"
)

// winProbability returns a deal's adjusted win probability and why.
func winProbability(kind string, stageProbability int, stageEnteredAt time.Time, expectedClose *string, now time.Time) (int, []string) {
	switch kind {
	case KindWon:
		return 100, []string{"Won"}
	case KindLost:
		return 0, []string{"Lost"}
	}
	p := float64(stageProbability)
	notes := []string{fmt.Sprintf("Stage probability %d%%", stageProbability)}
	if days := int(now.Sub(stageEnteredAt) / (24 * time.Hour)); now.Sub(stageEnteredAt) > staleAfter {
		p *= staleFactor
		notes = append(notes, fmt.Sprintf("No stage change for %d days (×%.1f)", days, staleFactor))
	}
	switch {
	case expectedClose == nil:
		p *= undatedFactor
		notes = append(notes, fmt.Sprintf("No expected close date (×%.1f)", undatedFactor))
	case *expectedClose < now.In(ist).Format(dateLayout):
		p *= overdueFactor
		notes = append(notes, fmt.Sprintf("Expected close date has passed (×%.1f)", overdueFactor))
	}
	return int(p + 0.5), notes
}

// ForecastMonth is one IST calendar month of the forecast. Values are
// deal values (one-time amount or first-year value) in paise.
type ForecastMonth struct {
	Month string `json:"month"` // YYYY-MM
	// Won in the month.
	WonDeals int   `json:"won_deals"`
	WonPaise int64 `json:"won_paise"`
	// Open deals expected to close in the month (overdue ones count in
	// the current month).
	OpenDeals     int   `json:"open_deals"`
	BestCasePaise int64 `json:"best_case_paise"` // every open deal closes
	WeightedPaise int64 `json:"weighted_paise"`  // value × stage probability
	ForecastPaise int64 `json:"forecast_paise"`  // value × adjusted win probability
	CommitPaise   int64 `json:"commit_paise"`    // deals with adjusted probability ≥ 70%
}

// Bucket is a count and value of deals.
type Bucket struct {
	Deals      int   `json:"deals"`
	ValuePaise int64 `json:"value_paise"`
}

type Forecast struct {
	Method  string          `json:"method"`
	Months  []ForecastMonth `json:"months"`
	Overdue Bucket          `json:"overdue"` // open, expected close date passed
	Undated Bucket          `json:"undated"` // open, no expected close date (not in months)
}

// openDeal and wonDeal are the rows the forecast needs.
type openDeal struct {
	ValuePaise       int64
	StageProbability int
	StageEnteredAt   time.Time
	ExpectedClose    *string
}

type wonDeal struct {
	ValuePaise int64
	ClosedAt   time.Time
}

// buildForecast aggregates deals into months starting at from (the first
// of an IST month).
func buildForecast(from time.Time, months int, open []openDeal, won []wonDeal, now time.Time) Forecast {
	f := Forecast{Method: forecastMethod, Months: make([]ForecastMonth, months)}
	index := map[string]int{}
	for i := range months {
		m := from.AddDate(0, i, 0).Format(monthLayout)
		f.Months[i].Month = m
		index[m] = i
	}
	today := now.In(ist).Format(dateLayout)
	thisMonth := now.In(ist).Format(monthLayout)

	for _, w := range won {
		if i, ok := index[w.ClosedAt.In(ist).Format(monthLayout)]; ok {
			f.Months[i].WonDeals++
			f.Months[i].WonPaise += w.ValuePaise
		}
	}
	for _, d := range open {
		if d.ExpectedClose == nil {
			f.Undated.Deals++
			f.Undated.ValuePaise += d.ValuePaise
			continue
		}
		month := (*d.ExpectedClose)[:7]
		if *d.ExpectedClose < today {
			f.Overdue.Deals++
			f.Overdue.ValuePaise += d.ValuePaise
			month = thisMonth
		}
		i, ok := index[month]
		if !ok {
			continue
		}
		p, _ := winProbability(KindOpen, d.StageProbability, d.StageEnteredAt, d.ExpectedClose, now)
		m := &f.Months[i]
		m.OpenDeals++
		m.BestCasePaise += d.ValuePaise
		m.WeightedPaise += d.ValuePaise * int64(d.StageProbability) / 100
		m.ForecastPaise += d.ValuePaise * int64(p) / 100
		if p >= commitAtLeast {
			m.CommitPaise += d.ValuePaise
		}
	}
	return f
}

// monthStart parses YYYY-MM (empty: the current IST month) as the first
// instant of that month in IST.
func monthStart(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		raw = now.In(ist).Format(monthLayout)
	}
	return time.ParseInLocation(monthLayout, raw, ist)
}

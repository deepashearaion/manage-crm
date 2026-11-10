package deals

import (
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestWinProbability(t *testing.T) {
	now := time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC) // 11:30 IST
	fresh := now.Add(-24 * time.Hour)
	stale := now.Add(-40 * 24 * time.Hour)
	for _, tc := range []struct {
		name    string
		kind    string
		prob    int
		entered time.Time
		close   *string
		want    int
		notes   int
	}{
		{"won", KindWon, 100, stale, nil, 100, 1},
		{"lost", KindLost, 0, fresh, nil, 0, 1},
		{"on track", KindOpen, 50, fresh, ptr("2026-12-01"), 50, 1},
		{"closes today", KindOpen, 50, fresh, ptr("2026-11-10"), 50, 1},
		{"undated", KindOpen, 50, fresh, nil, 40, 2},
		{"overdue", KindOpen, 50, fresh, ptr("2026-11-09"), 25, 2},
		{"stale and overdue", KindOpen, 75, stale, ptr("2026-11-01"), 19, 3},
	} {
		got, notes := winProbability(tc.kind, tc.prob, tc.entered, tc.close, now)
		if got != tc.want || len(notes) != tc.notes {
			t.Errorf("%s: %d %v, want %d with %d notes", tc.name, got, notes, tc.want, tc.notes)
		}
	}
}

func TestBuildForecast(t *testing.T) {
	now := time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC)
	from, err := monthStart("", now)
	if err != nil || from.Format(time.RFC3339) != "2026-11-01T00:00:00+05:30" {
		t.Fatalf("monthStart = %v, %v", from, err)
	}
	fresh := now.Add(-time.Hour)
	f := buildForecast(from, 2, []openDeal{
		{ValuePaise: 100000, StageProbability: 75, StageEnteredAt: fresh, ExpectedClose: ptr("2026-11-20")}, // commit
		{ValuePaise: 50000, StageProbability: 50, StageEnteredAt: fresh, ExpectedClose: ptr("2026-12-05")},
		{ValuePaise: 20000, StageProbability: 50, StageEnteredAt: fresh, ExpectedClose: ptr("2026-10-01")}, // overdue → Nov
		{ValuePaise: 70000, StageProbability: 25, StageEnteredAt: fresh},                                   // undated
		{ValuePaise: 99000, StageProbability: 25, StageEnteredAt: fresh, ExpectedClose: ptr("2027-03-01")}, // outside
	}, []wonDeal{
		{ValuePaise: 300000, ClosedAt: time.Date(2026, 10, 31, 19, 0, 0, 0, time.UTC)}, // 1 Nov 00:30 IST
		{ValuePaise: 1, ClosedAt: time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC)},      // 31 Oct IST
	}, now)

	nov, dec := f.Months[0], f.Months[1]
	if nov.Month != "2026-11" || nov.WonDeals != 1 || nov.WonPaise != 300000 || nov.OpenDeals != 2 ||
		nov.BestCasePaise != 120000 || nov.WeightedPaise != 85000 || nov.ForecastPaise != 80000 || nov.CommitPaise != 100000 {
		t.Errorf("November = %+v", nov)
	}
	if dec.Month != "2026-12" || dec.OpenDeals != 1 || dec.ForecastPaise != 25000 || dec.CommitPaise != 0 {
		t.Errorf("December = %+v", dec)
	}
	if f.Overdue != (Bucket{1, 20000}) || f.Undated != (Bucket{1, 70000}) || f.Method != "rules" {
		t.Errorf("overdue %+v undated %+v", f.Overdue, f.Undated)
	}
	if _, err := monthStart("2026-13", now); err == nil {
		t.Error("month 13 accepted")
	}
}

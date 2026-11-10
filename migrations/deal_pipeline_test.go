package migrations_test

import (
	"context"
	"testing"

	"github.com/deepashearaion/manage-crm/internal/testutil"
	"github.com/deepashearaion/manage-crm/migrations"
)

// Legacy NUMERIC rupee deals convert to paise with a billing type, and
// default stages get their kind and probability.
func TestDealPipelineBackfill(t *testing.T) {
	db := testutil.FreshDB(t)
	ctx := context.Background()
	url, err := testutil.TestDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	provider, sqlDB, err := migrations.Open(url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	defer func() {
		if _, err := provider.Up(ctx); err != nil {
			t.Errorf("migrate back up: %v", err)
		}
	}()
	if _, err := provider.DownTo(ctx, 10); err != nil {
		t.Fatalf("migrate down to 10: %v", err)
	}
	db.Reset()

	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO orgs (name) VALUES ('Acme')`)
	exec(`INSERT INTO deal_stages (org_id, name) VALUES (1, 'Proposal'), (1, 'Closed Won'), (1, 'Custom')`)
	exec(`INSERT INTO deals (org_id, title, amount, stage_id, monthly_recurring_revenue, annual_recurring_revenue) VALUES
		(1, 'one-time', 1234.56, 1, 0, 0),
		(1, 'yearly', 999, 2, 50, 600),
		(1, 'monthly', 0, 1, 25.5, 0),
		(1, 'no stage', 10, NULL, 0, 0)`)

	if _, err := provider.UpTo(ctx, 11); err != nil {
		t.Fatalf("migrate up to 11: %v", err)
	}
	db.Reset()

	rows, err := db.Query(ctx, `SELECT d.title, d.amount_paise, d.billing_type, COALESCE(d.billing_period, ''), d.status,
		d.closed_at IS NOT NULL, s.name FROM deals d JOIN deal_stages s ON s.id = d.stage_id ORDER BY d.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []struct {
		title  string
		paise  int64
		btype  string
		period string
		status string
		closed bool
		stage  string
	}{
		{"one-time", 123456, "one_time", "", "open", false, "Proposal"},
		{"yearly", 60000, "recurring", "yearly", "won", true, "Closed Won"},
		{"monthly", 2550, "recurring", "monthly", "open", false, "Proposal"},
		{"no stage", 1000, "one_time", "", "open", false, "Proposal"}, // the org's first stage
	}
	i := 0
	for rows.Next() {
		var got = want[0]
		if err := rows.Scan(&got.title, &got.paise, &got.btype, &got.period, &got.status, &got.closed, &got.stage); err != nil {
			t.Fatal(err)
		}
		if i >= len(want) || got != want[i] {
			t.Errorf("deal %d = %+v, want %+v", i, got, want[min(i, len(want)-1)])
		}
		i++
	}
	if i != len(want) {
		t.Errorf("%d deals, want %d", i, len(want))
	}

	var kind string
	var probability int
	if err := db.QueryRow(ctx, `SELECT kind, probability FROM deal_stages WHERE name = 'Closed Won'`).Scan(&kind, &probability); err != nil {
		t.Fatal(err)
	}
	if kind != "won" || probability != 100 {
		t.Errorf("Closed Won = %s %d", kind, probability)
	}
}

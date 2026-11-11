package migrations_test

import (
	"context"
	"testing"

	"github.com/deepashearaion/manage-crm/internal/testutil"
	"github.com/deepashearaion/manage-crm/migrations"
)

// Rolls the test database back to before multi-tenancy, adds data as the
// old schema allowed, migrates forward and checks where everything landed.
func TestOrgsBackfill(t *testing.T) {
	db := testutil.FreshDB(t) // holds the data lock while the schema is rolled back
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
	// Leave the schema at the latest version whatever happens.
	defer func() {
		if _, err := provider.Up(ctx); err != nil {
			t.Errorf("migrate back up: %v", err)
		}
	}()

	if _, err := provider.DownTo(ctx, 6); err != nil {
		t.Fatalf("migrate down to 6: %v", err)
	}
	// The pool's cached statement plans refer to the old columns.
	db.Reset()

	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO users (name, email, password_hash) VALUES
		('First', 'first@example.com', 'x'), ('Second', 'second@example.com', 'x')`)
	exec(`INSERT INTO deal_stages (name) VALUES ('Proposal'), ('Closed Won')`)
	exec(`INSERT INTO contacts (first_name, lead_owner_id) VALUES ('Ravi', 2)`)
	exec(`INSERT INTO deals (title, stage_id, contact_id) SELECT 'Deal', id, 1 FROM deal_stages WHERE name = 'Proposal'`)

	if _, err := provider.UpTo(ctx, 8); err != nil {
		t.Fatalf("migrate up to 8: %v", err)
	}
	db.Reset()

	var orgs int
	var orgName string
	if err := db.QueryRow(ctx, `SELECT count(*), min(name) FROM orgs`).Scan(&orgs, &orgName); err != nil {
		t.Fatal(err)
	}
	if orgs != 1 || orgName != "Default organization" {
		t.Fatalf("orgs = %d (%q), want one Default organization", orgs, orgName)
	}

	rows, err := db.Query(ctx, `SELECT email, role, org_id FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"first@example.com", "owner"}, {"second@example.com", "admin"}}
	i := 0
	for rows.Next() {
		var email, role string
		var orgID int64
		if err := rows.Scan(&email, &role, &orgID); err != nil {
			t.Fatal(err)
		}
		if i >= len(want) || email != want[i][0] || role != want[i][1] || orgID != 1 {
			t.Errorf("user %d = %s/%s/org %d, want %v in org 1", i, email, role, orgID, want)
		}
		i++
	}
	rows.Close()

	for _, table := range []string{"contacts", "deals", "deal_stages"} {
		var missing int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE org_id IS DISTINCT FROM 1`).Scan(&missing); err != nil {
			t.Fatal(err)
		}
		if missing != 0 {
			t.Errorf("%s: %d rows not in the default org", table, missing)
		}
	}
}

// A contact's owner must belong to the contact's org.
func TestContactOwnerMustShareOrg(t *testing.T) {
	db := testutil.FreshDB(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `
		INSERT INTO orgs (name) VALUES ('A'), ('B');
		INSERT INTO roles (org_id, name) VALUES (2, 'Rep');
		INSERT INTO users (name, email, password_hash, org_id, role_id) VALUES ('In B', 'b@example.com', 'x', 2, 1);
	`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(ctx, `INSERT INTO contacts (first_name, org_id, lead_owner_id) VALUES ('X', 1, 1)`)
	if err == nil {
		t.Fatal("contact in org A owned by a user of org B was accepted")
	}
}

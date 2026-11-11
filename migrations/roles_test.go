package migrations_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/deepashearaion/manage-crm/internal/auth"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
	"github.com/deepashearaion/manage-crm/internal/testutil"
	"github.com/deepashearaion/manage-crm/migrations"
)

// Existing orgs get the default roles (the same as auth.DefaultRoles gives
// new orgs), and each user the role matching their old fixed role. Going
// back down restores the fixed roles.
func TestRolesBackfill(t *testing.T) {
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
	if _, err := provider.DownTo(ctx, 14); err != nil {
		t.Fatalf("migrate down to 14: %v", err)
	}
	db.Reset()

	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO orgs (name) VALUES ('A'), ('B')`)
	exec(`INSERT INTO users (org_id, role, name, email, password_hash) VALUES
		(1, 'owner', 'o', 'o@a.test', 'x'), (1, 'rep', 'r', 'r@a.test', 'x'), (1, 'viewer', 'v', 'v@a.test', 'x'),
		(2, 'owner', 'o2', 'o@b.test', 'x'), (2, 'manager', 'm2', 'm@b.test', 'x')`)

	if _, err := provider.UpTo(ctx, 15); err != nil {
		t.Fatalf("migrate up to 15: %v", err)
	}
	db.Reset()

	var got []string
	rows, err := db.Query(ctx, `SELECT u.email, r.name, r.org_id = u.org_id FROM users u JOIN roles r ON r.id = u.role_id ORDER BY u.id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var email, role string
		var sameOrg bool
		if err := rows.Scan(&email, &role, &sameOrg); err != nil {
			t.Fatal(err)
		}
		if !sameOrg {
			t.Errorf("%s has another org's role", email)
		}
		got = append(got, email+":"+role)
	}
	rows.Close()
	if strings.Join(got, " ") != "o@a.test:Owner r@a.test:Rep v@a.test:Viewer o@b.test:Owner m@b.test:Manager" {
		t.Errorf("user roles = %v", got)
	}

	// The seeded roles match what new orgs get.
	for _, d := range auth.DefaultRoles {
		var raw []byte
		var locked bool
		var count int
		if err := db.QueryRow(ctx, `SELECT permissions, is_owner, (SELECT count(*) FROM roles WHERE name = $1)
			FROM roles WHERE org_id = 2 AND name = $1`, d.Name).Scan(&raw, &locked, &count); err != nil {
			t.Fatalf("role %s: %v", d.Name, err)
		}
		var perms principal.Permissions
		if err := json.Unmarshal(raw, &perms); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(perms, d.Permissions.Normalized()) || locked != d.Locked || count != 2 {
			t.Errorf("role %s: locked %v count %d permissions\n%v\nwant\n%v", d.Name, locked, count, perms, d.Permissions.Normalized())
		}
	}

	// Down: a custom role falls back to viewer.
	exec(`INSERT INTO roles (org_id, name) VALUES (1, 'Support')`)
	exec(`UPDATE users SET role_id = (SELECT id FROM roles WHERE name = 'Support') WHERE email = 'r@a.test'`)
	if _, err := provider.DownTo(ctx, 14); err != nil {
		t.Fatalf("migrate down to 14: %v", err)
	}
	db.Reset()
	got = nil
	rows, err = db.Query(ctx, `SELECT email || ':' || role FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	rows.Close()
	if strings.Join(got, " ") != "o@a.test:owner r@a.test:viewer v@a.test:viewer o@b.test:owner m@b.test:manager" {
		t.Errorf("after down = %v", got)
	}
}

package migrations_test

import (
	"context"
	"testing"

	"github.com/deepashearaion/manage-crm/internal/testutil"
)

// crm_phone_key decides which phone numbers count as the same lead.
func TestPhoneKey(t *testing.T) {
	db := testutil.Pool(t)
	cases := map[string]any{ // input -> key (nil: no usable key)
		"98765 43210":       "919876543210",
		"098765-43210":      "919876543210",
		"+91 98765 43210":   "919876543210",
		"919876543210":      "919876543210",
		"0091 98765 43210":  "919876543210",
		"+1 (415) 555-0100": "14155550100",
		"12345":             nil,
		"":                  nil,
		"call me":           nil,
	}
	for in, want := range cases {
		var got *string
		if err := db.QueryRow(context.Background(), `SELECT crm_phone_key($1)`, in).Scan(&got); err != nil {
			t.Fatal(err)
		}
		switch {
		case want == nil && got != nil:
			t.Errorf("crm_phone_key(%q) = %q, want NULL", in, *got)
		case want != nil && (got == nil || *got != want):
			t.Errorf("crm_phone_key(%q) = %v, want %q", in, got, want)
		}
	}
}

func TestContactDedupeKeys(t *testing.T) {
	db := testutil.FreshDB(t)
	ctx := context.Background()
	var org int64
	if err := db.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ('Acme') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	var emailKey, phoneKey, stage string
	if err := db.QueryRow(ctx,
		`INSERT INTO contacts (org_id, first_name, email, mobile) VALUES ($1, 'Ravi', '  Ravi@Example.COM ', '98765 43210')
		RETURNING email_key, phone_key, lifecycle_stage`, org,
	).Scan(&emailKey, &phoneKey, &stage); err != nil {
		t.Fatal(err)
	}
	if emailKey != "ravi@example.com" || phoneKey != "919876543210" || stage != "lead" {
		t.Errorf("email_key %q, phone_key %q, lifecycle_stage %q", emailKey, phoneKey, stage)
	}
}

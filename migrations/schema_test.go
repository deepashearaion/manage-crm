package migrations_test

import (
	"context"
	"testing"

	"github.com/deepashearaion/manage-crm/internal/testutil"
)

// Every foreign-key column needs an index led by that column; without one,
// joins and ON DELETE checks on the referenced table scan the whole table.
func TestForeignKeysAreIndexed(t *testing.T) {
	db := testutil.Pool(t)

	rows, err := db.Query(context.Background(), `
		SELECT c.conrelid::regclass::text, a.attname
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f'
		  AND c.connamespace = 'public'::regnamespace
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_index i
		      WHERE i.indrelid = c.conrelid AND i.indkey[0] = c.conkey[1]
		  )
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		t.Errorf("foreign key %s.%s has no index", table, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// Timestamps must be timestamptz so they are absolute instants.
func TestNoTimestampWithoutTimeZone(t *testing.T) {
	db := testutil.Pool(t)

	rows, err := db.Query(context.Background(), `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND data_type = 'timestamp without time zone'
		  AND table_name <> 'goose_db_version'
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s.%s is timestamp without time zone; use timestamptz", table, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

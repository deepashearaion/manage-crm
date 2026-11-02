// Package migrations embeds the SQL schema migrations and applies them with goose.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// Open opens a database/sql handle for dsn and returns a goose provider over
// the embedded migrations. Close the returned *sql.DB when done.
func Open(dsn string) (*goose.Provider, *sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return provider, db, nil
}

// Up applies every pending migration to the database at dsn.
func Up(ctx context.Context, dsn string) ([]*goose.MigrationResult, error) {
	provider, db, err := Open(dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	results, err := provider.Up(ctx)
	if err != nil {
		return results, fmt.Errorf("migrate up: %w", err)
	}
	return results, nil
}

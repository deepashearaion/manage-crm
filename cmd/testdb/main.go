// Command testdb drops and recreates the test database named by
// TEST_DATABASE_URL and applies all migrations to it. It refuses to run
// against any database whose name does not end in "_test".
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"authentication-backend/internal/testutil"
	"authentication-backend/migrations"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal("testdb: ", err)
	}
}

func run(ctx context.Context) error {
	url, err := testutil.TestDatabaseURL()
	if err != nil {
		return err
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return err
	}
	name := cfg.Database

	// Connect to the server's maintenance database to drop and create the test one.
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer admin.Close(ctx)

	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop %s: %w", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}

	results, err := migrations.Up(ctx, url)
	if err != nil {
		return err
	}

	fmt.Printf("test database %s recreated, %d migrations applied\n", name, len(results))
	return nil
}

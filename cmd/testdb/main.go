// Command testdb drops and recreates the test database named by
// TEST_DATABASE_URL and loads the schema into it. It refuses to run against
// any database whose name does not end in "_test".
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	"authentication-backend/internal/testutil"
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

	root, err := testutil.RepoRoot()
	if err != nil {
		return err
	}
	schema, err := os.ReadFile(filepath.Join(root, "database", "schema.sql")) //nolint:gosec // fixed path inside the repo
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, string(schema)); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}

	fmt.Printf("test database %s recreated\n", name)
	return nil
}

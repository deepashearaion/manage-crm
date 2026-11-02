// Package testutil provides helpers for tests that run against the real
// PostgreSQL test database named by TEST_DATABASE_URL.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// RepoRoot walks up from the working directory to the directory holding go.mod.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// TestDatabaseURL returns TEST_DATABASE_URL (loading the repo .env if needed)
// and refuses any URL whose database name does not end in "_test", so tests can
// never truncate a real database.
func TestDatabaseURL() (string, error) {
	if root, err := RepoRoot(); err == nil {
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		return "", fmt.Errorf("TEST_DATABASE_URL is not set; add it to .env and run `make test-db`")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return "", fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		return "", fmt.Errorf("refusing to use database %q: TEST_DATABASE_URL must name a database ending in _test", cfg.Database)
	}
	return url, nil
}

// Execer is satisfied by *pgx.Conn and *pgxpool.Pool.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Truncate empties every data table and resets identities. Seed tables
// (deal_stages) are left intact.
func Truncate(ctx context.Context, db Execer) error {
	_, err := db.Exec(ctx, `TRUNCATE users, contacts, deals RESTART IDENTITY CASCADE`)
	return err
}

// Pool opens a connection pool to the test database, empties it, and closes
// it when the test ends. The test fails if TEST_DATABASE_URL is missing or unsafe.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, err := TestDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open test database pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := Truncate(ctx, pool); err != nil {
		t.Fatalf("truncate test database (run `make test-db` first?): %v", err)
	}
	return pool
}

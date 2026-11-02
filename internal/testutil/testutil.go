// Package testutil provides helpers for tests that run against the real
// PostgreSQL test database named by TEST_DATABASE_URL.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/deepashearaion/manage-crm/internal/platform/database"
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

var shared struct {
	once sync.Once
	pool *pgxpool.Pool
	err  error
}

// dataLock is the advisory-lock key that serializes tests which modify data,
// across all test binaries (go test runs packages in parallel).
const dataLock = 7_340_001

// Pool returns the connection pool to the test database, shared by all tests
// in the binary because connecting is slow on some machines. Use it as is only
// for read-only checks (such as the schema); tests that write data use
// FreshDB. The test fails if TEST_DATABASE_URL is missing or unsafe.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	shared.once.Do(func() {
		url, err := TestDatabaseURL()
		if err != nil {
			shared.err = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shared.pool, shared.err = database.Open(ctx, url)
	})
	if shared.err != nil {
		t.Fatalf("open test database pool: %v", shared.err)
	}
	return shared.pool
}

// FreshDB gives the calling test exclusive use of an emptied test database:
// it holds an advisory lock until the test ends, so tests that write data
// never overlap, even across packages.
func FreshDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := Pool(t)
	ctx := context.Background()

	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire lock connection: %v", err)
	}
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, dataLock); err != nil {
		lockConn.Release()
		t.Fatalf("lock test database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, dataLock)
		lockConn.Release()
	})

	if err := Truncate(ctx, pool); err != nil {
		t.Fatalf("truncate test database (run `make test-db` first?): %v", err)
	}
	return pool
}
